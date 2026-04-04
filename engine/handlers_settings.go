package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// handleUserSettings renders the user settings page.
func handleUserSettings(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	freshUser, err := GetUserByID(user.ID)
	if err != nil {
		log.Printf("settings: failed to reload user %d: %v", user.ID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Title":          "Settings",
		"HasGithubToken": freshUser.GithubToken != "",
	}
	RenderShell(w, r, "settings", data)
}

// handleUserSettingsSave processes the settings form submission.
func handleUserSettingsSave(w http.ResponseWriter, r *http.Request) {
	// CSRF check
	csrf := r.FormValue("_csrf")
	if csrf != CtxCSRF(r) {
		http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
		return
	}

	user := CtxUser(r)
	action := r.FormValue("action")

	switch action {
	case "save_github_token":
		token := strings.TrimSpace(r.FormValue("github_token"))
		if err := UpdateGithubToken(user.ID, token); err != nil {
			log.Printf("settings: failed to update GitHub token for user %d: %v", user.ID, err)
			RenderShell(w, r, "settings", map[string]interface{}{
				"Title":          "Settings",
				"HasGithubToken": false,
				"Error":          "Failed to save token. Please try again.",
			})
			return
		}
		RenderShell(w, r, "settings", map[string]interface{}{
			"Title":          "Settings",
			"HasGithubToken": token != "",
			"Success":        "GitHub token saved.",
		})
	case "remove_github_token":
		if err := UpdateGithubToken(user.ID, ""); err != nil {
			log.Printf("settings: failed to remove GitHub token for user %d: %v", user.ID, err)
		}
		RenderShell(w, r, "settings", map[string]interface{}{
			"Title":          "Settings",
			"HasGithubToken": false,
			"Success":        "GitHub token removed.",
		})
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

func handleProjectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		handleProjectDetail(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := r.URL.Query().Get("action")
	project := CtxProject(r)

	if action == "destroy" {
		// Validate CSRF
		csrf := r.FormValue("_csrf")
		if csrf == "" {
			csrf = r.Header.Get("X-CSRF-Token")
		}
		if csrf != CtxCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		// 1. Stop container
		log.Printf("Destroying project %s: stopping container...", project.Name)
		stopContainer(project.Name)

		// 2. Drop database
		log.Printf("Destroying project %s: dropping database...", project.Name)
		if err := dropDatabase(project.Name); err != nil {
			log.Printf("dropDatabase error for %s: %v", project.Name, err)
		}

		// 3. Delete Gitea repo
		log.Printf("Destroying project %s: deleting Gitea repo...", project.Name)
		user := CtxUser(r)
		owner := user.Username
		repoName := project.Name
		if project.GiteaRepo != "" {
			parts := strings.SplitN(project.GiteaRepo, "/", 2)
			if len(parts) == 2 {
				owner = parts[0]
				repoName = parts[1]
			}
		}
		if err := GiteaDeleteRepo(user.GiteaToken, owner, repoName); err != nil {
			log.Printf("GiteaDeleteRepo error for %s: %v", project.Name, err)
		}

		// 4. Delete env vars, builds, and project from DB
		log.Printf("Destroying project %s: cleaning up database records...", project.Name)
		DB.Exec(`DELETE FROM project_env_vars WHERE project_id = $1`, project.ID)
		DB.Exec(`DELETE FROM builds WHERE project_id = $1`, project.ID)
		if err := DeleteProject(project.ID); err != nil {
			log.Printf("DeleteProject error for %s: %v", project.Name, err)
			http.Error(w, "failed to delete project", http.StatusInternalServerError)
			return
		}

		log.Printf("Project %s destroyed successfully", project.Name)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "unknown action", http.StatusBadRequest)
}

// replaceHyphens replaces hyphens with underscores for PostgreSQL identifiers.
func replaceHyphens(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// openRawDB opens a raw database connection to kumbula_system.
func openRawDB() (*sql.DB, error) {
	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)
	return sql.Open("postgres", connStr)
}

// dropDatabase drops the app database and role for the given project.
func dropDatabase(appName string) error {
	dbName := fmt.Sprintf("app_%s", replaceHyphens(appName))
	dbUser := dbName

	db, err := openRawDB()
	if err != nil {
		return fmt.Errorf("connect to kumbula_system: %w", err)
	}
	defer db.Close()

	// Drop the database first (must disconnect all sessions)
	if _, err := db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
		log.Printf("dropDatabase: DROP DATABASE %s error: %v", dbName, err)
	}

	// Then drop the role
	if _, err := db.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", dbUser)); err != nil {
		log.Printf("dropDatabase: DROP ROLE %s error: %v", dbUser, err)
	}

	return nil
}
