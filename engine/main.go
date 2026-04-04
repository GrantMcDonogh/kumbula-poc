package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	_ "github.com/lib/pq"
)

const (
	DEPLOY_DOMAIN = "kumbula.local"
	NETWORK_NAME  = "kumbula"
	POSTGRES_HOST         = "localhost"          // for engine (runs on host) to provision DBs
	POSTGRES_CONTAINER    = "kumbula-postgres"   // for app containers to connect
	POSTGRES_USER = "kumbula_admin"
	POSTGRES_PASS = "kumbula_secret_2024"
	CLONE_BASE    = "/tmp/kumbula-builds"
	GITEA_DOMAIN = "gitea.kumbula.local"
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

	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/apps", handleListApps)
	http.HandleFunc("/health", handleHealth)

	port := "9000"
	log.Printf("🚀 KumbulaCloud Engine starting on :%s", port)
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

	log.Printf("📦 Deploy triggered: %s (by %s)", appName, payload.Pusher.Login)

	// Respond immediately, deploy in background
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deploying",
		"app":    appName,
		"url":    fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
	})

	go deploy(appName, cloneURL)
}

func deploy(appName, cloneURL string) {
	start := time.Now()
	result := &DeployResult{
		AppName: appName,
		URL:     fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
		Status:  "building",
	}
	deployments[appName] = result

	// Step 1: Clone
	log.Printf("  [%s] Cloning %s...", appName, cloneURL)
	cloneDir := filepath.Join(CLONE_BASE, appName)
	os.RemoveAll(cloneDir)

	cmd := exec.Command("git", "clone", "--depth=1", cloneURL, cloneDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("  [%s] ❌ Clone failed: %s\n%s", appName, err, string(out))
		result.Status = "clone_failed"
		return
	}

	// Step 2: Detect language and generate Dockerfile if needed
	log.Printf("  [%s] Detecting language...", appName)
	if err := ensureDockerfile(appName, cloneDir); err != nil {
		log.Printf("  [%s] ❌ Dockerfile generation failed: %s", appName, err)
		result.Status = "detect_failed"
		return
	}

	// Step 3: Provision database
	log.Printf("  [%s] Provisioning database...", appName)
	dbURL, err := provisionDatabase(appName)
	if err != nil {
		log.Printf("  [%s] ⚠️  Database provisioning failed (continuing): %s", appName, err)
		dbURL = ""
	} else {
		result.DatabaseURL = dbURL
		log.Printf("  [%s] ✅ Database ready", appName)
	}

	// Step 4: Build Docker image
	log.Printf("  [%s] Building image...", appName)
	imageName := fmt.Sprintf("kumbula/%s:latest", appName)
	if err := buildImage(appName, cloneDir, imageName); err != nil {
		log.Printf("  [%s] ❌ Build failed: %s", appName, err)
		result.Status = "build_failed"
		return
	}

	// Step 5: Stop old container if exists
	stopContainer(appName)

	// Step 6: Run new container with Traefik labels
	log.Printf("  [%s] Starting container...", appName)
	containerID, err := runContainer(appName, imageName, dbURL)
	if err != nil {
		log.Printf("  [%s] ❌ Container start failed: %s", appName, err)
		result.Status = "run_failed"
		return
	}

	elapsed := time.Since(start)
	result.ContainerID = containerID[:12]
	result.Status = "running"
	result.DeployedAt = time.Now().Format(time.RFC3339)

	log.Printf("  [%s] ✅ Deployed in %s → %s", appName, elapsed.Round(time.Millisecond), result.URL)
}

// ensureDockerfile detects the language and creates a Dockerfile if none exists
func ensureDockerfile(appName, dir string) error {
	dockerfilePath := filepath.Join(dir, "Dockerfile")

	// If Dockerfile exists, use it
	if _, err := os.Stat(dockerfilePath); err == nil {
		log.Printf("  [%s] Found existing Dockerfile", appName)
		return nil
	}

	// Detect language
	lang := detectLanguage(dir)
	log.Printf("  [%s] Detected: %s", appName, lang)

	var dockerfile string
	switch lang {
	case "node":
		dockerfile = `FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
RUN npm ci --production 2>/dev/null || npm install --production
COPY . .
ENV PORT=3000
EXPOSE 3000
CMD ["node", "index.js"]
`
	case "python":
		dockerfile = `FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt* ./
RUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true
COPY . .
ENV PORT=3000
EXPOSE 3000
CMD ["python", "app.py"]
`
	case "go":
		dockerfile = `FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o server .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/server .
ENV PORT=3000
EXPOSE 3000
CMD ["./server"]
`
	default:
		dockerfile = `FROM nginx:alpine
COPY . /usr/share/nginx/html
EXPOSE 80
`
	}

	return os.WriteFile(dockerfilePath, []byte(dockerfile), 0644)
}

func detectLanguage(dir string) string {
	checks := map[string]string{
		"package.json":    "node",
		"requirements.txt": "python",
		"app.py":          "python",
		"main.py":         "python",
		"go.mod":          "go",
		"main.go":         "go",
		"index.html":      "static",
	}

	for file, lang := range checks {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			return lang
		}
	}
	return "static"
}

func provisionDatabase(appName string) (string, error) {
	dbName := fmt.Sprintf("app_%s", strings.ReplaceAll(appName, "-", "_"))
	dbUser := dbName
	dbPass := generatePassword(16)

	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return "", fmt.Errorf("connect failed: %w", err)
	}
	defer db.Close()

	// Create or update role (idempotent — always reset password so redeploys work)
	db.Exec(fmt.Sprintf(`DO $$ BEGIN
		IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s') THEN
			CREATE ROLE %s WITH LOGIN PASSWORD '%s';
		ELSE
			ALTER ROLE %s WITH PASSWORD '%s';
		END IF;
	END $$`, dbUser, dbUser, dbPass, dbUser, dbPass))

	// Create database if not exists
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT FROM pg_database WHERE datname = $1)", dbName).Scan(&exists)
	if !exists {
		db.Exec(fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbName, dbUser))
	}

	// Grant privileges
	db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s", dbName, dbUser))

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", dbUser, dbPass, POSTGRES_CONTAINER, dbName)
	return dbURL, nil
}

func buildImage(appName, contextDir, imageName string) error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer cli.Close()

	// Create tar of build context
	cmd := exec.Command("tar", "-cf", "-", "-C", contextDir, ".")
	tarReader, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	resp, err := cli.ImageBuild(ctx, tarReader, types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Stream build output
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var line map[string]interface{}
		json.Unmarshal(scanner.Bytes(), &line)
		if stream, ok := line["stream"].(string); ok {
			stream = strings.TrimSpace(stream)
			if stream != "" {
				log.Printf("  [%s] build: %s", appName, stream)
			}
		}
		if errMsg, ok := line["error"].(string); ok {
			return fmt.Errorf("build error: %s", errMsg)
		}
	}

	cmd.Wait()
	return nil
}

func stopContainer(appName string) {
	ctx := context.Background()
	cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	defer cli.Close()

	containerName := fmt.Sprintf("kumbula-app-%s", appName)
	timeout := 10
	cli.ContainerStop(ctx, containerName, container.StopOptions{Timeout: &timeout})
	cli.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})
}

func runContainer(appName, imageName, dbURL string) (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containerName := fmt.Sprintf("kumbula-app-%s", appName)
	hostname := fmt.Sprintf("%s.%s", appName, DEPLOY_DOMAIN)

	env := []string{
		"PORT=3000",
		fmt.Sprintf("APP_NAME=%s", appName),
		fmt.Sprintf("APP_URL=http://%s", hostname),
	}
	if dbURL != "" {
		env = append(env, fmt.Sprintf("DATABASE_URL=%s", dbURL))
	}

	labels := map[string]string{
		"traefik.enable": "true",
		fmt.Sprintf("traefik.http.routers.%s.rule", appName):                     fmt.Sprintf("Host(`%s`)", hostname),
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", appName): "3000",
		"kumbula.managed": "true",
		"kumbula.app":     appName,
	}

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:  imageName,
			Env:    env,
			Labels: labels,
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				NETWORK_NAME: {},
			},
		},
		nil,
		containerName,
	)
	if err != nil {
		return "", err
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}

	return resp.ID, nil
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

// rewriteCloneURL replaces gitea.kumbula.local with the Gitea container's IP
// so the engine (running on the host) can clone repos without DNS resolution.
func rewriteCloneURL(cloneURL string) string {
	giteaIP := getContainerIP("gitea")
	if giteaIP == "" {
		return cloneURL
	}
	return strings.Replace(cloneURL,
		fmt.Sprintf("http://%s", GITEA_DOMAIN),
		fmt.Sprintf("http://%s:3000", giteaIP),
		1)
}

func getContainerIP(containerName string) string {
	out, err := exec.Command("docker", "inspect", "-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		containerName).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func generatePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
