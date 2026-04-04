package main

import (
	"log"
	"net/http"
)

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	projects, err := GetProjectsByUser(user.ID)
	if err != nil {
		log.Printf("GetProjectsByUser error: %v", err)
		projects = nil
	}

	runningCount := 0
	failedCount := 0
	for _, p := range projects {
		switch p.Status {
		case "running":
			runningCount++
		case "failed":
			failedCount++
		}
	}

	RenderShell(w, r, "dashboard", map[string]interface{}{
		"Title":          "Dashboard",
		"Projects":       projects,
		"SidebarProjects": projects,
		"RunningCount":   runningCount,
		"FailedCount":    failedCount,
	})
}

func handleSidebarProjectsPartial(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	projects, err := GetProjectsByUser(user.ID)
	if err != nil {
		log.Printf("GetProjectsByUser error: %v", err)
		projects = nil
	}
	activeProject := r.URL.Query().Get("active")
	RenderPartial(w, r, "sidebar_projects", map[string]interface{}{
		"SidebarProjects": projects,
		"ActiveProject":   activeProject,
	})
}
