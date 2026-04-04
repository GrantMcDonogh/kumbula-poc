package main

import (
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	pageTemplates map[string]*template.Template
	templateDir   string
)

var funcMap = template.FuncMap{
	"upper": strings.ToUpper,
	"statusClass": func(status string) string {
		switch strings.ToLower(status) {
		case "running":
			return "status-running"
		case "building":
			return "status-building"
		case "failed":
			return "status-failed"
		default:
			return "status-default"
		}
	},
}

func initTemplates() {
	_, thisFile, _, _ := runtime.Caller(0)
	templateDir = filepath.Join(filepath.Dir(thisFile), "templates")

	layoutPath := filepath.Join(templateDir, "layout.html")

	contentPages := map[string]string{
		"login":       "auth/login.html",
		"signup":      "auth/signup.html",
		"dashboard":   "dashboard/index.html",
		"new_project": "projects/new.html",
		"detail":      "projects/detail.html",
	}

	// Collect any partials
	partialGlob := filepath.Join(templateDir, "partials", "*.html")
	partials, _ := filepath.Glob(partialGlob)

	pageTemplates = make(map[string]*template.Template, len(contentPages))

	for name, relPath := range contentPages {
		contentPath := filepath.Join(templateDir, relPath)
		if _, err := os.Stat(contentPath); os.IsNotExist(err) {
			log.Printf("template: skipping %q (file not found: %s)", name, relPath)
			continue
		}

		files := []string{layoutPath, contentPath}
		files = append(files, partials...)

		t, err := template.New("").Funcs(funcMap).ParseFiles(files...)
		if err != nil {
			log.Fatalf("template: failed to parse %q: %v", name, err)
		}
		pageTemplates[name] = t
	}

	log.Printf("template: loaded %d page templates", len(pageTemplates))
}

// RenderPage renders a full page using the layout. data["Content"] selects the content template.
func RenderPage(w http.ResponseWriter, r *http.Request, _ string, data map[string]interface{}) {
	// Inject user and CSRF from context if not already set
	if _, ok := data["User"]; !ok {
		data["User"] = CtxUser(r)
	}
	if _, ok := data["CSRF"]; !ok {
		data["CSRF"] = CtxCSRF(r)
	}

	contentName, _ := data["Content"].(string)
	t, ok := pageTemplates[contentName]
	if !ok {
		http.Error(w, "template not found: "+contentName, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template: render error for %q: %v", contentName, err)
	}
}

// RenderPartial renders a standalone partial template by name.
func RenderPartial(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if _, ok := data["User"]; !ok {
		data["User"] = CtxUser(r)
	}
	if _, ok := data["CSRF"]; !ok {
		data["CSRF"] = CtxCSRF(r)
	}

	partialPath := filepath.Join(templateDir, "partials", name+".html")
	t, err := template.New("").Funcs(funcMap).ParseFiles(partialPath)
	if err != nil {
		http.Error(w, "partial not found: "+name, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template: partial render error for %q: %v", name, err)
	}
}
