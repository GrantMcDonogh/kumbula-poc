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
	RenderPage(w, r, "layout", map[string]interface{}{
		"Title":    "Dashboard",
		"Content":  "dashboard",
		"Projects": projects,
	})
}

func handleProjectCardsPartial(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	projects, err := GetProjectsByUser(user.ID)
	if err != nil {
		log.Printf("GetProjectsByUser error: %v", err)
		projects = nil
	}
	RenderPartial(w, r, "project_cards", map[string]interface{}{
		"Projects": projects,
	})
}
