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
		"Title":         project.Name,
		"Project":       project,
		"Tab":           tab,
		"ActiveProject": project.Name,
	}
	switch tab {
	case "overview":
		builds, err := GetBuildsByProject(project.ID)
		if err != nil {
			log.Printf("GetBuildsByProject error: %v", err)
		}
		envVars, err := GetEnvVarsByProject(project.ID)
		if err != nil {
			log.Printf("GetEnvVarsByProject error: %v", err)
		}
		data["Builds"] = builds
		data["BuildCount"] = len(builds)
		data["EnvVarCount"] = len(envVars)
		// Preview first 3 env vars
		if len(envVars) > 3 {
			data["EnvVarsPreview"] = envVars[:3]
		} else {
			data["EnvVarsPreview"] = envVars
		}
		if len(builds) > 0 {
			data["LastBuild"] = builds[0]
		}
		freshUser, _ := GetUserByID(CtxUser(r).ID)
		if freshUser != nil {
			data["HasGithubToken"] = freshUser.GithubToken != ""
		}
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
	RenderShell(w, r, "detail", data)
}
