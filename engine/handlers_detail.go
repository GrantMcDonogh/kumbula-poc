package main

import (
	"log"
	"net/http"
)

func handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}
	data := map[string]interface{}{
		"Title":   project.Name,
		"Content": "detail",
		"Project": project,
		"Tab":     tab,
	}
	switch tab {
	case "builds":
		builds, err := GetBuildsByProject(project.ID)
		if err != nil {
			log.Printf("GetBuildsByProject error: %v", err)
		}
		data["Builds"] = builds
	case "env":
		vars, err := GetEnvVarsByProject(project.ID)
		if err != nil {
			log.Printf("GetEnvVarsByProject error: %v", err)
		}
		data["EnvVars"] = vars
	}
	RenderPage(w, r, "layout", data)
}
