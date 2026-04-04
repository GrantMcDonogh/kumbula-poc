package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func handleRedeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := CtxProject(r)
	if project == nil {
		http.Error(w, "Project not found", http.StatusNotFound)
		return
	}

	// Validate CSRF
	if r.FormValue("_csrf") != CtxCSRF(r) {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}

	// Load user for Gitea credentials
	user, err := GetUserByID(project.UserID)
	if err != nil {
		http.Error(w, "Failed to load user", http.StatusInternalServerError)
		return
	}

	// Build clone URL with credentials via Gitea container IP
	giteaIP := getContainerIP("gitea")
	cloneURL := fmt.Sprintf("http://%s:%s@%s:3000/%s.git",
		user.Username, user.GiteaPassword, giteaIP, project.GiteaRepo)

	// Create build record
	build, err := CreateBuild(project.ID, "manual")
	if err != nil {
		log.Printf("Failed to create build: %v", err)
		http.Error(w, "Failed to create build", http.StatusInternalServerError)
		return
	}

	// Update project status
	UpdateProjectStatus(project.ID, "building", "")

	// Launch deploy in background
	go deployWithBuild(project, build, cloneURL)

	// Redirect to builds tab
	http.Redirect(w, r, fmt.Sprintf("/projects/%s?tab=builds", project.Name), http.StatusSeeOther)
}

func handleBuildStream(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)
	if project == nil {
		http.Error(w, "Project not found", http.StatusNotFound)
		return
	}

	// Extract build ID from URL: /projects/{name}/builds/{id}/stream
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid build stream URL", http.StatusBadRequest)
		return
	}
	buildID, err := strconv.Atoi(parts[3])
	if err != nil {
		http.Error(w, "Invalid build ID", http.StatusBadRequest)
		return
	}

	// Verify build belongs to project
	build, err := GetBuild(buildID)
	if err != nil {
		http.Error(w, "Build not found", http.StatusNotFound)
		return
	}
	if build.ProjectID != project.ID {
		http.Error(w, "Build does not belong to project", http.StatusForbidden)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// If build already finished, send full log and return
	if build.Status == "success" || build.Status == "failed" {
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", build.Log)
		flusher.Flush()
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", build.Status)
		flusher.Flush()
		return
	}

	// Subscribe to live stream
	ch := broadcaster.Subscribe(buildID)
	defer broadcaster.Unsubscribe(buildID, ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if line == "__BUILD_DONE__" {
				fmt.Fprintf(w, "event: done\ndata: done\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", line)
			flusher.Flush()
		}
	}
}
