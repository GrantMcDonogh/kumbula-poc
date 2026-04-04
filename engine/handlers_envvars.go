package main

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

var reservedEnvKeys = map[string]bool{
	"DATABASE_URL": true,
	"PORT":         true,
	"APP_NAME":     true,
	"APP_URL":      true,
}

func handleEnvVars(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)

	// CSRF check: form value or header
	csrf := r.FormValue("_csrf")
	if csrf == "" {
		csrf = r.Header.Get("X-CSRF-Token")
	}
	if csrf != CtxCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}

	// Determine if this is a DELETE operation
	isDelete := r.Method == http.MethodDelete || r.URL.Query().Get("_method") == "DELETE"

	if isDelete {
		idStr := r.URL.Query().Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "invalid env var id", http.StatusBadRequest)
			return
		}
		if err := DeleteEnvVar(id); err != nil {
			log.Printf("DeleteEnvVar error: %v", err)
			http.Error(w, "failed to delete env var", http.StatusInternalServerError)
			return
		}
	} else if r.Method == http.MethodPost {
		key := strings.ToUpper(strings.TrimSpace(r.FormValue("key")))
		value := r.FormValue("value")

		if key == "" || value == "" {
			http.Error(w, "key and value are required", http.StatusBadRequest)
			return
		}

		if reservedEnvKeys[key] {
			http.Error(w, key+" is a reserved variable name", http.StatusBadRequest)
			return
		}

		if err := SetEnvVar(project.ID, key, value); err != nil {
			log.Printf("SetEnvVar error: %v", err)
			http.Error(w, "failed to set env var", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Reload and re-render partial
	vars, err := GetEnvVarsByProject(project.ID)
	if err != nil {
		log.Printf("GetEnvVarsByProject error: %v", err)
	}

	data := map[string]interface{}{
		"Project": project,
		"EnvVars": vars,
	}
	RenderPartial(w, r, "envvar_form", data)
}
