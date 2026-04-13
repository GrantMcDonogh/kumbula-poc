package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	DEPLOY_DOMAIN      = "kumbula.local"
	NETWORK_NAME       = "kumbula"
	POSTGRES_HOST      = "localhost"
	POSTGRES_CONTAINER = "kumbula-postgres"
	POSTGRES_USER      = "kumbula_admin"
	POSTGRES_PASS      = "kumbula_secret_2024"
	CLONE_BASE         = "/tmp/kumbula-builds"
	GITEA_DOMAIN       = "gitea.kumbula.local"
	ENGINE_PORT        = "9000"
)

// GiteaWebhook is the payload Gitea sends on push
type GiteaWebhook struct {
	Ref   string `json:"ref"`
	After string `json:"after"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Pusher struct {
		Login string `json:"login"`
	} `json:"pusher"`
	HeadCommit struct {
		ID string `json:"id"`
	} `json:"head_commit"`
}

// DeployResult tracks a deployment
type DeployResult struct {
	AppName     string `json:"app_name"`
	URL         string `json:"url"`
	ContainerID string `json:"container_id"`
	DatabaseURL string `json:"database_url"`
	Status      string `json:"status"`
	DeployedAt  string `json:"deployed_at"`
}

var deployments = make(map[string]*DeployResult)

func main() {
	os.MkdirAll(CLONE_BASE, 0755)

	if err := OpenDB(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := RunMigrations(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	InitGitea()
	initTemplates()

	mux := http.NewServeMux()

	// --- Static files ---
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// --- Public routes ---
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/apps", handleListApps)
	mux.HandleFunc("/webhook", handleWebhook)

	// --- Auth routes ---
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleLoginPage(w, r)
		case http.MethodPost:
			handleLogin(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/signup", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleSignupPage(w, r)
		case http.MethodPost:
			handleSignup(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/logout", handleLogout)
	mux.Handle("/settings", RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleUserSettings(w, r)
		case http.MethodPost:
			handleUserSettingsSave(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))

	// --- Authenticated routes ---
	mux.Handle("/", RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleDashboard(w, r)
	})))
	mux.Handle("/projects/", RequireAuth(RequireProjectOwner(http.HandlerFunc(routeProject))))
	mux.Handle("/partials/project-cards", RequireAuth(http.HandlerFunc(handleSidebarProjectsPartial)))
	mux.Handle("/partials/sidebar-projects", RequireAuth(http.HandlerFunc(handleSidebarProjectsPartial)))

	// Wrap the entire mux with session middleware
	handler := SessionMiddleware(mux)

	log.Printf("KumbulaCloud Engine starting on :%s", ENGINE_PORT)
	log.Printf("   Domain: *.%s", DEPLOY_DOMAIN)
	log.Fatal(http.ListenAndServe(":"+ENGINE_PORT, handler))
}

// routeProject dispatches /projects/* sub-routes.
func routeProject(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// parts[0] = "projects", parts[1] = name or "new", parts[2+] = action

	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	name := parts[1]

	// /projects/new
	if name == "new" {
		switch r.Method {
		case http.MethodGet:
			handleNewProjectPage(w, r)
		case http.MethodPost:
			handleCreateProject(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /projects/{name}
	if len(parts) == 2 {
		handleProjectDetail(w, r)
		return
	}

	// /projects/{name}/{action}
	action := parts[2]
	switch action {
	case "redeploy":
		handleRedeploy(w, r)
	case "env":
		handleEnvVars(w, r)
	case "settings":
		handleProjectSettings(w, r)
	case "builds":
		// /projects/{name}/builds/{id}/stream
		if len(parts) >= 5 && parts[4] == "stream" {
			handleBuildStream(w, r)
			return
		}
		http.NotFound(w, r)
	case "import":
		handleGitHubImport(w, r)
	default:
		http.NotFound(w, r)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"engine": "kumbula-cloud-poc",
	})
}

func handleListApps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deployments)
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var payload GiteaWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	appName := sanitizeName(payload.Repository.Name)
	cloneURL := rewriteCloneURL(payload.Repository.CloneURL)

	log.Printf("Deploy triggered: %s (by %s)", appName, payload.Pusher.Login)

	// Look up the project in the database
	project, err := GetProjectByName(appName)
	if err != nil {
		// Project not found in DB — fall back to legacy in-memory deploy
		log.Printf("  [%s] No DB project found, using legacy deploy", appName)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "deploying",
			"app":    appName,
			"url":    fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
		})
		go deploy(appName, cloneURL)
		return
	}

	// Extract commit SHA from payload (prefer head_commit.id, fall back to after)
	commitSHA := payload.HeadCommit.ID
	if commitSHA == "" {
		commitSHA = payload.After
	}
	if commitSHA == "" {
		commitSHA = "unknown"
	}

	// Create a build record
	build, err := CreateBuild(project.ID, commitSHA)
	if err != nil {
		log.Printf("  [%s] Failed to create build: %v", appName, err)
		http.Error(w, "Failed to create build", http.StatusInternalServerError)
		return
	}

	// Update project status to building
	UpdateProjectStatus(project.ID, "building", "")

	// Respond with 202 and build info
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "building",
		"app":      appName,
		"build_id": fmt.Sprintf("%d", build.ID),
		"url":      fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
	})

	go deployWithBuild(project, build, cloneURL)
}
