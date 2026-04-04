package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
)

// GiteaWebhook is the payload Gitea sends on push
type GiteaWebhook struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Pusher struct {
		Login string `json:"login"`
	} `json:"pusher"`
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

	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/apps", handleListApps)
	http.HandleFunc("/health", handleHealth)

	port := "9000"
	log.Printf("KumbulaCloud Engine starting on :%s", port)
	log.Printf("   Domain: *.%s", DEPLOY_DOMAIN)
	log.Fatal(http.ListenAndServe(":"+port, nil))
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

	// Respond immediately, deploy in background
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deploying",
		"app":    appName,
		"url":    fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
	})

	go deploy(appName, cloneURL)
}
