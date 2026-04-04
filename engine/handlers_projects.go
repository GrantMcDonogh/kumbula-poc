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
	RenderShell(w, r, "new_project", map[string]interface{}{
		"Title": "New Project",
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
		RenderShell(w, r, "new_project", map[string]interface{}{
			"Title": "New Project",
			"Error":   err.Error(),
		})
		return
	}

	// Check uniqueness
	if _, err := GetProjectByName(name); err == nil {
		RenderShell(w, r, "new_project", map[string]interface{}{
			"Title": "New Project",
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
		RenderShell(w, r, "new_project", map[string]interface{}{
			"Title": "New Project",
			"Error":   "Your account is missing a Gitea token. Please contact support.",
		})
		return
	}

	// Create Gitea repo
	_, err = GiteaCreateRepo(freshUser.GiteaToken, name)
	if err != nil {
		log.Printf("projects: failed to create Gitea repo %s for user %s: %v", name, freshUser.Username, err)
		RenderShell(w, r, "new_project", map[string]interface{}{
			"Title": "New Project",
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
	giteaRepo := fmt.Sprintf("%s/%s", freshUser.Username, name)
	_, err = CreateProject(freshUser.ID, name, giteaRepo, appURL)
	if err != nil {
		log.Printf("projects: failed to create project record %s: %v", name, err)
		RenderShell(w, r, "new_project", map[string]interface{}{
			"Title": "New Project",
			"Error":   "Failed to save project. Please try again.",
		})
		return
	}

	http.Redirect(w, r, "/projects/"+name, http.StatusSeeOther)
}

// handleGitHubImport clones a GitHub repo and pushes it to Gitea, triggering a deploy.
func handleGitHubImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// CSRF check
	csrf := r.FormValue("_csrf")
	if csrf == "" {
		csrf = r.Header.Get("X-CSRF-Token")
	}
	if csrf != CtxCSRF(r) {
		http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
		return
	}

	project := CtxProject(r)
	user := CtxUser(r)

	githubURL := strings.TrimSpace(r.FormValue("github_url"))

	// Validate URL
	if !strings.HasPrefix(githubURL, "https://github.com/") {
		RenderShell(w, r, "detail", importErrorData(r, project, "Invalid URL. Must start with https://github.com/"))
		return
	}

	// Save github_url on the project
	if err := UpdateProjectGithubURL(project.ID, githubURL); err != nil {
		log.Printf("import: failed to save github_url for %s: %v", project.Name, err)
		RenderShell(w, r, "detail", importErrorData(r, project, "Failed to save import URL. Please try again."))
		return
	}

	// Load fresh user for tokens
	freshUser, err := GetUserByID(user.ID)
	if err != nil {
		log.Printf("import: failed to reload user %d: %v", user.ID, err)
		UpdateProjectGithubURL(project.ID, "")
		RenderShell(w, r, "detail", importErrorData(r, project, "Internal error. Please try again."))
		return
	}

	// Build Gitea remote URL
	giteaRemoteURL := fmt.Sprintf("http://%s:3000/%s.git", getContainerIP("gitea"), project.GiteaRepo)

	// Clone and push
	log.Printf("import: importing %s -> %s for user %s", githubURL, project.Name, freshUser.Username)
	if err := GitHubCloneAndPush(githubURL, freshUser.GithubToken, giteaRemoteURL, freshUser.Username, freshUser.GiteaPassword); err != nil {
		log.Printf("import: GitHubCloneAndPush failed for %s: %v", project.Name, err)
		UpdateProjectGithubURL(project.ID, "")
		RenderShell(w, r, "detail", importErrorData(r, project, "Could not import repository. Check the URL and ensure your GitHub token is set for private repos."))
		return
	}

	log.Printf("import: %s imported successfully from %s", project.Name, githubURL)
	http.Redirect(w, r, "/projects/"+project.Name, http.StatusSeeOther)
}

// importErrorData builds the template data for rendering the detail page with an import error.
func importErrorData(r *http.Request, project *Project, errMsg string) map[string]interface{} {
	builds, _ := GetBuildsByProject(project.ID)
	envVars, _ := GetEnvVarsByProject(project.ID)
	data := map[string]interface{}{
		"Title":         project.Name,
		"Project":       project,
		"Tab":           "overview",
		"ActiveProject": project.Name,
		"ImportError":   errMsg,
		"Builds":        builds,
		"BuildCount":    len(builds),
		"EnvVarCount":   len(envVars),
	}
	if len(envVars) > 3 {
		data["EnvVarsPreview"] = envVars[:3]
	} else {
		data["EnvVarsPreview"] = envVars
	}
	if len(builds) > 0 {
		data["LastBuild"] = builds[0]
	}
	return data
}
