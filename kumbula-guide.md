# KumbulaCloud — Proof of Concept Build Guide

## What you're building

A working mini-PaaS that runs on your spare machine. When you're done, you'll demo this to your co-founders:

1. **You type** `git push kumbula main`
2. **The system** detects the language, builds a Docker image, deploys it, provisions a database, and assigns a URL
3. **Your co-founder visits** `myapp.kumbula.local` and sees a running app — deployed entirely on local hardware

Total time: **one weekend** (8–12 hours).

---

## Architecture overview

```
Developer                    Your Machine (Ubuntu)
─────────                    ─────────────────────
                              ┌─────────────────────────────┐
git push ──────────────────▶  │  Gitea (git server)          │
                              │       │                      │
                              │       ▼ webhook              │
                              │  kumbula-engine (Go API)     │
                              │       │                      │
                              │       ├── docker build       │
                              │       ├── docker run         │
                              │       ├── provision Postgres │
                              │       └── configure Traefik  │
                              │                              │
browser ◀──── traefik ◀────── │  *.kumbula.local routing     │
                              └─────────────────────────────┘
```

**Components (all free, all local):**

| Component | Role | Why this one |
|-----------|------|-------------|
| **Gitea** | Self-hosted Git server | Lightweight, webhook support, 1-binary install |
| **kumbula-engine** | Your custom Go API | Receives webhooks, builds, deploys, provisions DBs |
| **Docker** | Container runtime | Industry standard, Buildpack-compatible |
| **Traefik** | Reverse proxy + TLS | Auto-discovers containers, dynamic routing |
| **PostgreSQL** | Managed database | Most-requested DB on any PaaS |
| **dnsmasq** | Local DNS wildcard | Routes *.kumbula.local to your machine |

---

## Phase 1: Prepare the machine (1 hour)

### 1.1 Install Ubuntu 24.04

If your spare machine doesn't have it, install Ubuntu 24.04 LTS Server (or Desktop). You can also use an existing Ubuntu/Debian install.

### 1.2 Install Docker

```bash
# Remove old versions
sudo apt remove docker docker-engine docker.io containerd runc 2>/dev/null

# Install Docker
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
newgrp docker

# Verify
docker run hello-world
```

### 1.3 Install Go 1.22+

```bash
wget https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.5.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

### 1.4 Install supporting tools

```bash
sudo apt update
sudo apt install -y git curl jq dnsmasq postgresql-client
```

### 1.5 Set up local DNS wildcard

This makes `*.kumbula.local` resolve to your machine.

> **Important:** On Ubuntu with systemd-resolved, dnsmasq and the stub resolver
> will conflict on port 53. You must configure dnsmasq to bind only to 127.0.0.1
> and then point DNS resolution directly at dnsmasq (bypassing systemd-resolved).

```bash
# Get your machine's IP (use the LAN IP, not 127.0.0.1)
MY_IP=$(hostname -I | awk '{print $1}')
echo "Your IP: $MY_IP"

# Configure dnsmasq to coexist with systemd-resolved
echo -e "listen-address=127.0.0.1\nbind-interfaces" | sudo tee /etc/dnsmasq.d/00-listen.conf

# Configure wildcard DNS — both A and AAAA records
# The AAAA record (::) prevents 5-second timeouts on IPv6 lookups
echo -e "address=/kumbula.local/$MY_IP\naddress=/kumbula.local/::" | sudo tee /etc/dnsmasq.d/kumbula.conf
sudo systemctl restart dnsmasq

# Point DNS resolution directly at dnsmasq (bypass systemd-resolved)
# This avoids the slow forwarding path through resolved
sudo rm -f /etc/resolv.conf
echo -e "nameserver 127.0.0.1\nnameserver $(ip route | grep default | awk '{print $3}')\nsearch localdomain" | sudo tee /etc/resolv.conf

# Verify
getent hosts test.kumbula.local
# Should print: 192.168.x.x  test.kumbula.local (instantly, not after 5 seconds)
```

> **Why not use systemd-resolved forwarding?** You can configure resolved to
> forward `.kumbula.local` queries to dnsmasq via
> `/etc/systemd/resolved.conf.d/kumbula.conf`, but in practice the forwarding
> path adds multi-second timeouts that cause curl and git to hang. The direct
> approach above is more reliable for a PoC.

---

## Phase 2: Deploy infrastructure with Docker Compose (1 hour)

Create the project directory:

```bash
mkdir -p ~/kumbula-poc && cd ~/kumbula-poc
```

### 2.1 Docker Compose stack

Create `docker-compose.yml`:

> **Note:** Use `traefik:latest` (not `traefik:v3.1`). Older Traefik images ship
> with a Docker client that defaults to API v1.24, which Docker 29+ rejects
> (minimum API v1.40). The latest Traefik has proper API version negotiation.

```yaml
networks:
  kumbula:
    name: kumbula
    driver: bridge

volumes:
  gitea_data:
  postgres_data:
  traefik_certs:

services:
  # ─── Reverse Proxy ───────────────────────────────────────
  traefik:
    image: traefik:latest
    container_name: traefik
    restart: unless-stopped
    command:
      - "--api.dashboard=true"
      - "--api.insecure=true"
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--providers.docker.network=kumbula"
      - "--entrypoints.web.address=:80"
      - "--log.level=INFO"
    ports:
      - "80:80"
      - "8080:8080"   # Traefik dashboard
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - kumbula
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.dashboard.rule=Host(`traefik.kumbula.local`)"
      - "traefik.http.routers.dashboard.service=api@internal"

  # ─── Git Server ──────────────────────────────────────────
  gitea:
    image: gitea/gitea:1.22
    container_name: gitea
    restart: unless-stopped
    environment:
      - GITEA__server__DOMAIN=gitea.kumbula.local
      - GITEA__server__ROOT_URL=http://gitea.kumbula.local/
      - GITEA__server__HTTP_PORT=3000
      - GITEA__database__DB_TYPE=sqlite3
      - GITEA__service__DISABLE_REGISTRATION=false
      - GITEA__webhook__ALLOWED_HOST_LIST=*
    volumes:
      - gitea_data:/data
    networks:
      - kumbula
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.gitea.rule=Host(`gitea.kumbula.local`)"
      - "traefik.http.services.gitea.loadbalancer.server.port=3000"

  # ─── Shared PostgreSQL ──────────────────────────────────
  postgres:
    image: postgres:16-alpine
    container_name: kumbula-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: kumbula_admin
      POSTGRES_PASSWORD: kumbula_secret_2024
      POSTGRES_DB: kumbula_system
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5432:5432"
    networks:
      - kumbula
```

### 2.2 Start the stack

```bash
cd ~/kumbula-poc
docker compose up -d
```

### 2.3 Configure Gitea

> **Important:** Gitea starts in install mode. The web installer can be unreliable
> via API. The most reliable approach is to set `INSTALL_LOCK=true` in the config
> and create the admin user via the CLI.

```bash
# Wait for Gitea to start
sleep 5

# Set INSTALL_LOCK so Gitea exits install mode
docker exec gitea sed -i 's/INSTALL_LOCK = false/INSTALL_LOCK = true/' /data/gitea/conf/app.ini
docker restart gitea
sleep 3

# Create admin user via CLI (must run as 'git' user inside container)
docker exec -u git gitea gitea admin user create \
  --admin --username kumbula --password kumbula123 \
  --email admin@kumbula.local --config /data/gitea/conf/app.ini

# Create an API token
GITEA_TOKEN=$(docker exec gitea curl -s -u "kumbula:kumbula123" \
  -X POST 'http://localhost:3000/api/v1/users/kumbula/tokens' \
  -H 'Content-Type: application/json' \
  -d '{"name": "kumbula-engine", "scopes": ["all"]}' | jq -r '.sha1')

echo "Your Gitea token: $GITEA_TOKEN"

# Store for later use
export GITEA_TOKEN
export GITEA_URL="http://gitea.kumbula.local"
```

---

## Phase 3: Build the engine (4–6 hours)

This is the core — a Go HTTP server that:
- Receives webhook from Gitea on `git push`
- Clones the repo
- Detects the language and creates a Dockerfile
- Builds a Docker image
- Creates a PostgreSQL database for the app
- Runs the container with the right env vars
- Registers it with Traefik via Docker labels

### 3.1 Initialize the Go project

```bash
mkdir -p ~/kumbula-poc/engine && cd ~/kumbula-poc/engine
go mod init kumbula-engine
```

> **Note on Docker SDK versions:** Use `github.com/docker/docker@v27.x` (not the
> latest). The latest Docker SDK has moved to `github.com/moby/moby` and has
> module path conflicts. With v27, `ImageBuildOptions` lives in
> `github.com/docker/docker/api/types` (not `api/types/image`).

```bash
go get github.com/docker/docker@v27.5.1+incompatible
go get github.com/lib/pq@latest
```

### 3.2 Main engine code

Create `main.go`:

> **Key design decisions from the first build:**
> - The engine runs on the **host** (not in Docker), so it connects to Postgres
>   via `localhost:5432`. But app containers connect via the container name
>   `kumbula-postgres`. The code uses separate constants for each.
> - Gitea's webhook sends clone URLs using `gitea.kumbula.local`, which the host
>   may not be able to resolve (depending on DNS setup). The engine rewrites clone
>   URLs to use Gitea's container IP via `docker inspect`.
> - Database provisioning must be **idempotent** — on redeploy, the password is
>   reset with `ALTER ROLE` so the new container gets a working credential.

```go
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
	DEPLOY_DOMAIN      = "kumbula.local"
	NETWORK_NAME       = "kumbula"
	POSTGRES_HOST      = "localhost"        // engine runs on host
	POSTGRES_CONTAINER = "kumbula-postgres" // app containers use this
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
		log.Printf("  [%s] Clone failed: %s\n%s", appName, err, string(out))
		result.Status = "clone_failed"
		return
	}

	// Step 2: Detect language and generate Dockerfile if needed
	log.Printf("  [%s] Detecting language...", appName)
	if err := ensureDockerfile(appName, cloneDir); err != nil {
		log.Printf("  [%s] Dockerfile generation failed: %s", appName, err)
		result.Status = "detect_failed"
		return
	}

	// Step 3: Provision database
	log.Printf("  [%s] Provisioning database...", appName)
	dbURL, err := provisionDatabase(appName)
	if err != nil {
		log.Printf("  [%s] Database provisioning failed (continuing): %s", appName, err)
		dbURL = ""
	} else {
		result.DatabaseURL = dbURL
		log.Printf("  [%s] Database ready", appName)
	}

	// Step 4: Build Docker image
	log.Printf("  [%s] Building image...", appName)
	imageName := fmt.Sprintf("kumbula/%s:latest", appName)
	if err := buildImage(appName, cloneDir, imageName); err != nil {
		log.Printf("  [%s] Build failed: %s", appName, err)
		result.Status = "build_failed"
		return
	}

	// Step 5: Stop old container if exists
	stopContainer(appName)

	// Step 6: Run new container with Traefik labels
	log.Printf("  [%s] Starting container...", appName)
	containerID, err := runContainer(appName, imageName, dbURL)
	if err != nil {
		log.Printf("  [%s] Container start failed: %s", appName, err)
		result.Status = "run_failed"
		return
	}

	elapsed := time.Since(start)
	result.ContainerID = containerID[:12]
	result.Status = "running"
	result.DeployedAt = time.Now().Format(time.RFC3339)

	log.Printf("  [%s] Deployed in %s -> %s", appName, elapsed.Round(time.Millisecond), result.URL)
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
		"package.json":     "node",
		"requirements.txt": "python",
		"app.py":           "python",
		"main.py":          "python",
		"go.mod":           "go",
		"main.go":          "go",
		"index.html":       "static",
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

	// App containers connect via the container name, not localhost
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
// so the engine (running on the host) can clone repos without relying on DNS.
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
```

### 3.3 Build and run the engine

```bash
cd ~/kumbula-poc/engine
go mod tidy
go build -o kumbula-engine .

# Run the engine (it needs access to Docker socket)
./kumbula-engine
```

You should see:
```
KumbulaCloud Engine starting on :9000
   Domain: *.kumbula.local
```

**Tip:** In production you'd run this as a systemd service or container. For the PoC, a terminal window is fine.

---

## Phase 4: Wire up the webhook (15 minutes)

### 4.1 Create a test repo on Gitea

```bash
# Create a repo via API
curl -X POST "$GITEA_URL/api/v1/user/repos" \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "hello-world", "auto_init": false, "private": false}'
```

### 4.2 Add webhook

```bash
# Point Gitea webhook at your engine
# Replace YOUR_IP with the machine's LAN IP
ENGINE_URL="http://YOUR_IP:9000/webhook"

curl -X POST "$GITEA_URL/api/v1/repos/kumbula/hello-world/hooks" \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"type\": \"gitea\",
    \"active\": true,
    \"events\": [\"push\"],
    \"config\": {
      \"url\": \"$ENGINE_URL\",
      \"content_type\": \"json\"
    }
  }"
```

---

## Phase 5: Create a sample app and deploy (15 minutes)

### 5.1 Node.js sample app

```bash
mkdir -p /tmp/hello-world && cd /tmp/hello-world
git init
git config user.email "admin@kumbula.local"
git config user.name "kumbula"
```

Create `package.json`:
```json
{
  "name": "hello-world",
  "version": "1.0.0",
  "main": "index.js",
  "scripts": { "start": "node index.js" },
  "dependencies": { "express": "^4.18.0", "pg": "^8.12.0" }
}
```

Create `index.js`:
```javascript
const express = require('express');
const { Pool } = require('pg');
const app = express();
const port = process.env.PORT || 3000;

// Connect to the provisioned database (if available)
let pool = null;
if (process.env.DATABASE_URL) {
  pool = new Pool({ connectionString: process.env.DATABASE_URL });
  pool.query(`
    CREATE TABLE IF NOT EXISTS visits (
      id SERIAL PRIMARY KEY,
      visited_at TIMESTAMP DEFAULT NOW(),
      user_agent TEXT
    )
  `).catch(console.error);
}

app.get('/', async (req, res) => {
  let visitCount = 'N/A';
  let dbStatus = 'Not connected';

  if (pool) {
    try {
      await pool.query(
        'INSERT INTO visits (user_agent) VALUES ($1)',
        [req.headers['user-agent']]
      );
      const result = await pool.query('SELECT COUNT(*) FROM visits');
      visitCount = result.rows[0].count;
      dbStatus = 'Connected';
    } catch (err) {
      dbStatus = err.message;
    }
  }

  res.send(`
    <!DOCTYPE html>
    <html>
    <head>
      <title>${process.env.APP_NAME || 'My App'} - KumbulaCloud</title>
      <style>
        body { font-family: system-ui; max-width: 600px; margin: 80px auto;
               padding: 0 20px; background: #0B1D28; color: #E2E8F0; }
        h1 { color: #14C4A0; }
        .card { background: #0F2B3C; border-radius: 12px; padding: 24px;
                margin: 16px 0; border-left: 4px solid #0A8F7F; }
        .stat { font-size: 2em; color: #14C4A0; font-weight: bold; }
        code { background: #1a3a4a; padding: 2px 8px; border-radius: 4px; }
        .footer { margin-top: 40px; color: #6B7B8D; font-size: 0.9em; }
      </style>
    </head>
    <body>
      <h1>${process.env.APP_NAME || 'My App'}</h1>
      <p>Deployed on <strong>KumbulaCloud</strong> - South Africa's sovereign PaaS</p>

      <div class="card">
        <div>Database: ${dbStatus}</div>
        <div>Visit count: <span class="stat">${visitCount}</span></div>
      </div>

      <div class="card">
        <div>Container: <code>${require('os').hostname()}</code></div>
        <div>Node.js: <code>${process.version}</code></div>
        <div>App URL: <code>${process.env.APP_URL || 'unknown'}</code></div>
        <div>Region: <code>ZA-JHB (local)</code></div>
      </div>

      <div class="footer">
        <p>Data residency: South Africa &middot; Latency: &lt;2ms &middot; Zero hyperscaler dependency</p>
      </div>
    </body>
    </html>
  `);
});

app.get('/health', (req, res) => res.json({ status: 'ok' }));

app.listen(port, () => {
  console.log(`App listening on port ${port}`);
});
```

### 5.2 Push to deploy

```bash
cd /tmp/hello-world
git add .
git commit -m "Initial commit"
git remote add kumbula http://gitea.kumbula.local/kumbula/hello-world.git
git push kumbula main
```

**Watch your engine terminal** — you should see:
```
Deploy triggered: hello-world (by kumbula)
  [hello-world] Cloning ...
  [hello-world] Detected: node
  [hello-world] Database ready
  [hello-world] build: Step 1/7 : FROM node:20-alpine
  ...
  [hello-world] Deployed in 23.4s -> http://hello-world.kumbula.local
```

### 5.3 Visit the app

Open `http://hello-world.kumbula.local` in your browser. You should see the app running with a live database connection and visit counter.

---

## Phase 6: Build a simple CLI (optional, 1 hour)

A CLI makes the demo more impressive. Create `~/kumbula-poc/cli/kc`:

```bash
#!/usr/bin/env bash
# kc — KumbulaCloud CLI
set -euo pipefail

GITEA_URL="${GITEA_URL:-http://gitea.kumbula.local}"
ENGINE_URL="${ENGINE_URL:-http://localhost:9000}"
DOMAIN="kumbula.local"

case "${1:-help}" in

  create)
    APP_NAME="${2:?Usage: kc create <app-name>}"
    echo "Creating $APP_NAME on KumbulaCloud..."
    curl -s -X POST "$GITEA_URL/api/v1/user/repos" \
      -H "Authorization: token $GITEA_TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"name\": \"$APP_NAME\", \"auto_init\": true, \"private\": false}" | jq .clone_url

    # Add webhook
    curl -s -X POST "$GITEA_URL/api/v1/repos/kumbula/$APP_NAME/hooks" \
      -H "Authorization: token $GITEA_TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"type\":\"gitea\",\"active\":true,\"events\":[\"push\"],\"config\":{\"url\":\"$ENGINE_URL/webhook\",\"content_type\":\"json\"}}" > /dev/null

    echo "App created. Deploy with:"
    echo "   git remote add kumbula $GITEA_URL/kumbula/$APP_NAME.git"
    echo "   git push kumbula main"
    echo ""
    echo "URL: http://$APP_NAME.$DOMAIN"
    ;;

  apps)
    echo "Deployed apps:"
    curl -s "$ENGINE_URL/apps" | jq -r 'to_entries[] | "  \(.value.status) \(.value.app_name)\t-> \(.value.url)"'
    ;;

  logs)
    APP_NAME="${2:?Usage: kc logs <app-name>}"
    docker logs -f "kumbula-app-$APP_NAME" 2>&1
    ;;

  destroy)
    APP_NAME="${2:?Usage: kc destroy <app-name>}"
    echo "Destroying $APP_NAME..."
    docker stop "kumbula-app-$APP_NAME" 2>/dev/null && docker rm "kumbula-app-$APP_NAME" 2>/dev/null
    echo "Removed"
    ;;

  help|*)
    echo "KumbulaCloud CLI"
    echo ""
    echo "Commands:"
    echo "  kc create <name>    Create a new app"
    echo "  kc apps             List deployed apps"
    echo "  kc logs <name>      Stream app logs"
    echo "  kc destroy <name>   Remove an app"
    ;;
esac
```

```bash
chmod +x ~/kumbula-poc/cli/kc
sudo ln -sf ~/kumbula-poc/cli/kc /usr/local/bin/kc
```

Now you can demo: `kc create my-api` -> write code -> `git push kumbula main` -> live.

---

## Phase 7: Validate the setup

Run `~/kumbula-poc/validate.sh` after completing all phases to verify everything works.

Create `~/kumbula-poc/validate.sh`:

```bash
#!/usr/bin/env bash
# KumbulaCloud PoC — Validation Script
# Run after completing all phases to verify the setup.
set -euo pipefail

PASS=0
FAIL=0
WARN=0

pass() { echo "  PASS: $1"; ((PASS++)); }
fail() { echo "  FAIL: $1"; ((FAIL++)); }
warn() { echo "  WARN: $1"; ((WARN++)); }

echo "============================================"
echo " KumbulaCloud PoC — Validation"
echo "============================================"
echo ""

# --- 1. Prerequisites ---
echo "[1/7] Prerequisites"
command -v docker &>/dev/null && pass "Docker installed ($(docker --version | cut -d' ' -f3 | tr -d ','))" || fail "Docker not installed"
command -v go &>/dev/null && pass "Go installed ($(go version | cut -d' ' -f3))" || fail "Go not installed"
command -v jq &>/dev/null && pass "jq installed" || fail "jq not installed"
command -v psql &>/dev/null && pass "psql installed" || fail "psql not installed"
echo ""

# --- 2. DNS ---
echo "[2/7] DNS Resolution"
if getent hosts test.kumbula.local &>/dev/null; then
  RESOLVED_IP=$(getent hosts test.kumbula.local | awk '{print $1}' | head -1)
  # Check resolution speed (should be < 1 second)
  START=$(date +%s%N)
  getent hosts speed-test.kumbula.local &>/dev/null
  END=$(date +%s%N)
  ELAPSED_MS=$(( (END - START) / 1000000 ))
  if [ "$ELAPSED_MS" -lt 1000 ]; then
    pass "*.kumbula.local resolves to $RESOLVED_IP (${ELAPSED_MS}ms)"
  else
    warn "*.kumbula.local resolves but slowly (${ELAPSED_MS}ms) — check AAAA record in dnsmasq"
  fi
else
  fail "*.kumbula.local does not resolve — check dnsmasq and /etc/resolv.conf"
fi
echo ""

# --- 3. Docker Compose Stack ---
echo "[3/7] Docker Compose Stack"
for svc in traefik gitea kumbula-postgres; do
  STATUS=$(docker inspect -f '{{.State.Status}}' "$svc" 2>/dev/null || echo "missing")
  if [ "$STATUS" = "running" ]; then
    pass "$svc is running"
  else
    fail "$svc is $STATUS"
  fi
done

# Check Traefik can see Docker containers
ROUTER_COUNT=$(curl -s http://localhost:8080/api/http/routers 2>/dev/null | python3 -c "
import sys,json
try:
  routers = json.load(sys.stdin)
  print(sum(1 for r in routers if r.get('provider') != 'internal'))
except: print(0)
" 2>/dev/null)
if [ "$ROUTER_COUNT" -gt 0 ]; then
  pass "Traefik sees $ROUTER_COUNT Docker route(s)"
else
  fail "Traefik has no Docker routes — check Docker API version compatibility"
fi
echo ""

# --- 4. Gitea ---
echo "[4/7] Gitea"
GITEA_STATUS=$(docker exec gitea curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/api/v1/settings/api 2>/dev/null)
if [ "$GITEA_STATUS" = "200" ]; then
  pass "Gitea API responding"
else
  fail "Gitea API returned $GITEA_STATUS (still in install mode?)"
fi

GITEA_USER=$(docker exec gitea curl -s -u "kumbula:kumbula123" http://localhost:3000/api/v1/user 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('login',''))" 2>/dev/null)
if [ "$GITEA_USER" = "kumbula" ]; then
  pass "Gitea admin user 'kumbula' exists"
else
  fail "Gitea admin user not found — run: docker exec -u git gitea gitea admin user create ..."
fi

GITEA_VIA_TRAEFIK=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: gitea.kumbula.local" http://localhost:80/ 2>/dev/null)
if [ "$GITEA_VIA_TRAEFIK" = "200" ]; then
  pass "Gitea accessible via Traefik"
else
  fail "Gitea not routed via Traefik (got $GITEA_VIA_TRAEFIK)"
fi
echo ""

# --- 5. Engine ---
echo "[5/7] Engine"
ENGINE_HEALTH=$(curl -s http://localhost:9000/health 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)
if [ "$ENGINE_HEALTH" = "healthy" ]; then
  pass "Engine is healthy on :9000"
else
  fail "Engine not responding — start it with: cd ~/kumbula-poc/engine && ./kumbula-engine"
fi
echo ""

# --- 6. PostgreSQL ---
echo "[6/7] PostgreSQL"
PG_OK=$(PGPASSWORD=kumbula_secret_2024 psql -h localhost -U kumbula_admin -d kumbula_system -c "SELECT 1" -t 2>/dev/null | tr -d ' \n')
if [ "$PG_OK" = "1" ]; then
  pass "PostgreSQL connection works"
else
  fail "Cannot connect to PostgreSQL"
fi
echo ""

# --- 7. Deployed Apps ---
echo "[7/7] Deployed Apps"
APP_COUNT=$(curl -s http://localhost:9000/apps 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
if [ "$APP_COUNT" -gt 0 ]; then
  pass "$APP_COUNT app(s) registered in engine"

  # Test each app via Traefik
  curl -s http://localhost:9000/apps 2>/dev/null | python3 -c "
import sys, json
apps = json.load(sys.stdin)
for name, info in apps.items():
    print(f'{name}|{info[\"status\"]}|{info[\"url\"]}')
" 2>/dev/null | while IFS='|' read -r APP_NAME APP_STATUS APP_URL; do
    CONTAINER_STATUS=$(docker inspect -f '{{.State.Status}}' "kumbula-app-$APP_NAME" 2>/dev/null || echo "missing")
    if [ "$CONTAINER_STATUS" = "running" ]; then
      # Test via Traefik
      HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 3 \
        -H "Host: ${APP_NAME}.kumbula.local" http://localhost:80/ 2>/dev/null)
      if [ "$HTTP_CODE" = "200" ]; then
        echo "  PASS: $APP_NAME -> container running, HTTP 200 via Traefik"
      else
        echo "  FAIL: $APP_NAME -> container running but Traefik returned $HTTP_CODE"
      fi
    else
      echo "  FAIL: $APP_NAME -> container is $CONTAINER_STATUS"
    fi
  done
else
  warn "No apps deployed yet — push an app to test the full pipeline"
fi
echo ""

# --- Summary ---
echo "============================================"
echo " Results: $PASS passed, $FAIL failed, $WARN warnings"
echo "============================================"
if [ "$FAIL" -gt 0 ]; then
  echo " Fix the failures above before running the demo."
  exit 1
else
  echo " KumbulaCloud is ready for the demo!"
  exit 0
fi
```

```bash
chmod +x ~/kumbula-poc/validate.sh
./validate.sh
```

---

## The demo script (what to show your co-founders)

Run through this in 5 minutes:

**1. "Let me create a new app"**
```bash
kc create demo-app
```

**2. "I'll write a quick API"** (show them you're writing real code)
```bash
cd /tmp && mkdir demo-app && cd demo-app
# Create a simple Express app (copy the sample above or write one live)
git init && git add . && git commit -m "first deploy"
git remote add kumbula http://gitea.kumbula.local/kumbula/demo-app.git
```

**3. "One push and it deploys"**
```bash
git push kumbula main
```

**4. Point to the engine terminal** — show the build output streaming in real time.

**5. Open the browser** — visit `http://demo-app.kumbula.local`
   - Show the database is connected
   - Refresh to show the visit counter incrementing
   - Point out: "This is running on THIS machine. No AWS. No Google. No Azure."

**6. "Let me push an update"** — change the heading, commit, push again. Watch it redeploy in seconds.

**7. The closer:**
> "Everything you just saw — the git server, the build system, the database provisioning, the routing — runs on a single laptop. Imagine this on 5 rack servers at Teraco in Joburg, with a proper dashboard and billing. That's what R5.4M buys us."

---

## What this PoC proves

| Investor concern | PoC demonstrates |
|-----------------|------------------|
| "Can you actually build this?" | Yes — working deploy pipeline, end to end |
| "Does auto-detection work?" | Node, Python, Go, static sites all detected |
| "What about databases?" | PostgreSQL provisioned per app automatically |
| "How does routing work?" | Traefik handles it via Docker labels — zero config |
| "Is it fast?" | Sub-30 second deploys on a laptop |
| "Can it scale?" | Same architecture works on bare metal — swap Docker for Nomad/K3s |

---

## Troubleshooting

### Traefik shows "client version 1.24 is too old"

Use `traefik:latest` instead of `traefik:v3.1` or `traefik:v3.3`. Older Traefik
images default to Docker API v1.24 which Docker 29+ rejects.

### Gitea stuck on install page

Set `INSTALL_LOCK = true` in `/data/gitea/conf/app.ini` inside the container and
restart: `docker restart gitea`. Then create the admin user via CLI.

### DNS resolves but takes 5 seconds

dnsmasq is only returning A records. Add an AAAA record so IPv6 lookups don't
time out:
```bash
echo "address=/kumbula.local/::" | sudo tee -a /etc/dnsmasq.d/kumbula.conf
sudo systemctl restart dnsmasq
```

### dnsmasq fails to start (port 53 in use)

systemd-resolved binds to `127.0.0.53:53`. dnsmasq tries `0.0.0.0:53` which
conflicts. Fix:
```bash
echo -e "listen-address=127.0.0.1\nbind-interfaces" | sudo tee /etc/dnsmasq.d/00-listen.conf
sudo systemctl restart dnsmasq
```

### Engine can't clone repos (gitea.kumbula.local not resolving)

The engine rewrites clone URLs to use Gitea's container IP automatically. If DNS
works on the host, cloning also works directly. Check with:
`getent hosts gitea.kumbula.local`

### Database auth fails on redeploy

The engine generates a new password on each deploy. It uses `ALTER ROLE` to
update existing roles. If you see "password authentication failed", check that
the engine uses the fixed `provisionDatabase` function (with the `ALTER ROLE`
fallback).

---

## Next steps after the PoC

Once co-founders are on board, the path to MVP:

1. **Dashboard** — React web UI for deploy history, logs, env vars, scaling
2. **Buildpacks** — Replace Dockerfile generation with Cloud Native Buildpacks
3. **Multi-tenancy** — User accounts, project isolation, resource limits
4. **Billing** — Usage metering per container (CPU/RAM/bandwidth)
5. **Real hardware** — Move from laptop to rack servers at Teraco JHB
6. **Custom domains** — Let's Encrypt wildcard + custom domain mapping
7. **Horizontal scaling** — Multiple containers per app behind Traefik
