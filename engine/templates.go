package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	shellTemplates map[string]*template.Template
	authTemplates  map[string]*template.Template
	templateDir    string
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
	"initials": func(s string) string {
		if len(s) == 0 {
			return "?"
		}
		s = strings.ToUpper(s)
		if len(s) >= 2 {
			return s[:2]
		}
		return s[:1]
	},
	"timeAgo": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			m := int(d.Minutes())
			return fmt.Sprintf("%dm ago", m)
		case d < 24*time.Hour:
			h := int(d.Hours())
			return fmt.Sprintf("%dh ago", h)
		default:
			days := int(d.Hours() / 24)
			return fmt.Sprintf("%dd ago", days)
		}
	},
	"shortSHA": func(s string) string {
		if len(s) >= 7 {
			return s[:7]
		}
		return s
	},
	"buildDuration": func(b *Build) string {
		if b == nil || !b.StartedAt.Valid {
			return "—"
		}
		var end time.Time
		if b.FinishedAt.Valid {
			end = b.FinishedAt.Time
		} else {
			end = time.Now()
		}
		d := end.Sub(b.StartedAt.Time)
		if d < time.Minute {
			return fmt.Sprintf("%ds", int(d.Seconds()))
		}
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	},
}

func initTemplates() {
	_, thisFile, _, _ := runtime.Caller(0)
	templateDir = filepath.Join(filepath.Dir(thisFile), "templates")

	shellLayoutPath := filepath.Join(templateDir, "layout_shell.html")
	authLayoutPath := filepath.Join(templateDir, "layout_auth.html")

	// Shell pages (authenticated, with sidebar)
	shellPages := map[string]string{
		"dashboard":   "dashboard/index.html",
		"new_project": "projects/new.html",
		"detail":      "projects/detail.html",
		"settings":    "settings.html",
	}

	// Auth pages (no sidebar)
	authPages := map[string]string{
		"login":  "auth/login.html",
		"signup": "auth/signup.html",
	}

	// Collect partials
	partialGlob := filepath.Join(templateDir, "partials", "*.html")
	partials, _ := filepath.Glob(partialGlob)

	// Build shell templates
	shellTemplates = make(map[string]*template.Template, len(shellPages))
	for name, relPath := range shellPages {
		contentPath := filepath.Join(templateDir, relPath)
		if _, err := os.Stat(contentPath); os.IsNotExist(err) {
			log.Printf("template: skipping %q (file not found: %s)", name, relPath)
			continue
		}

		files := []string{shellLayoutPath, contentPath}
		files = append(files, partials...)

		t, err := template.New("").Funcs(funcMap).ParseFiles(files...)
		if err != nil {
			log.Fatalf("template: failed to parse shell/%q: %v", name, err)
		}
		shellTemplates[name] = t
	}

	// Build auth templates
	authTemplates = make(map[string]*template.Template, len(authPages))
	for name, relPath := range authPages {
		contentPath := filepath.Join(templateDir, relPath)
		if _, err := os.Stat(contentPath); os.IsNotExist(err) {
			log.Printf("template: skipping %q (file not found: %s)", name, relPath)
			continue
		}

		files := []string{authLayoutPath, contentPath}

		t, err := template.New("").Funcs(funcMap).ParseFiles(files...)
		if err != nil {
			log.Fatalf("template: failed to parse auth/%q: %v", name, err)
		}
		authTemplates[name] = t
	}

	log.Printf("template: loaded %d shell + %d auth templates", len(shellTemplates), len(authTemplates))
}

// RenderShell renders an authenticated page with the sidebar layout.
// It auto-injects User, CSRF, SidebarProjects, and ActiveProject.
func RenderShell(w http.ResponseWriter, r *http.Request, templateName string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}

	// Inject user and CSRF
	user := CtxUser(r)
	if _, ok := data["User"]; !ok {
		data["User"] = user
	}
	if _, ok := data["CSRF"]; !ok {
		data["CSRF"] = CtxCSRF(r)
	}

	// Load sidebar projects
	if _, ok := data["SidebarProjects"]; !ok && user != nil {
		projects, err := GetProjectsByUser(user.ID)
		if err != nil {
			log.Printf("RenderShell: failed to load sidebar projects: %v", err)
			projects = nil
		}
		data["SidebarProjects"] = projects
	}

	// Determine active project
	if _, ok := data["ActiveProject"]; !ok {
		if project := CtxProject(r); project != nil {
			data["ActiveProject"] = project.Name
		} else {
			data["ActiveProject"] = ""
		}
	}

	t, ok := shellTemplates[templateName]
	if !ok {
		http.Error(w, "template not found: "+templateName, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template: render error for %q: %v", templateName, err)
	}
}

// RenderAuth renders an unauthenticated page (login/signup) without sidebar.
func RenderAuth(w http.ResponseWriter, r *http.Request, templateName string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}

	if _, ok := data["CSRF"]; !ok {
		data["CSRF"] = CtxCSRF(r)
	}

	t, ok := authTemplates[templateName]
	if !ok {
		http.Error(w, "template not found: "+templateName, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template: render error for %q: %v", templateName, err)
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
