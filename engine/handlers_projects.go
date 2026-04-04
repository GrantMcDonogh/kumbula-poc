package main

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

// getHostIP returns the gateway IP of the kumbula Docker network,
// which is reachable from containers on that network (e.g. Gitea).
func getHostIP() string {
	out, err := exec.Command("docker", "network", "inspect", "kumbula",
		"-f", "{{range .IPAM.Config}}{{.Gateway}}{{end}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// handleNewProjectPage renders the "Create a new project" form.
func handleNewProjectPage(w http.ResponseWriter, r *http.Request) {
	RenderPage(w, r, "", map[string]interface{}{
		"Content": "new_project",
	})
}

// handleCreateProject validates the form, creates a Gitea repo + webhook,
// inserts a DB record, and redirects to the new project page.
func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	// Validate CSRF
	expected := CtxCSRF(r)
	token := r.FormValue("_csrf")
	if expected == "" || token != expected {
		http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
		return
	}

	name := r.FormValue("name")

	// Validate project name
	if err := ValidateProjectName(name); err != nil {
		RenderPage(w, r, "", map[string]interface{}{
			"Content": "new_project",
			"Error":   err.Error(),
		})
		return
	}

	// Check uniqueness
	if _, err := GetProjectByName(name); err == nil {
		RenderPage(w, r, "", map[string]interface{}{
			"Content": "new_project",
			"Error":   fmt.Sprintf("A project named %q already exists.", name),
		})
		return
	}

	// Reload user to get fresh Gitea token
	user := CtxUser(r)
	freshUser, err := GetUserByID(user.ID)
	if err != nil {
		log.Printf("projects: failed to reload user %d: %v", user.ID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if freshUser.GiteaToken == "" {
		RenderPage(w, r, "", map[string]interface{}{
			"Content": "new_project",
			"Error":   "Your account is missing a Gitea token. Please contact support.",
		})
		return
	}

	// Create Gitea repo
	cloneURL, err := GiteaCreateRepo(freshUser.GiteaToken, name)
	if err != nil {
		log.Printf("projects: failed to create Gitea repo %s for user %s: %v", name, freshUser.Username, err)
		RenderPage(w, r, "", map[string]interface{}{
			"Content": "new_project",
			"Error":   "Failed to create repository. Please try again.",
		})
		return
	}

	// Add webhook (non-fatal if fails)
	hostIP := getHostIP()
	if hostIP != "" {
		webhookURL := fmt.Sprintf("http://%s:%s/webhook", hostIP, ENGINE_PORT)
		if err := GiteaAddWebhook(freshUser.GiteaToken, freshUser.Username, name, webhookURL); err != nil {
			log.Printf("projects: failed to add webhook for %s/%s: %v (non-fatal)", freshUser.Username, name, err)
		}
	} else {
		log.Printf("projects: could not determine host IP for webhook (non-fatal)")
	}

	// Create project record
	appURL := fmt.Sprintf("http://%s.%s", name, DEPLOY_DOMAIN)
	_, err = CreateProject(freshUser.ID, name, cloneURL, appURL)
	if err != nil {
		log.Printf("projects: failed to create project record %s: %v", name, err)
		RenderPage(w, r, "", map[string]interface{}{
			"Content": "new_project",
			"Error":   "Failed to save project. Please try again.",
		})
		return
	}

	http.Redirect(w, r, "/projects/"+name, http.StatusSeeOther)
}
