package main

import "net/http"

func handleDashboard(w http.ResponseWriter, r *http.Request)           {}
func handleProjectCardsPartial(w http.ResponseWriter, r *http.Request) {}
func handleNewProjectPage(w http.ResponseWriter, r *http.Request)      {}
func handleCreateProject(w http.ResponseWriter, r *http.Request)       {}
func handleProjectDetail(w http.ResponseWriter, r *http.Request)       {}
func handleRedeploy(w http.ResponseWriter, r *http.Request)            {}
func handleEnvVars(w http.ResponseWriter, r *http.Request)             {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)     {}
func handleBuildStream(w http.ResponseWriter, r *http.Request)         {}
