# KumbulaCloud Frontend Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Railway-like web dashboard to KumbulaCloud so developers can sign up, create projects, manage builds, and view logs from a browser.

**Architecture:** Extend the existing Go engine into a multi-package monolith serving HTML templates + htmx from `dashboard.kumbula.local`. All state moves from in-memory maps to PostgreSQL. Gitea accounts are created behind the scenes on signup.

**Tech Stack:** Go `html/template`, htmx (vendored), Pico CSS, PostgreSQL, SSE for build log streaming, bcrypt for passwords, `crypto/rand` for session tokens.

**Spec:** `docs/superpowers/specs/2026-04-04-frontend-dashboard-design.md`

---

## File Structure

```
engine/
├── main.go                    # entrypoint — wires DB, router, starts server
├── db.go                      # DB connection pool + RunMigrations()
├── migrate.go                 # SQL table definitions, runs on startup
├── deploy.go                  # deploy(), buildImage(), runContainer(), stopContainer() — extracted from main.go
├── gitea.go                   # Gitea API client: CreateUser, CreateToken, CreateRepo, AddWebhook, DeleteRepo
├── models/
│   ├── user.go                # User struct, CreateUser, GetUserByUsername, GetUserByID
│   ├── project.go             # Project struct, CRUD, GetProjectsByUser, GetProjectByName
│   ├── build.go               # Build struct, CreateBuild, UpdateBuild, GetBuildsByProject, GetBuild, AppendLog
│   ├── envvar.go              # EnvVar struct, CRUD, GetEnvVarsByProject
│   └── session.go             # Session struct, CreateSession, GetSession, DeleteSession, CleanExpired
├── middleware/
│   ├── session.go             # LoadSession middleware — reads cookie, attaches user to context
│   ├── auth.go                # RequireAuth middleware — redirects to /login if no user
│   ├── csrf.go                # CSRF token generation + validation on POST/DELETE
│   └── ownership.go           # RequireProjectOwner — loads project, verifies ownership, 404 if not
├── handlers/
│   ├── auth.go                # GET/POST /login, /signup, POST /logout
│   ├── dashboard.go           # GET / — project cards grid
│   ├── projects.go            # GET/POST /projects/new, GET /projects/{name}, POST /projects/{name}/settings
│   ├── builds.go              # POST /projects/{name}/redeploy, GET /projects/{name}/builds/{id}/stream (SSE)
│   ├── envvars.go             # GET/POST/DELETE /projects/{name}/env
│   ├── partials.go            # GET /partials/project-cards (htmx polling)
│   └── webhook.go             # POST /webhook — existing logic adapted for multi-user
├── templates/
│   ├── layout.html            # base: html head, nav, htmx script, pico css, csrf meta tag
│   ├── auth/
│   │   ├── login.html
│   │   └── signup.html
│   ├── dashboard/
│   │   └── index.html         # project cards grid
│   ├── projects/
│   │   ├── new.html           # create project form
│   │   ├── detail.html        # overview + tabs for builds/env/settings
│   │   └── settings.html      # rename + destroy
│   └── partials/
│       ├── project_cards.html # htmx partial for dashboard polling
│       ├── build_list.html    # build history rows
│       ├── build_log.html     # single build log display
│       └── envvar_form.html   # env var table with inline editing
├── static/
│   ├── htmx.min.js           # vendored htmx 2.x
│   ├── sse.js                # htmx SSE extension
│   └── style.css             # KumbulaCloud-specific overrides on top of Pico
├── go.mod
└── go.sum
```

---

## Task 1: Project restructure — extract deploy logic and set up DB

Extract the deploy/build/container logic from `main.go` into `deploy.go`, set up a DB connection pool in `db.go`, and run migrations in `migrate.go`. After this task, the engine compiles and runs identically to before, but the code is split across files and connects to PostgreSQL on startup.

**Files:**
- Modify: `engine/main.go` (remove deploy functions, add DB init)
- Create: `engine/deploy.go` (deploy, buildImage, runContainer, stopContainer, ensureDockerfile, detectLanguage, helpers)
- Create: `engine/db.go` (OpenDB function)
- Create: `engine/migrate.go` (RunMigrations with all table DDL)

- [ ] **Step 1: Create `engine/deploy.go`**

Extract all deploy-related functions from `main.go`. These stay in `package main` for now (same binary). Move these functions:
- `deploy()`
- `ensureDockerfile()`
- `detectLanguage()`
- `buildImage()`
- `stopContainer()`
- `runContainer()`
- `sanitizeName()`
- `rewriteCloneURL()`
- `getContainerIP()`
- `generatePassword()`

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// deploy clones, detects, builds, and runs an app container.
// This is the existing deploy function moved from main.go.
func deploy(appName, cloneURL string) {
	start := time.Now()
	result := &DeployResult{
		AppName: appName,
		URL:     fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
		Status:  "building",
	}
	deployments[appName] = result

	log.Printf("  [%s] Cloning %s...", appName, cloneURL)
	cloneDir := filepath.Join(CLONE_BASE, appName)
	os.RemoveAll(cloneDir)

	cmd := exec.Command("git", "clone", "--depth=1", cloneURL, cloneDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("  [%s] Clone failed: %s\n%s", appName, err, string(out))
		result.Status = "clone_failed"
		return
	}

	log.Printf("  [%s] Detecting language...", appName)
	if err := ensureDockerfile(appName, cloneDir); err != nil {
		log.Printf("  [%s] Dockerfile generation failed: %s", appName, err)
		result.Status = "detect_failed"
		return
	}

	log.Printf("  [%s] Provisioning database...", appName)
	dbURL, err := provisionDatabase(appName)
	if err != nil {
		log.Printf("  [%s] Database provisioning failed (continuing): %s", appName, err)
		dbURL = ""
	} else {
		result.DatabaseURL = dbURL
		log.Printf("  [%s] Database ready", appName)
	}

	log.Printf("  [%s] Building image...", appName)
	imageName := fmt.Sprintf("kumbula/%s:latest", appName)
	if err := buildImage(appName, cloneDir, imageName); err != nil {
		log.Printf("  [%s] Build failed: %s", appName, err)
		result.Status = "build_failed"
		return
	}

	stopContainer(appName)

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

func ensureDockerfile(appName, dir string) error {
	dockerfilePath := filepath.Join(dir, "Dockerfile")

	if _, err := os.Stat(dockerfilePath); err == nil {
		log.Printf("  [%s] Found existing Dockerfile", appName)
		return nil
	}

	lang := detectLanguage(dir)
	log.Printf("  [%s] Detected: %s", appName, lang)

	var dockerfile string
	switch lang {
	case "node":
		dockerfile = "FROM node:20-alpine\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --production 2>/dev/null || npm install --production\nCOPY . .\nENV PORT=3000\nEXPOSE 3000\nCMD [\"node\", \"index.js\"]\n"
	case "python":
		dockerfile = "FROM python:3.12-slim\nWORKDIR /app\nCOPY requirements.txt* ./\nRUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true\nCOPY . .\nENV PORT=3000\nEXPOSE 3000\nCMD [\"python\", \"app.py\"]\n"
	case "go":
		dockerfile = "FROM golang:1.22-alpine AS builder\nWORKDIR /app\nCOPY go.* ./\nRUN go mod download\nCOPY . .\nRUN CGO_ENABLED=0 go build -o server .\n\nFROM alpine:3.20\nWORKDIR /app\nCOPY --from=builder /app/server .\nENV PORT=3000\nEXPOSE 3000\nCMD [\"./server\"]\n"
	default:
		dockerfile = "FROM nginx:alpine\nCOPY . /usr/share/nginx/html\nEXPOSE 80\n"
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

	db.Exec(fmt.Sprintf(`DO $$ BEGIN
		IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s') THEN
			CREATE ROLE %s WITH LOGIN PASSWORD '%s';
		ELSE
			ALTER ROLE %s WITH PASSWORD '%s';
		END IF;
	END $$`, dbUser, dbUser, dbPass, dbUser, dbPass))

	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT FROM pg_database WHERE datname = $1)", dbName).Scan(&exists)
	if !exists {
		db.Exec(fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbName, dbUser))
	}

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

- [ ] **Step 2: Create `engine/db.go`**

```go
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

// DB is the global database connection pool.
var DB *sql.DB

// OpenDB connects to PostgreSQL and stores the pool in the global DB var.
func OpenDB() error {
	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)

	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}

	if err := DB.Ping(); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}

	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)

	log.Printf("Connected to PostgreSQL")
	return nil
}
```

- [ ] **Step 3: Create `engine/migrate.go`**

```go
package main

import (
	"log"
)

// RunMigrations creates all tables if they don't exist.
func RunMigrations() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(50) UNIQUE NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			gitea_password VARCHAR(255) NOT NULL,
			gitea_token VARCHAR(255) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL REFERENCES users(id),
			name VARCHAR(100) UNIQUE NOT NULL,
			gitea_repo VARCHAR(255) NOT NULL,
			container_id VARCHAR(64),
			status VARCHAR(20) NOT NULL DEFAULT 'created',
			url VARCHAR(255) NOT NULL,
			database_url TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS builds (
			id SERIAL PRIMARY KEY,
			project_id INT NOT NULL REFERENCES projects(id),
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			log TEXT NOT NULL DEFAULT '',
			commit_sha VARCHAR(40) NOT NULL DEFAULT '',
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			finished_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS project_env_vars (
			id SERIAL PRIMARY KEY,
			project_id INT NOT NULL REFERENCES projects(id),
			key VARCHAR(255) NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(project_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token VARCHAR(64) PRIMARY KEY,
			user_id INT NOT NULL REFERENCES users(id),
			expires_at TIMESTAMPTZ NOT NULL
		)`,
	}

	for _, m := range migrations {
		if _, err := DB.Exec(m); err != nil {
			return err
		}
	}

	log.Printf("Database migrations complete")
	return nil
}
```

- [ ] **Step 4: Slim down `engine/main.go`**

Replace the entire `main.go` with only the constants, types, global state, and the HTTP setup. All deploy functions are now in `deploy.go`.

```go
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
		log.Fatalf("Database connection failed: %s", err)
	}
	defer DB.Close()

	if err := RunMigrations(); err != nil {
		log.Fatalf("Migration failed: %s", err)
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

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deploying",
		"app":    appName,
		"url":    fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
	})

	go deploy(appName, cloneURL)
}
```

- [ ] **Step 5: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build, no errors.

- [ ] **Step 6: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/deploy.go engine/db.go engine/migrate.go engine/main.go
git commit -m "refactor: extract deploy logic, add DB connection and migrations"
```

---

## Task 2: Models — user, session, project, build, env var

Create the model layer with CRUD functions for each entity. Each model file lives in `package main` (same as the rest of the engine — we keep it flat since Go's `internal/` packages aren't needed for a single-binary PoC). Tests use a real PostgreSQL connection to `kumbula_system`.

**Files:**
- Create: `engine/models_user.go`
- Create: `engine/models_session.go`
- Create: `engine/models_project.go`
- Create: `engine/models_build.go`
- Create: `engine/models_envvar.go`
- Create: `engine/models_test.go`

- [ ] **Step 1: Add `golang.org/x/crypto` dependency for bcrypt**

Run: `cd /home/kijani/kumbula-poc/engine && go get golang.org/x/crypto/bcrypt`

- [ ] **Step 2: Create `engine/models_user.go`**

```go
package main

import (
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID            int
	Username      string
	Email         string
	PasswordHash  string
	GiteaPassword string
	GiteaToken    string
	CreatedAt     time.Time
}

// CreateUser inserts a new user and returns the created User with its ID.
func CreateUser(username, email, password, giteaPassword string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}

	u := &User{}
	err = DB.QueryRow(
		`INSERT INTO users (username, email, password_hash, gitea_password)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, gitea_password, gitea_token, created_at`,
		username, email, string(hash), giteaPassword,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByUsername returns a user or sql.ErrNoRows.
func GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, created_at
		 FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByID returns a user or sql.ErrNoRows.
func GetUserByID(id int) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, created_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateGiteaToken sets the gitea_token for a user.
func UpdateGiteaToken(userID int, token string) error {
	_, err := DB.Exec(`UPDATE users SET gitea_token = $1 WHERE id = $2`, token, userID)
	return err
}

// CheckPassword compares a plaintext password against the stored hash.
func CheckPassword(user *User, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil
}
```

- [ ] **Step 3: Create `engine/models_session.go`**

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Session struct {
	Token     string
	UserID    int
	ExpiresAt time.Time
}

// CreateSession generates a random token and inserts a session row.
func CreateSession(userID int) (*Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(7 * 24 * time.Hour) // 7 days

	_, err := DB.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, UserID: userID, ExpiresAt: expiresAt}, nil
}

// GetSession returns a valid (non-expired) session or sql.ErrNoRows.
func GetSession(token string) (*Session, error) {
	s := &Session{}
	err := DB.QueryRow(
		`SELECT token, user_id, expires_at FROM sessions
		 WHERE token = $1 AND expires_at > NOW()`, token,
	).Scan(&s.Token, &s.UserID, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// DeleteSession removes a session by token.
func DeleteSession(token string) error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE token = $1`, token)
	return err
}

// CleanExpiredSessions removes all expired sessions.
func CleanExpiredSessions() error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE expires_at <= NOW()`)
	return err
}
```

- [ ] **Step 4: Create `engine/models_project.go`**

```go
package main

import (
	"database/sql"
	"time"
)

type Project struct {
	ID          int
	UserID      int
	Name        string
	GiteaRepo   string
	ContainerID sql.NullString
	Status      string
	URL         string
	DatabaseURL sql.NullString
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateProject inserts a new project.
func CreateProject(userID int, name, giteaRepo, url string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`INSERT INTO projects (user_id, name, gitea_repo, url)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at`,
		userID, name, giteaRepo, url,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GetProjectsByUser returns all projects for a user, ordered by most recent.
func GetProjectsByUser(userID int) ([]*Project, error) {
	rows, err := DB.Query(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at
		 FROM projects WHERE user_id = $1 ORDER BY updated_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectByName returns a project by its unique name.
func GetProjectByName(name string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at
		 FROM projects WHERE name = $1`, name,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateProjectStatus updates the status and optionally the container_id.
func UpdateProjectStatus(projectID int, status, containerID string) error {
	_, err := DB.Exec(
		`UPDATE projects SET status = $1, container_id = $2, updated_at = NOW() WHERE id = $3`,
		status, sql.NullString{String: containerID, Valid: containerID != ""}, projectID,
	)
	return err
}

// UpdateProjectDatabaseURL sets the database_url for a project.
func UpdateProjectDatabaseURL(projectID int, dbURL string) error {
	_, err := DB.Exec(
		`UPDATE projects SET database_url = $1, updated_at = NOW() WHERE id = $2`,
		sql.NullString{String: dbURL, Valid: dbURL != ""}, projectID,
	)
	return err
}

// DeleteProject removes a project row.
func DeleteProject(projectID int) error {
	_, err := DB.Exec(`DELETE FROM projects WHERE id = $1`, projectID)
	return err
}
```

- [ ] **Step 5: Create `engine/models_build.go`**

```go
package main

import (
	"database/sql"
	"time"
)

type Build struct {
	ID         int
	ProjectID  int
	Status     string
	Log        string
	CommitSHA  string
	StartedAt  time.Time
	FinishedAt sql.NullTime
}

// CreateBuild inserts a new build record.
func CreateBuild(projectID int, commitSHA string) (*Build, error) {
	b := &Build{}
	err := DB.QueryRow(
		`INSERT INTO builds (project_id, status, commit_sha)
		 VALUES ($1, 'building', $2)
		 RETURNING id, project_id, status, log, commit_sha, started_at, finished_at`,
		projectID, commitSHA,
	).Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// GetBuildsByProject returns builds for a project, most recent first.
func GetBuildsByProject(projectID int) ([]*Build, error) {
	rows, err := DB.Query(
		`SELECT id, project_id, status, log, commit_sha, started_at, finished_at
		 FROM builds WHERE project_id = $1 ORDER BY started_at DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		b := &Build{}
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		builds = append(builds, b)
	}
	return builds, nil
}

// GetBuild returns a single build by ID.
func GetBuild(buildID int) (*Build, error) {
	b := &Build{}
	err := DB.QueryRow(
		`SELECT id, project_id, status, log, commit_sha, started_at, finished_at
		 FROM builds WHERE id = $1`, buildID,
	).Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// AppendBuildLog appends text to a build's log.
func AppendBuildLog(buildID int, text string) error {
	_, err := DB.Exec(`UPDATE builds SET log = log || $1 WHERE id = $2`, text, buildID)
	return err
}

// FinishBuild sets the final status and finished_at timestamp.
func FinishBuild(buildID int, status string) error {
	_, err := DB.Exec(
		`UPDATE builds SET status = $1, finished_at = NOW() WHERE id = $2`, status, buildID,
	)
	return err
}
```

- [ ] **Step 6: Create `engine/models_envvar.go`**

```go
package main

import (
	"time"
)

type EnvVar struct {
	ID        int
	ProjectID int
	Key       string
	Value     string
	CreatedAt time.Time
}

// GetEnvVarsByProject returns all env vars for a project.
func GetEnvVarsByProject(projectID int) ([]*EnvVar, error) {
	rows, err := DB.Query(
		`SELECT id, project_id, key, value, created_at
		 FROM project_env_vars WHERE project_id = $1 ORDER BY key`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vars []*EnvVar
	for rows.Next() {
		v := &EnvVar{}
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.Key, &v.Value, &v.CreatedAt); err != nil {
			return nil, err
		}
		vars = append(vars, v)
	}
	return vars, nil
}

// SetEnvVar upserts an env var (insert or update on conflict).
func SetEnvVar(projectID int, key, value string) error {
	_, err := DB.Exec(
		`INSERT INTO project_env_vars (project_id, key, value)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, key) DO UPDATE SET value = EXCLUDED.value`,
		projectID, key, value,
	)
	return err
}

// DeleteEnvVar removes an env var by ID.
func DeleteEnvVar(envVarID int) error {
	_, err := DB.Exec(`DELETE FROM project_env_vars WHERE id = $1`, envVarID)
	return err
}
```

- [ ] **Step 7: Write `engine/models_test.go`**

Tests use the real `kumbula_system` database. They clean up after themselves.

```go
package main

import (
	"testing"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	if err := OpenDB(); err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	t.Cleanup(func() {
		// Clean up test data in reverse FK order
		DB.Exec("DELETE FROM project_env_vars")
		DB.Exec("DELETE FROM builds")
		DB.Exec("DELETE FROM sessions")
		DB.Exec("DELETE FROM projects")
		DB.Exec("DELETE FROM users")
		DB.Close()
	})
}

func TestCreateAndGetUser(t *testing.T) {
	setupTestDB(t)

	u, err := CreateUser("testuser", "test@example.com", "password123", "gitea_pass_abc")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("expected non-zero user ID")
	}
	if u.Username != "testuser" {
		t.Fatalf("expected username testuser, got %s", u.Username)
	}

	got, err := GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("expected ID %d, got %d", u.ID, got.ID)
	}

	got2, err := GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got2.Username != "testuser" {
		t.Fatalf("expected testuser, got %s", got2.Username)
	}
}

func TestCheckPassword(t *testing.T) {
	setupTestDB(t)

	u, err := CreateUser("passuser", "pass@example.com", "correct-horse", "gp")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if !CheckPassword(u, "correct-horse") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(u, "wrong-password") {
		t.Fatal("expected password to NOT match")
	}
}

func TestSessionLifecycle(t *testing.T) {
	setupTestDB(t)

	u, _ := CreateUser("sessuser", "sess@example.com", "pw", "gp")

	s, err := CreateSession(u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(s.Token) != 64 {
		t.Fatalf("expected 64-char token, got %d", len(s.Token))
	}

	got, err := GetSession(s.Token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != u.ID {
		t.Fatalf("expected user_id %d, got %d", u.ID, got.UserID)
	}

	if err := DeleteSession(s.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err = GetSession(s.Token)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestProjectCRUD(t *testing.T) {
	setupTestDB(t)

	u, _ := CreateUser("projuser", "proj@example.com", "pw", "gp")

	p, err := CreateProject(u.ID, "my-app", "projuser/my-app", "http://my-app.kumbula.local")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Status != "created" {
		t.Fatalf("expected status created, got %s", p.Status)
	}

	got, err := GetProjectByName("my-app")
	if err != nil {
		t.Fatalf("GetProjectByName: %v", err)
	}
	if got.ID != p.ID {
		t.Fatalf("expected ID %d, got %d", p.ID, got.ID)
	}

	projects, err := GetProjectsByUser(u.ID)
	if err != nil {
		t.Fatalf("GetProjectsByUser: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}

	if err := UpdateProjectStatus(p.ID, "running", "abc123"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}

	got, _ = GetProjectByName("my-app")
	if got.Status != "running" {
		t.Fatalf("expected running, got %s", got.Status)
	}

	if err := DeleteProject(p.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
}

func TestBuildLifecycle(t *testing.T) {
	setupTestDB(t)

	u, _ := CreateUser("builduser", "build@example.com", "pw", "gp")
	p, _ := CreateProject(u.ID, "build-app", "builduser/build-app", "http://build-app.kumbula.local")

	b, err := CreateBuild(p.ID, "abc1234")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	if b.Status != "building" {
		t.Fatalf("expected building, got %s", b.Status)
	}

	if err := AppendBuildLog(b.ID, "Step 1: clone\n"); err != nil {
		t.Fatalf("AppendBuildLog: %v", err)
	}
	if err := AppendBuildLog(b.ID, "Step 2: build\n"); err != nil {
		t.Fatalf("AppendBuildLog: %v", err)
	}

	got, _ := GetBuild(b.ID)
	if got.Log != "Step 1: clone\nStep 2: build\n" {
		t.Fatalf("unexpected log: %q", got.Log)
	}

	if err := FinishBuild(b.ID, "success"); err != nil {
		t.Fatalf("FinishBuild: %v", err)
	}

	got, _ = GetBuild(b.ID)
	if got.Status != "success" {
		t.Fatalf("expected success, got %s", got.Status)
	}

	builds, err := GetBuildsByProject(p.ID)
	if err != nil {
		t.Fatalf("GetBuildsByProject: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(builds))
	}
}

func TestEnvVarCRUD(t *testing.T) {
	setupTestDB(t)

	u, _ := CreateUser("envuser", "env@example.com", "pw", "gp")
	p, _ := CreateProject(u.ID, "env-app", "envuser/env-app", "http://env-app.kumbula.local")

	if err := SetEnvVar(p.ID, "API_KEY", "secret123"); err != nil {
		t.Fatalf("SetEnvVar: %v", err)
	}

	vars, err := GetEnvVarsByProject(p.ID)
	if err != nil {
		t.Fatalf("GetEnvVarsByProject: %v", err)
	}
	if len(vars) != 1 {
		t.Fatalf("expected 1 var, got %d", len(vars))
	}
	if vars[0].Key != "API_KEY" || vars[0].Value != "secret123" {
		t.Fatalf("unexpected var: %s=%s", vars[0].Key, vars[0].Value)
	}

	// Upsert
	if err := SetEnvVar(p.ID, "API_KEY", "updated"); err != nil {
		t.Fatalf("SetEnvVar upsert: %v", err)
	}

	vars, _ = GetEnvVarsByProject(p.ID)
	if vars[0].Value != "updated" {
		t.Fatalf("expected updated, got %s", vars[0].Value)
	}

	if err := DeleteEnvVar(vars[0].ID); err != nil {
		t.Fatalf("DeleteEnvVar: %v", err)
	}

	vars, _ = GetEnvVarsByProject(p.ID)
	if len(vars) != 0 {
		t.Fatalf("expected 0 vars, got %d", len(vars))
	}
}
```

- [ ] **Step 8: Run tests**

Run: `cd /home/kijani/kumbula-poc/engine && go test -v -count=1 .`
Expected: all 6 tests pass (TestCreateAndGetUser, TestCheckPassword, TestSessionLifecycle, TestProjectCRUD, TestBuildLifecycle, TestEnvVarCRUD).

- [ ] **Step 9: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/models_user.go engine/models_session.go engine/models_project.go engine/models_build.go engine/models_envvar.go engine/models_test.go engine/go.mod engine/go.sum
git commit -m "feat: add model layer with user, session, project, build, envvar CRUD"
```

---

## Task 3: Gitea API client

Create a Gitea API client that can create users, generate tokens, create repos, add webhooks, and delete repos. This is used during signup and project creation.

**Files:**
- Create: `engine/gitea.go`
- Create: `engine/gitea_test.go`

- [ ] **Step 1: Create `engine/gitea.go`**

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// GiteaAdminToken is the admin API token, read from GITEA_ADMIN_TOKEN env var.
var GiteaAdminToken string

// GiteaURL is the internal URL for the Gitea API.
var GiteaURL string

func InitGitea() {
	GiteaAdminToken = os.Getenv("GITEA_ADMIN_TOKEN")
	if GiteaAdminToken == "" {
		GiteaAdminToken = os.Getenv("GITEA_TOKEN") // fallback to existing env var
	}
	// Use container IP for API calls from the host
	giteaIP := getContainerIP("gitea")
	if giteaIP != "" {
		GiteaURL = fmt.Sprintf("http://%s:3000", giteaIP)
	} else {
		GiteaURL = fmt.Sprintf("http://%s", GITEA_DOMAIN)
	}
}

// giteaRequest makes an authenticated request to Gitea.
func giteaRequest(method, path, token string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, GiteaURL+path, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// GiteaCreateUser creates a new Gitea user via the admin API.
func GiteaCreateUser(username, email, password string) error {
	payload := map[string]interface{}{
		"username":             username,
		"email":                email,
		"password":             password,
		"must_change_password": false,
	}

	_, status, err := giteaRequest("POST", "/api/v1/admin/users", GiteaAdminToken, payload)
	if err != nil {
		return fmt.Errorf("gitea create user: %w", err)
	}
	if status != 201 {
		return fmt.Errorf("gitea create user: status %d", status)
	}
	return nil
}

// GiteaCreateToken creates an API token for a Gitea user. Returns the token string.
func GiteaCreateToken(username, password string) (string, error) {
	payload := map[string]interface{}{
		"name":   "kumbula-engine",
		"scopes": []string{"all"},
	}

	// Use basic auth for token creation
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(payload)

	req, err := http.NewRequest("POST", GiteaURL+fmt.Sprintf("/api/v1/users/%s/tokens", username), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gitea create token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("gitea create token: status %d", resp.StatusCode)
	}

	var result struct {
		Sha1 string `json:"sha1"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Sha1, nil
}

// GiteaCreateRepo creates a repo under the given user's account.
func GiteaCreateRepo(userToken, repoName string) (string, error) {
	payload := map[string]interface{}{
		"name":      repoName,
		"auto_init": true,
		"private":   false,
	}

	data, status, err := giteaRequest("POST", "/api/v1/user/repos", userToken, payload)
	if err != nil {
		return "", fmt.Errorf("gitea create repo: %w", err)
	}
	if status != 201 {
		return "", fmt.Errorf("gitea create repo: status %d", status)
	}

	var result struct {
		CloneURL string `json:"clone_url"`
	}
	json.Unmarshal(data, &result)
	return result.CloneURL, nil
}

// GiteaAddWebhook adds a push webhook to a repo.
func GiteaAddWebhook(userToken, owner, repoName, webhookURL string) error {
	payload := map[string]interface{}{
		"type":   "gitea",
		"active": true,
		"events": []string{"push"},
		"config": map[string]string{
			"url":          webhookURL,
			"content_type": "json",
		},
	}

	_, status, err := giteaRequest("POST", fmt.Sprintf("/api/v1/repos/%s/%s/hooks", owner, repoName), userToken, payload)
	if err != nil {
		return fmt.Errorf("gitea add webhook: %w", err)
	}
	if status != 201 {
		return fmt.Errorf("gitea add webhook: status %d", status)
	}
	return nil
}

// GiteaDeleteRepo deletes a repo.
func GiteaDeleteRepo(userToken, owner, repoName string) error {
	_, status, err := giteaRequest("DELETE", fmt.Sprintf("/api/v1/repos/%s/%s", owner, repoName), userToken, nil)
	if err != nil {
		return fmt.Errorf("gitea delete repo: %w", err)
	}
	if status != 204 {
		return fmt.Errorf("gitea delete repo: status %d", status)
	}
	return nil
}
```

- [ ] **Step 2: Add `InitGitea()` call to `main.go`**

In `main.go`, add `InitGitea()` call right after `RunMigrations()`:

```go
	if err := RunMigrations(); err != nil {
		log.Fatalf("Migration failed: %s", err)
	}

	InitGitea()
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/gitea.go engine/main.go
git commit -m "feat: add Gitea API client for user/repo/webhook management"
```

---

## Task 4: Middleware — session, auth, CSRF, project ownership

Create the middleware stack. These are `func(http.Handler) http.Handler` wrappers that read sessions, enforce auth, validate CSRF, and check project ownership.

**Files:**
- Create: `engine/middleware.go` (all middleware in one file — not enough code to justify splitting)
- Create: `engine/context.go` (context key helpers)

- [ ] **Step 1: Create `engine/context.go`**

```go
package main

import (
	"context"
	"net/http"
)

type contextKey string

const (
	ctxUserKey    contextKey = "user"
	ctxProjectKey contextKey = "project"
	ctxCSRFKey    contextKey = "csrf"
)

// CtxUser returns the logged-in user from the request context, or nil.
func CtxUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxUserKey).(*User)
	return u
}

// CtxProject returns the loaded project from the request context, or nil.
func CtxProject(r *http.Request) *Project {
	p, _ := r.Context().Value(ctxProjectKey).(*Project)
	return p
}

// CtxCSRF returns the CSRF token from the request context.
func CtxCSRF(r *http.Request) string {
	s, _ := r.Context().Value(ctxCSRFKey).(string)
	return s
}

func withUser(r *http.Request, u *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u))
}

func withProject(r *http.Request, p *Project) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxProjectKey, p))
}

func withCSRF(r *http.Request, token string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxCSRFKey, token))
}
```

- [ ] **Step 2: Create `engine/middleware.go`**

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

const sessionCookieName = "kumbula_session"

// SessionMiddleware loads the session and user from the cookie on every request.
// Also generates/loads a CSRF token stored in the session cookie's associated data.
func SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		session, err := GetSession(cookie.Value)
		if err != nil {
			// Invalid or expired session — clear the cookie
			http.SetCookie(w, &http.Cookie{
				Name:   sessionCookieName,
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})
			next.ServeHTTP(w, r)
			return
		}

		user, err := GetUserByID(session.UserID)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		r = withUser(r, user)
		// Use first 32 chars of session token as CSRF token (deterministic per session)
		r = withCSRF(r, session.Token[:32])
		next.ServeHTTP(w, r)
	})
}

// RequireAuth redirects to /login if no user is in context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CtxUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFProtect checks the CSRF token on POST/DELETE requests.
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			expected := CtxCSRF(r)
			if expected == "" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			got := r.FormValue("_csrf")
			if got == "" {
				got = r.Header.Get("X-CSRF-Token")
			}
			if got != expected {
				http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireProjectOwner loads a project by name from the URL path and verifies ownership.
// The project name is extracted from the path: /projects/{name}/...
// Returns 404 if the project doesn't exist or doesn't belong to the user.
func RequireProjectOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract project name from path: /projects/{name}[/...]
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "projects" {
			http.NotFound(w, r)
			return
		}
		projectName := parts[1]
		if projectName == "new" {
			// /projects/new is not a project route
			next.ServeHTTP(w, r)
			return
		}

		project, err := GetProjectByName(projectName)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		user := CtxUser(r)
		if user == nil || project.UserID != user.ID {
			http.NotFound(w, r)
			return
		}

		r = withProject(r, project)
		next.ServeHTTP(w, r)
	})
}

// generateCSRFToken creates a random CSRF token (used as fallback).
func generateCSRFToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/context.go engine/middleware.go
git commit -m "feat: add session, auth, CSRF, and ownership middleware"
```

---

## Task 5: Template engine and layout

Set up the template rendering system with a shared layout, Pico CSS, and vendored htmx. Create the base layout and auth templates (login + signup).

**Files:**
- Create: `engine/templates.go` (template loading + render helpers)
- Create: `engine/templates/layout.html`
- Create: `engine/templates/auth/login.html`
- Create: `engine/templates/auth/signup.html`
- Create: `engine/static/style.css`

- [ ] **Step 1: Create `engine/templates.go`**

```go
package main

import (
	"html/template"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
)

var templates *template.Template

// InitTemplates loads all templates from the templates/ directory.
func InitTemplates() error {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	tmplDir := filepath.Join(baseDir, "templates")

	funcMap := template.FuncMap{
		"upper": strings.ToUpper,
		"statusClass": func(status string) string {
			switch status {
			case "running":
				return "status-running"
			case "building":
				return "status-building"
			case "failed", "build_failed", "clone_failed", "run_failed", "detect_failed":
				return "status-failed"
			default:
				return "status-default"
			}
		},
	}

	var err error
	templates, err = template.New("").Funcs(funcMap).ParseGlob(filepath.Join(tmplDir, "**/*.html"))
	if err != nil {
		// Try flat + nested pattern
		templates, err = template.New("").Funcs(funcMap).ParseGlob(filepath.Join(tmplDir, "*.html"))
		if err != nil {
			return err
		}
		// Parse subdirectories
		for _, sub := range []string{"auth", "dashboard", "projects", "partials"} {
			pattern := filepath.Join(tmplDir, sub, "*.html")
			templates, err = templates.ParseGlob(pattern)
			if err != nil {
				// Subdirectory may not exist yet, skip
				continue
			}
		}
	}

	return nil
}

// RenderPage renders a named template inside the layout with common data.
func RenderPage(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["User"] = CtxUser(r)
	data["CSRF"] = CtxCSRF(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// RenderPartial renders a template fragment (no layout wrapper).
func RenderPartial(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["User"] = CtxUser(r)
	data["CSRF"] = CtxCSRF(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 2: Create directory structure**

Run:
```bash
mkdir -p /home/kijani/kumbula-poc/engine/templates/auth
mkdir -p /home/kijani/kumbula-poc/engine/templates/dashboard
mkdir -p /home/kijani/kumbula-poc/engine/templates/projects
mkdir -p /home/kijani/kumbula-poc/engine/templates/partials
mkdir -p /home/kijani/kumbula-poc/engine/static
```

- [ ] **Step 3: Create `engine/templates/layout.html`**

```html
{{define "layout"}}
<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{if .Title}}{{.Title}} — {{end}}KumbulaCloud</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css">
    <link rel="stylesheet" href="/static/style.css">
    <script src="/static/htmx.min.js"></script>
    <script src="/static/sse.js"></script>
</head>
<body>
    <nav class="container-fluid">
        <ul>
            <li><strong><a href="/">KumbulaCloud</a></strong></li>
        </ul>
        {{if .User}}
        <ul>
            <li>{{.User.Username}}</li>
            <li>
                <form method="POST" action="/logout" style="margin:0">
                    <input type="hidden" name="_csrf" value="{{.CSRF}}">
                    <button type="submit" class="outline secondary btn-sm">Logout</button>
                </form>
            </li>
        </ul>
        {{end}}
    </nav>
    <main class="container">
        {{template "content" .}}
    </main>
</body>
</html>
{{end}}
```

- [ ] **Step 4: Create `engine/templates/auth/login.html`**

```html
{{define "content"}}
<article class="auth-card">
    <header>
        <h2>Log in to KumbulaCloud</h2>
    </header>
    {{if .Error}}
    <p class="error-msg">{{.Error}}</p>
    {{end}}
    <form method="POST" action="/login">
        <input type="hidden" name="_csrf" value="{{.CSRF}}">
        <label for="username">Username</label>
        <input type="text" id="username" name="username" required autofocus>
        <label for="password">Password</label>
        <input type="password" id="password" name="password" required>
        <button type="submit">Log in</button>
    </form>
    <p>Don't have an account? <a href="/signup">Sign up</a></p>
</article>
{{end}}
```

- [ ] **Step 5: Create `engine/templates/auth/signup.html`**

```html
{{define "content"}}
<article class="auth-card">
    <header>
        <h2>Create your KumbulaCloud account</h2>
    </header>
    {{if .Error}}
    <p class="error-msg">{{.Error}}</p>
    {{end}}
    <form method="POST" action="/signup">
        <input type="hidden" name="_csrf" value="{{.CSRF}}">
        <label for="username">Username</label>
        <input type="text" id="username" name="username" required autofocus
               pattern="[a-z][a-z0-9-]{1,48}[a-z0-9]" title="Lowercase letters, numbers, hyphens. 3-50 chars.">
        <label for="email">Email</label>
        <input type="email" id="email" name="email" required>
        <label for="password">Password</label>
        <input type="password" id="password" name="password" required minlength="8">
        <button type="submit">Create account</button>
    </form>
    <p>Already have an account? <a href="/login">Log in</a></p>
</article>
{{end}}
```

- [ ] **Step 6: Create `engine/static/style.css`**

```css
/* KumbulaCloud — custom overrides on Pico CSS */

.auth-card {
    max-width: 420px;
    margin: 2rem auto;
}

.error-msg {
    color: var(--pico-del-color);
    background: var(--pico-mark-background-color);
    padding: 0.5rem 1rem;
    border-radius: var(--pico-border-radius);
}

.btn-sm {
    padding: 0.25rem 0.75rem;
    font-size: 0.875rem;
}

/* Project cards grid */
.project-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 1rem;
}

.project-card {
    position: relative;
}

.project-card .status-badge {
    display: inline-block;
    padding: 0.15rem 0.5rem;
    border-radius: 1rem;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
}

.status-running { color: #22c55e; }
.status-building { color: #f59e0b; }
.status-failed { color: #ef4444; }
.status-default { color: #94a3b8; }

.status-running .status-badge { background: rgba(34, 197, 94, 0.15); }
.status-building .status-badge { background: rgba(245, 158, 11, 0.15); }
.status-failed .status-badge { background: rgba(239, 68, 68, 0.15); }
.status-default .status-badge { background: rgba(148, 163, 184, 0.15); }

/* Build log */
.build-log {
    background: #0d1117;
    color: #c9d1d9;
    padding: 1rem;
    border-radius: var(--pico-border-radius);
    font-family: monospace;
    font-size: 0.8rem;
    max-height: 500px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-wrap: break-word;
}

/* Env vars table */
.envvar-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    margin-bottom: 0.5rem;
}

.envvar-row input {
    margin-bottom: 0;
}

.envvar-key {
    flex: 1;
}

.envvar-value {
    flex: 2;
}

/* Danger zone */
.danger-zone {
    border: 1px solid var(--pico-del-color);
    border-radius: var(--pico-border-radius);
    padding: 1rem;
    margin-top: 2rem;
}

.danger-zone h4 {
    color: var(--pico-del-color);
}

/* Header row with button */
.page-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
}

/* Tabs */
.tabs {
    display: flex;
    gap: 0;
    border-bottom: 1px solid var(--pico-muted-border-color);
    margin-bottom: 1.5rem;
}

.tabs a {
    padding: 0.5rem 1rem;
    text-decoration: none;
    border-bottom: 2px solid transparent;
    color: var(--pico-muted-color);
}

.tabs a.active {
    border-bottom-color: var(--pico-primary);
    color: var(--pico-primary);
}
```

- [ ] **Step 7: Download htmx and SSE extension**

Run:
```bash
curl -sL https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js -o /home/kijani/kumbula-poc/engine/static/htmx.min.js
curl -sL https://unpkg.com/htmx-ext-sse@2.2.2/sse.js -o /home/kijani/kumbula-poc/engine/static/sse.js
```

- [ ] **Step 8: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 9: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/templates.go engine/templates/ engine/static/
git commit -m "feat: add template engine, layout, auth templates, Pico CSS, htmx"
```

---

## Task 6: Auth handlers — signup, login, logout

Wire up the auth endpoints. Signup creates a KumbulaCloud user + Gitea user + session. Login verifies password + creates session. Logout deletes session.

**Files:**
- Create: `engine/handlers_auth.go`
- Modify: `engine/main.go` (add routes + middleware wiring)

- [ ] **Step 1: Create `engine/handlers_auth.go`**

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

var usernameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,48}[a-z0-9]$`)

var reservedNames = map[string]bool{
	"traefik": true, "gitea": true, "dashboard": true,
	"postgres": true, "api": true, "admin": true, "www": true,
	"new": true, "login": true, "signup": true, "logout": true,
	"static": true, "health": true, "webhook": true, "apps": true,
	"partials": true, "projects": true,
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if CtxUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	RenderPage(w, r, "layout", map[string]interface{}{
		"Title":   "Log in",
		"Content": "login",
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	user, err := GetUserByUsername(username)
	if err != nil || !CheckPassword(user, password) {
		RenderPage(w, r, "layout", map[string]interface{}{
			"Title":   "Log in",
			"Content": "login",
			"Error":   "Invalid username or password.",
		})
		return
	}

	session, err := CreateSession(user.ID)
	if err != nil {
		RenderPage(w, r, "layout", map[string]interface{}{
			"Title":   "Log in",
			"Content": "login",
			"Error":   "Something went wrong. Please try again.",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleSignupPage(w http.ResponseWriter, r *http.Request) {
	if CtxUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	RenderPage(w, r, "layout", map[string]interface{}{
		"Title":   "Sign up",
		"Content": "signup",
	})
}

func handleSignup(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(strings.ToLower(r.FormValue("username")))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	renderErr := func(msg string) {
		RenderPage(w, r, "layout", map[string]interface{}{
			"Title":   "Sign up",
			"Content": "signup",
			"Error":   msg,
		})
	}

	// Validate
	if !usernameRegex.MatchString(username) {
		renderErr("Username must be 3-50 characters: lowercase letters, numbers, and hyphens.")
		return
	}
	if reservedNames[username] {
		renderErr("That username is reserved. Please choose another.")
		return
	}
	if len(password) < 8 {
		renderErr("Password must be at least 8 characters.")
		return
	}

	// Generate Gitea password
	giteaPass := generatePassword(32)

	// Create KumbulaCloud user
	user, err := CreateUser(username, email, password, giteaPass)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			renderErr("Username or email already taken.")
		} else {
			log.Printf("CreateUser error: %v", err)
			renderErr("Something went wrong. Please try again.")
		}
		return
	}

	// Create Gitea user
	if err := GiteaCreateUser(username, email, giteaPass); err != nil {
		log.Printf("GiteaCreateUser error: %v", err)
		// Roll back: delete the KumbulaCloud user
		DB.Exec("DELETE FROM users WHERE id = $1", user.ID)
		renderErr("Failed to set up your account. Please try again.")
		return
	}

	// Create Gitea API token
	token, err := GiteaCreateToken(username, giteaPass)
	if err != nil {
		log.Printf("GiteaCreateToken error: %v", err)
		renderErr("Account created but token generation failed. Please contact support.")
		return
	}

	if err := UpdateGiteaToken(user.ID, token); err != nil {
		log.Printf("UpdateGiteaToken error: %v", err)
	}

	// Create session
	session, err := CreateSession(user.ID)
	if err != nil {
		log.Printf("CreateSession error: %v", err)
		// User was created, redirect to login
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	log.Printf("New user registered: %s", username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ValidateProjectName checks if a name is valid for a project.
func ValidateProjectName(name string) error {
	if !usernameRegex.MatchString(name) {
		return fmt.Errorf("name must be 3-50 characters: lowercase letters, numbers, and hyphens")
	}
	if reservedNames[name] {
		return fmt.Errorf("that name is reserved")
	}
	return nil
}
```

- [ ] **Step 2: Update `engine/main.go` — wire up routes with middleware**

Replace the HTTP setup section of `main()` and add a `mux` with middleware:

```go
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
		log.Fatalf("Database connection failed: %s", err)
	}
	defer DB.Close()

	if err := RunMigrations(); err != nil {
		log.Fatalf("Migration failed: %s", err)
	}

	InitGitea()

	if err := InitTemplates(); err != nil {
		log.Fatalf("Template loading failed: %s", err)
	}

	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir("static"))))

	// Public routes (no auth)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/webhook", handleWebhook)
	mux.HandleFunc("/apps", handleListApps)

	// Auth routes (no CSRF for GET, CSRF for POST handled in handler)
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handleLogin(w, r)
		} else {
			handleLoginPage(w, r)
		}
	})
	mux.HandleFunc("/signup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handleSignup(w, r)
		} else {
			handleSignupPage(w, r)
		}
	})
	mux.HandleFunc("/logout", handleLogout)

	// Dashboard (auth required)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if CtxUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		handleDashboard(w, r)
	})

	// Project routes — handled by a sub-handler that checks ownership
	mux.HandleFunc("/projects/", func(w http.ResponseWriter, r *http.Request) {
		if CtxUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		routeProject(w, r)
	})

	// Partials (auth required)
	mux.HandleFunc("/partials/project-cards", func(w http.ResponseWriter, r *http.Request) {
		if CtxUser(r) == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handleProjectCardsPartial(w, r)
	})

	// Wrap everything with session middleware
	handler := SessionMiddleware(mux)

	log.Printf("KumbulaCloud Engine starting on :%s", ENGINE_PORT)
	log.Printf("   Domain: *.%s", DEPLOY_DOMAIN)
	log.Fatal(http.ListenAndServe(":"+ENGINE_PORT, handler))
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

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deploying",
		"app":    appName,
		"url":    fmt.Sprintf("http://%s.%s", appName, DEPLOY_DOMAIN),
	})

	go deploy(appName, cloneURL)
}

// routeProject dispatches /projects/* to the correct handler.
func routeProject(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// parts[0] = "projects"

	if len(parts) == 2 && parts[1] == "new" {
		if r.Method == http.MethodPost {
			handleCreateProject(w, r)
		} else {
			handleNewProjectPage(w, r)
		}
		return
	}

	// All other /projects/{name}[/...] routes need ownership check
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	projectName := parts[1]
	project, err := GetProjectByName(projectName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := CtxUser(r)
	if project.UserID != user.ID {
		http.NotFound(w, r)
		return
	}
	r = withProject(r, project)

	if len(parts) == 2 {
		handleProjectDetail(w, r)
		return
	}

	switch parts[2] {
	case "redeploy":
		if r.Method == http.MethodPost {
			handleRedeploy(w, r)
		}
	case "env":
		handleEnvVars(w, r)
	case "settings":
		handleProjectSettings(w, r)
	case "builds":
		if len(parts) == 5 && parts[4] == "stream" {
			handleBuildStream(w, r)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
```

- [ ] **Step 3: Verify it compiles**

This will fail because `handleDashboard`, `handleProjectCardsPartial`, `handleNewProjectPage`, `handleCreateProject`, `handleProjectDetail`, `handleRedeploy`, `handleEnvVars`, `handleProjectSettings`, and `handleBuildStream` don't exist yet. Create stubs:

Create `engine/handlers_stubs.go` (temporary):

```go
package main

import "net/http"

func handleDashboard(w http.ResponseWriter, r *http.Request)          {}
func handleProjectCardsPartial(w http.ResponseWriter, r *http.Request) {}
func handleNewProjectPage(w http.ResponseWriter, r *http.Request)     {}
func handleCreateProject(w http.ResponseWriter, r *http.Request)      {}
func handleProjectDetail(w http.ResponseWriter, r *http.Request)      {}
func handleRedeploy(w http.ResponseWriter, r *http.Request)           {}
func handleEnvVars(w http.ResponseWriter, r *http.Request)            {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
func handleBuildStream(w http.ResponseWriter, r *http.Request)        {}
```

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_auth.go engine/handlers_stubs.go engine/main.go
git commit -m "feat: add auth handlers (signup, login, logout) with Gitea provisioning"
```

---

## Task 7: Dashboard and project cards

Implement the dashboard page showing a grid of the user's projects, plus the htmx polling partial.

**Files:**
- Create: `engine/handlers_dashboard.go`
- Create: `engine/templates/dashboard/index.html`
- Create: `engine/templates/partials/project_cards.html`
- Modify: `engine/handlers_stubs.go` (remove `handleDashboard`, `handleProjectCardsPartial`)

- [ ] **Step 1: Create `engine/templates/dashboard/index.html`**

```html
{{define "content"}}
<div class="page-header">
    <h2>Your Projects</h2>
    <a href="/projects/new" role="button">New Project</a>
</div>
<div id="project-cards"
     hx-get="/partials/project-cards"
     hx-trigger="every 5s"
     hx-swap="innerHTML">
    {{template "project_cards" .}}
</div>
{{end}}
```

- [ ] **Step 2: Create `engine/templates/partials/project_cards.html`**

```html
{{define "project_cards"}}
{{if .Projects}}
<div class="project-grid">
    {{range .Projects}}
    <article class="project-card">
        <header>
            <a href="/projects/{{.Name}}"><strong>{{.Name}}</strong></a>
            <span class="{{.Status | statusClass}}">
                <span class="status-badge">{{.Status}}</span>
            </span>
        </header>
        <p><a href="{{.URL}}" target="_blank">{{.URL}}</a></p>
        <footer>
            <small>Updated {{.UpdatedAt.Format "Jan 02, 15:04"}}</small>
        </footer>
    </article>
    {{end}}
</div>
{{else}}
<article>
    <p>No projects yet. <a href="/projects/new">Create your first project</a> to get started.</p>
</article>
{{end}}
{{end}}
```

- [ ] **Step 3: Create `engine/handlers_dashboard.go`**

```go
package main

import (
	"log"
	"net/http"
)

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	projects, err := GetProjectsByUser(user.ID)
	if err != nil {
		log.Printf("GetProjectsByUser error: %v", err)
		projects = nil
	}

	RenderPage(w, r, "layout", map[string]interface{}{
		"Title":    "Dashboard",
		"Content":  "dashboard",
		"Projects": projects,
	})
}

func handleProjectCardsPartial(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	projects, err := GetProjectsByUser(user.ID)
	if err != nil {
		log.Printf("GetProjectsByUser error: %v", err)
		projects = nil
	}

	RenderPartial(w, r, "project_cards", map[string]interface{}{
		"Projects": projects,
	})
}
```

- [ ] **Step 4: Update `engine/handlers_stubs.go`**

Remove `handleDashboard` and `handleProjectCardsPartial` from stubs:

```go
package main

import "net/http"

func handleNewProjectPage(w http.ResponseWriter, r *http.Request)     {}
func handleCreateProject(w http.ResponseWriter, r *http.Request)      {}
func handleProjectDetail(w http.ResponseWriter, r *http.Request)      {}
func handleRedeploy(w http.ResponseWriter, r *http.Request)           {}
func handleEnvVars(w http.ResponseWriter, r *http.Request)            {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
func handleBuildStream(w http.ResponseWriter, r *http.Request)        {}
```

- [ ] **Step 5: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 6: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_dashboard.go engine/handlers_stubs.go engine/templates/dashboard/ engine/templates/partials/project_cards.html
git commit -m "feat: add dashboard with project cards grid and htmx polling"
```

---

## Task 8: Project creation

Implement the "New Project" page and handler. Creates a Gitea repo + webhook + DB record.

**Files:**
- Create: `engine/handlers_projects.go`
- Create: `engine/templates/projects/new.html`
- Modify: `engine/handlers_stubs.go` (remove project stubs)

- [ ] **Step 1: Create `engine/templates/projects/new.html`**

```html
{{define "content"}}
<article class="auth-card">
    <header>
        <h2>Create a new project</h2>
    </header>
    {{if .Error}}
    <p class="error-msg">{{.Error}}</p>
    {{end}}
    <form method="POST" action="/projects/new">
        <input type="hidden" name="_csrf" value="{{.CSRF}}">
        <label for="name">Project name</label>
        <input type="text" id="name" name="name" required autofocus
               pattern="[a-z][a-z0-9-]{1,48}[a-z0-9]"
               title="Lowercase letters, numbers, hyphens. 3-50 chars."
               placeholder="my-awesome-app">
        <small>This becomes your app URL: <code>http://&lt;name&gt;.kumbula.local</code></small>
        <button type="submit">Create project</button>
    </form>
</article>
{{end}}
```

- [ ] **Step 2: Create `engine/handlers_projects.go`**

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

func handleNewProjectPage(w http.ResponseWriter, r *http.Request) {
	RenderPage(w, r, "layout", map[string]interface{}{
		"Title":   "New Project",
		"Content": "new_project",
	})
}

func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	name := strings.TrimSpace(strings.ToLower(r.FormValue("name")))

	renderErr := func(msg string) {
		RenderPage(w, r, "layout", map[string]interface{}{
			"Title":   "New Project",
			"Content": "new_project",
			"Error":   msg,
		})
	}

	// Validate CSRF
	if r.FormValue("_csrf") != CtxCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Validate name
	if err := ValidateProjectName(name); err != nil {
		renderErr(err.Error())
		return
	}

	// Check uniqueness
	if _, err := GetProjectByName(name); err == nil {
		renderErr("A project with that name already exists.")
		return
	}

	// Reload user to get Gitea token
	user, err := GetUserByID(user.ID)
	if err != nil {
		renderErr("Something went wrong.")
		return
	}

	// Create Gitea repo
	_, err = GiteaCreateRepo(user.GiteaToken, name)
	if err != nil {
		log.Printf("GiteaCreateRepo error: %v", err)
		renderErr("Failed to create repository. Please try again.")
		return
	}

	// Add webhook
	webhookURL := fmt.Sprintf("http://%s:%s/webhook", getContainerIP(""), ENGINE_PORT)
	// Use host.docker.internal or the engine's host IP for webhook callback
	hostIP := getHostIP()
	if hostIP != "" {
		webhookURL = fmt.Sprintf("http://%s:%s/webhook", hostIP, ENGINE_PORT)
	}

	if err := GiteaAddWebhook(user.GiteaToken, user.Username, name, webhookURL); err != nil {
		log.Printf("GiteaAddWebhook error: %v", err)
		// Non-fatal: repo was created, user can add webhook manually
	}

	// Create project record
	url := fmt.Sprintf("http://%s.%s", name, DEPLOY_DOMAIN)
	giteaRepo := fmt.Sprintf("%s/%s", user.Username, name)

	project, err := CreateProject(user.ID, name, giteaRepo, url)
	if err != nil {
		log.Printf("CreateProject error: %v", err)
		renderErr("Failed to create project record.")
		return
	}

	log.Printf("Project created: %s by %s", project.Name, user.Username)
	http.Redirect(w, r, fmt.Sprintf("/projects/%s", name), http.StatusSeeOther)
}

// getHostIP returns the host machine's IP on the Docker bridge network.
func getHostIP() string {
	// Try to get the gateway IP of the kumbula network (host from container perspective)
	out, err := exec.Command("docker", "network", "inspect", "kumbula",
		"-f", "{{range .IPAM.Config}}{{.Gateway}}{{end}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Add the missing import to `handlers_projects.go`:

```go
import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
)
```

- [ ] **Step 3: Update `engine/handlers_stubs.go`**

Remove project-related stubs:

```go
package main

import "net/http"

func handleProjectDetail(w http.ResponseWriter, r *http.Request)      {}
func handleRedeploy(w http.ResponseWriter, r *http.Request)           {}
func handleEnvVars(w http.ResponseWriter, r *http.Request)            {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
func handleBuildStream(w http.ResponseWriter, r *http.Request)        {}
```

- [ ] **Step 4: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_projects.go engine/handlers_stubs.go engine/templates/projects/new.html
git commit -m "feat: add project creation with Gitea repo + webhook provisioning"
```

---

## Task 9: Project detail page

Implement the project detail view with overview, builds list, and tabbed navigation.

**Files:**
- Create: `engine/handlers_detail.go`
- Create: `engine/templates/projects/detail.html`
- Create: `engine/templates/partials/build_list.html`
- Modify: `engine/handlers_stubs.go` (remove `handleProjectDetail`)

- [ ] **Step 1: Create `engine/templates/projects/detail.html`**

```html
{{define "content"}}
<h2>{{.Project.Name}}</h2>

<div class="tabs">
    <a href="/projects/{{.Project.Name}}" class="{{if eq .Tab "overview"}}active{{end}}">Overview</a>
    <a href="/projects/{{.Project.Name}}?tab=builds" class="{{if eq .Tab "builds"}}active{{end}}">Builds</a>
    <a href="/projects/{{.Project.Name}}?tab=env" class="{{if eq .Tab "env"}}active{{end}}">Environment</a>
    <a href="/projects/{{.Project.Name}}?tab=settings" class="{{if eq .Tab "settings"}}active{{end}}">Settings</a>
</div>

{{if eq .Tab "overview"}}
<div class="grid">
    <article>
        <header>Status</header>
        <span class="{{.Project.Status | statusClass}}">
            <span class="status-badge">{{.Project.Status}}</span>
        </span>
    </article>
    <article>
        <header>URL</header>
        <a href="{{.Project.URL}}" target="_blank">{{.Project.URL}}</a>
    </article>
    <article>
        <header>Repository</header>
        <code>{{.Project.GiteaRepo}}</code>
    </article>
</div>

<h4>Deploy your code</h4>
<pre><code>git remote add kumbula http://{{.User.Username}}:***@gitea.kumbula.local/{{.Project.GiteaRepo}}.git
git push kumbula main</code></pre>

<form method="POST" action="/projects/{{.Project.Name}}/redeploy">
    <input type="hidden" name="_csrf" value="{{.CSRF}}">
    <button type="submit" class="outline">Redeploy latest</button>
</form>

{{else if eq .Tab "builds"}}
<div id="build-list">
    {{template "build_list" .}}
</div>

{{else if eq .Tab "env"}}
{{template "envvar_form" .}}

{{else if eq .Tab "settings"}}
{{template "settings_form" .}}
{{end}}
{{end}}
```

- [ ] **Step 2: Create `engine/templates/partials/build_list.html`**

```html
{{define "build_list"}}
{{if .Builds}}
<table>
    <thead>
        <tr>
            <th>Build</th>
            <th>Status</th>
            <th>Commit</th>
            <th>Started</th>
            <th>Duration</th>
        </tr>
    </thead>
    <tbody>
        {{range .Builds}}
        <tr>
            <td>#{{.ID}}</td>
            <td>
                <span class="{{.Status | statusClass}}">
                    <span class="status-badge">{{.Status}}</span>
                </span>
            </td>
            <td><code>{{if .CommitSHA}}{{slice .CommitSHA 0 7}}{{else}}-{{end}}</code></td>
            <td>{{.StartedAt.Format "Jan 02, 15:04"}}</td>
            <td>
                {{if .FinishedAt.Valid}}
                    {{.FinishedAt.Time.Sub .StartedAt | printf "%s"}}
                {{else}}
                    -
                {{end}}
            </td>
        </tr>
        {{if eq .Status "building"}}
        <tr>
            <td colspan="5">
                <div class="build-log"
                     hx-ext="sse"
                     sse-connect="/projects/{{$.Project.Name}}/builds/{{.ID}}/stream"
                     sse-swap="log"
                     hx-swap="beforeend">
                    <pre>{{.Log}}</pre>
                </div>
            </td>
        </tr>
        {{else if .Log}}
        <tr>
            <td colspan="5">
                <details>
                    <summary>View log</summary>
                    <div class="build-log"><pre>{{.Log}}</pre></div>
                </details>
            </td>
        </tr>
        {{end}}
        {{end}}
    </tbody>
</table>
{{else}}
<p>No builds yet. Push code to trigger your first build.</p>
{{end}}
{{end}}
```

- [ ] **Step 3: Create `engine/handlers_detail.go`**

```go
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
```

- [ ] **Step 4: Update stubs**

Remove `handleProjectDetail` from `engine/handlers_stubs.go`:

```go
package main

import "net/http"

func handleRedeploy(w http.ResponseWriter, r *http.Request)           {}
func handleEnvVars(w http.ResponseWriter, r *http.Request)            {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
func handleBuildStream(w http.ResponseWriter, r *http.Request)        {}
```

- [ ] **Step 5: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 6: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_detail.go engine/handlers_stubs.go engine/templates/projects/detail.html engine/templates/partials/build_list.html
git commit -m "feat: add project detail page with overview, builds, and tabbed navigation"
```

---

## Task 10: Build log streaming (SSE) and redeploy

Implement the SSE endpoint for live build log streaming and the redeploy button.

**Files:**
- Create: `engine/handlers_builds.go`
- Create: `engine/build_stream.go` (build log broadcaster)
- Modify: `engine/deploy.go` (integrate with builds table + broadcaster)
- Modify: `engine/handlers_stubs.go` (remove build stubs)

- [ ] **Step 1: Create `engine/build_stream.go`**

A simple in-memory pub/sub for build log lines. Each build ID maps to a channel list.

```go
package main

import (
	"sync"
)

// BuildBroadcaster fans out build log lines to connected SSE clients.
type BuildBroadcaster struct {
	mu       sync.RWMutex
	channels map[int][]chan string // buildID -> list of subscriber channels
}

var broadcaster = &BuildBroadcaster{
	channels: make(map[int][]chan string),
}

// Subscribe returns a channel that receives log lines for a build.
func (b *BuildBroadcaster) Subscribe(buildID int) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 64)
	b.channels[buildID] = append(b.channels[buildID], ch)
	return ch
}

// Unsubscribe removes a channel from a build's subscriber list.
// Does NOT close the channel — Finish handles that.
func (b *BuildBroadcaster) Unsubscribe(buildID int, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.channels[buildID]
	for i, s := range subs {
		if s == ch {
			b.channels[buildID] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

// Broadcast sends a log line to all subscribers of a build.
func (b *BuildBroadcaster) Broadcast(buildID int, line string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.channels[buildID] {
		select {
		case ch <- line:
		default:
			// Slow consumer, drop line
		}
	}
}

// Finish sends a final event and cleans up subscribers.
func (b *BuildBroadcaster) Finish(buildID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.channels[buildID] {
		select {
		case ch <- "__BUILD_DONE__":
		default:
		}
		close(ch)
	}
	delete(b.channels, buildID)
}
```

- [ ] **Step 2: Create `engine/handlers_builds.go`**

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func handleRedeploy(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)

	// Validate CSRF
	if r.FormValue("_csrf") != CtxCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	user, err := GetUserByID(project.UserID)
	if err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Build clone URL with credentials
	cloneURL := fmt.Sprintf("http://%s:%s@%s:3000/%s.git",
		user.Username, user.GiteaPassword,
		getContainerIP("gitea"), project.GiteaRepo)

	// Create build record
	build, err := CreateBuild(project.ID, "manual")
	if err != nil {
		log.Printf("CreateBuild error: %v", err)
		http.Error(w, "Failed to start build", http.StatusInternalServerError)
		return
	}

	UpdateProjectStatus(project.ID, "building", "")

	go deployWithBuild(project, build, cloneURL)

	http.Redirect(w, r, fmt.Sprintf("/projects/%s?tab=builds", project.Name), http.StatusSeeOther)
}

func handleBuildStream(w http.ResponseWriter, r *http.Request) {
	// Extract build ID from path: /projects/{name}/builds/{id}/stream
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}

	buildID, err := strconv.Atoi(parts[3])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Verify the build belongs to this project
	build, err := GetBuild(buildID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	project := CtxProject(r)
	if build.ProjectID != project.ID {
		http.NotFound(w, r)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := broadcaster.Subscribe(buildID)
	defer broadcaster.Unsubscribe(buildID, ch)

	// If build is already done, send the full log and close
	if build.Status != "building" {
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", build.Log)
		flusher.Flush()
		return
	}

	for line := range ch {
		if line == "__BUILD_DONE__" {
			fmt.Fprintf(w, "event: done\ndata: done\n\n")
			flusher.Flush()
			return
		}
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", line)
		flusher.Flush()
	}
}
```

- [ ] **Step 3: Add `deployWithBuild` to `engine/deploy.go`**

Add this function at the end of `deploy.go`. This is the build-aware version of `deploy()` that writes to the builds table and broadcasts log lines:

```go
// deployWithBuild is the DB-backed deploy that logs to a Build record and broadcasts via SSE.
func deployWithBuild(project *Project, build *Build, cloneURL string) {
	logLine := func(msg string) {
		log.Printf("  [%s] %s", project.Name, msg)
		AppendBuildLog(build.ID, msg+"\n")
		broadcaster.Broadcast(build.ID, msg)
	}

	logLine("Cloning repository...")
	cloneDir := filepath.Join(CLONE_BASE, project.Name)
	os.RemoveAll(cloneDir)

	cmd := exec.Command("git", "clone", "--depth=1", cloneURL, cloneDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		logLine(fmt.Sprintf("Clone failed: %s\n%s", err, string(out)))
		FinishBuild(build.ID, "failed")
		UpdateProjectStatus(project.ID, "failed", "")
		broadcaster.Finish(build.ID)
		return
	}
	logLine("Clone complete")

	logLine("Detecting language...")
	if err := ensureDockerfile(project.Name, cloneDir); err != nil {
		logLine(fmt.Sprintf("Dockerfile generation failed: %s", err))
		FinishBuild(build.ID, "failed")
		UpdateProjectStatus(project.ID, "failed", "")
		broadcaster.Finish(build.ID)
		return
	}

	// Provision database if not already done
	if !project.DatabaseURL.Valid || project.DatabaseURL.String == "" {
		logLine("Provisioning database...")
		dbURL, err := provisionDatabase(project.Name)
		if err != nil {
			logLine(fmt.Sprintf("Database provisioning failed (continuing): %s", err))
		} else {
			UpdateProjectDatabaseURL(project.ID, dbURL)
			project.DatabaseURL.String = dbURL
			project.DatabaseURL.Valid = true
			logLine("Database ready")
		}
	}

	imageName := fmt.Sprintf("kumbula/%s:latest", project.Name)
	logLine("Building Docker image...")

	if err := buildImage(project.Name, cloneDir, imageName); err != nil {
		logLine(fmt.Sprintf("Build failed: %s", err))
		FinishBuild(build.ID, "failed")
		UpdateProjectStatus(project.ID, "failed", "")
		broadcaster.Finish(build.ID)
		return
	}
	logLine("Image built successfully")

	stopContainer(project.Name)

	logLine("Starting container...")
	dbURL := ""
	if project.DatabaseURL.Valid {
		dbURL = project.DatabaseURL.String
	}

	// Load env vars
	envVars, _ := GetEnvVarsByProject(project.ID)
	containerID, err := runContainerWithEnv(project.Name, imageName, dbURL, envVars)
	if err != nil {
		logLine(fmt.Sprintf("Container start failed: %s", err))
		FinishBuild(build.ID, "failed")
		UpdateProjectStatus(project.ID, "failed", "")
		broadcaster.Finish(build.ID)
		return
	}

	logLine(fmt.Sprintf("Deployed successfully -> %s", project.URL))
	FinishBuild(build.ID, "success")
	UpdateProjectStatus(project.ID, "running", containerID[:12])
	broadcaster.Finish(build.ID)
}

// runContainerWithEnv is like runContainer but also injects user-defined env vars.
func runContainerWithEnv(appName, imageName, dbURL string, envVars []*EnvVar) (string, error) {
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
	for _, v := range envVars {
		env = append(env, fmt.Sprintf("%s=%s", v.Key, v.Value))
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
```

- [ ] **Step 4: Update stubs**

```go
package main

import "net/http"

func handleEnvVars(w http.ResponseWriter, r *http.Request)            {}
func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
```

- [ ] **Step 5: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 6: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/build_stream.go engine/handlers_builds.go engine/deploy.go engine/handlers_stubs.go
git commit -m "feat: add build log streaming via SSE and redeploy support"
```

---

## Task 11: Environment variables management

Implement the env var CRUD UI using htmx inline forms.

**Files:**
- Create: `engine/handlers_envvars.go`
- Create: `engine/templates/partials/envvar_form.html`
- Modify: `engine/handlers_stubs.go` (remove `handleEnvVars`)

- [ ] **Step 1: Create `engine/templates/partials/envvar_form.html`**

```html
{{define "envvar_form"}}
<div id="envvar-section">
    <h4>Environment Variables</h4>
    <p><small>Changes take effect on the next deploy. <code>DATABASE_URL</code> is managed automatically.</small></p>

    {{if .Project.DatabaseURL.Valid}}
    <div class="envvar-row">
        <input class="envvar-key" type="text" value="DATABASE_URL" disabled>
        <input class="envvar-value" type="text" value="(managed by KumbulaCloud)" disabled>
        <span></span>
    </div>
    {{end}}

    {{range .EnvVars}}
    <div class="envvar-row" id="envvar-{{.ID}}">
        <input class="envvar-key" type="text" value="{{.Key}}" disabled>
        <input class="envvar-value" type="text" value="{{.Value}}" disabled>
        <form method="POST" action="/projects/{{$.Project.Name}}/env?_method=DELETE&id={{.ID}}"
              hx-delete="/projects/{{$.Project.Name}}/env?id={{.ID}}"
              hx-target="#envvar-section"
              hx-swap="outerHTML"
              hx-headers='{"X-CSRF-Token": "{{$.CSRF}}"}'>
            <button type="submit" class="outline secondary btn-sm">Remove</button>
        </form>
    </div>
    {{end}}

    <form method="POST" action="/projects/{{.Project.Name}}/env"
          hx-post="/projects/{{.Project.Name}}/env"
          hx-target="#envvar-section"
          hx-swap="outerHTML"
          hx-headers='{"X-CSRF-Token": "{{.CSRF}}"}'>
        <div class="envvar-row">
            <input class="envvar-key" type="text" name="key" placeholder="KEY" required>
            <input class="envvar-value" type="text" name="value" placeholder="value" required>
            <button type="submit" class="btn-sm">Add</button>
        </div>
    </form>
</div>
{{end}}
```

- [ ] **Step 2: Create `engine/handlers_envvars.go`**

```go
package main

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

func handleEnvVars(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)

	// Check CSRF
	csrf := r.FormValue("_csrf")
	if csrf == "" {
		csrf = r.Header.Get("X-CSRF-Token")
	}
	if csrf != CtxCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Query().Get("_method") != "DELETE":
		// Add env var
		key := strings.TrimSpace(strings.ToUpper(r.FormValue("key")))
		value := r.FormValue("value")

		if key == "" {
			http.Error(w, "Key is required", http.StatusBadRequest)
			return
		}
		if key == "DATABASE_URL" || key == "PORT" || key == "APP_NAME" || key == "APP_URL" {
			http.Error(w, "That variable is managed by KumbulaCloud", http.StatusBadRequest)
			return
		}

		if err := SetEnvVar(project.ID, key, value); err != nil {
			log.Printf("SetEnvVar error: %v", err)
			http.Error(w, "Failed to set variable", http.StatusInternalServerError)
			return
		}

	case r.Method == http.MethodDelete || r.URL.Query().Get("_method") == "DELETE":
		// Delete env var
		idStr := r.URL.Query().Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}
		if err := DeleteEnvVar(id); err != nil {
			log.Printf("DeleteEnvVar error: %v", err)
			http.Error(w, "Failed to delete variable", http.StatusInternalServerError)
			return
		}
	}

	// Re-render the env var section
	vars, err := GetEnvVarsByProject(project.ID)
	if err != nil {
		log.Printf("GetEnvVarsByProject error: %v", err)
	}

	RenderPartial(w, r, "envvar_form", map[string]interface{}{
		"Project": project,
		"EnvVars": vars,
	})
}
```

- [ ] **Step 3: Update stubs**

```go
package main

import "net/http"

func handleProjectSettings(w http.ResponseWriter, r *http.Request)    {}
```

- [ ] **Step 4: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_envvars.go engine/templates/partials/envvar_form.html engine/handlers_stubs.go
git commit -m "feat: add env var management with htmx inline CRUD"
```

---

## Task 12: Project settings — rename and destroy

Implement the settings tab with rename and destroy actions.

**Files:**
- Create: `engine/handlers_settings.go`
- Create: `engine/templates/partials/settings_form.html`
- Delete: `engine/handlers_stubs.go` (all stubs implemented)

- [ ] **Step 1: Create `engine/templates/partials/settings_form.html`**

```html
{{define "settings_form"}}
<h4>Project Settings</h4>

{{if .SettingsError}}
<p class="error-msg">{{.SettingsError}}</p>
{{end}}
{{if .SettingsSuccess}}
<p style="color: var(--pico-ins-color);">{{.SettingsSuccess}}</p>
{{end}}

<div class="danger-zone">
    <h4>Danger Zone</h4>

    <p>Permanently delete this project. This will:</p>
    <ul>
        <li>Stop and remove the running container</li>
        <li>Drop the provisioned database</li>
        <li>Delete the Gitea repository</li>
        <li>Remove all build history and environment variables</li>
    </ul>

    <form method="POST" action="/projects/{{.Project.Name}}/settings?action=destroy"
          onsubmit="return confirm('Are you sure you want to destroy {{.Project.Name}}? This cannot be undone.')">
        <input type="hidden" name="_csrf" value="{{.CSRF}}">
        <button type="submit" class="outline" style="--pico-color: var(--pico-del-color); --pico-border-color: var(--pico-del-color);">
            Destroy {{.Project.Name}}
        </button>
    </form>
</div>
{{end}}
```

- [ ] **Step 2: Create `engine/handlers_settings.go`**

```go
package main

import (
	"fmt"
	"log"
	"net/http"
)

func handleProjectSettings(w http.ResponseWriter, r *http.Request) {
	project := CtxProject(r)

	if r.Method != http.MethodPost {
		// GET — just render the settings tab on the detail page
		handleProjectDetail(w, r)
		return
	}

	// Validate CSRF
	if r.FormValue("_csrf") != CtxCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	action := r.URL.Query().Get("action")

	switch action {
	case "destroy":
		user, err := GetUserByID(project.UserID)
		if err != nil {
			log.Printf("GetUserByID error: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		// Stop container
		stopContainer(project.Name)

		// Drop database
		dropDatabase(project.Name)

		// Delete Gitea repo
		if err := GiteaDeleteRepo(user.GiteaToken, user.Username, project.Name); err != nil {
			log.Printf("GiteaDeleteRepo error: %v", err)
		}

		// Delete env vars, builds, then project
		DB.Exec("DELETE FROM project_env_vars WHERE project_id = $1", project.ID)
		DB.Exec("DELETE FROM builds WHERE project_id = $1", project.ID)
		if err := DeleteProject(project.ID); err != nil {
			log.Printf("DeleteProject error: %v", err)
		}

		log.Printf("Project destroyed: %s", project.Name)
		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		http.Redirect(w, r, fmt.Sprintf("/projects/%s?tab=settings", project.Name), http.StatusSeeOther)
	}
}

// dropDatabase drops the app's database and role.
func dropDatabase(appName string) {
	dbName := fmt.Sprintf("app_%s", replaceHyphens(appName))
	dbUser := dbName

	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)

	db, err := openRawDB(connStr)
	if err != nil {
		log.Printf("dropDatabase connect error: %v", err)
		return
	}
	defer db.Close()

	db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName))
	db.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", dbUser))
}

func replaceHyphens(s string) string {
	result := ""
	for _, c := range s {
		if c == '-' {
			result += "_"
		} else {
			result += string(c)
		}
	}
	return result
}

func openRawDB(connStr string) (*sql.DB, error) {
	return sql.Open("postgres", connStr)
}
```

Add the missing import:

```go
import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
)
```

- [ ] **Step 3: Delete `engine/handlers_stubs.go`**

Run: `rm /home/kijani/kumbula-poc/engine/handlers_stubs.go`

- [ ] **Step 4: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/handlers_settings.go engine/templates/partials/settings_form.html
git rm engine/handlers_stubs.go
git commit -m "feat: add project settings with destroy action"
```

---

## Task 13: Update webhook handler for multi-user

Update the webhook handler to look up the project in the database and use the build-aware deploy path.

**Files:**
- Modify: `engine/main.go` (update `handleWebhook`)

- [ ] **Step 1: Update `handleWebhook` in `engine/main.go`**

Replace the existing `handleWebhook` function:

```go
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

	// Look up project in DB
	project, err := GetProjectByName(appName)
	if err != nil {
		// Fallback to legacy in-memory deploy for backward compatibility
		log.Printf("Project %s not found in DB, using legacy deploy", appName)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "deploying",
			"app":    appName,
		})
		go deploy(appName, cloneURL)
		return
	}

	// Extract commit SHA from ref
	commitSHA := ""
	if payload.Ref != "" && len(payload.Ref) >= 7 {
		commitSHA = payload.Ref
	}

	// Create build record
	build, err := CreateBuild(project.ID, commitSHA)
	if err != nil {
		log.Printf("CreateBuild error: %v", err)
		http.Error(w, "Failed to create build", http.StatusInternalServerError)
		return
	}

	UpdateProjectStatus(project.ID, "building", "")

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "deploying",
		"app":      appName,
		"url":      project.URL,
		"build_id": fmt.Sprintf("%d", build.ID),
	})

	go deployWithBuild(project, build, cloneURL)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/main.go
git commit -m "feat: update webhook handler to use DB-backed projects and builds"
```

---

## Task 14: Template rendering fix — nested template dispatch

Go's `html/template` doesn't natively support choosing a content template by name from a variable. Fix the layout template and `RenderPage` to dispatch correctly.

**Files:**
- Modify: `engine/templates.go` (update `RenderPage` to compose layout + content)
- Modify: `engine/templates/layout.html` (use `{{block}}` pattern)

- [ ] **Step 1: Update `engine/templates.go`**

Replace `InitTemplates` and `RenderPage` with a pattern that clones and composes templates:

```go
package main

import (
	"html/template"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	layoutTemplate   *template.Template
	contentTemplates map[string]*template.Template
)

var templateFuncs = template.FuncMap{
	"upper": strings.ToUpper,
	"statusClass": func(status string) string {
		switch status {
		case "running":
			return "status-running"
		case "building":
			return "status-building"
		case "failed", "build_failed", "clone_failed", "run_failed", "detect_failed":
			return "status-failed"
		default:
			return "status-default"
		}
	},
}

// InitTemplates loads layout and content templates.
func InitTemplates() error {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	tmplDir := filepath.Join(baseDir, "templates")

	contentTemplates = make(map[string]*template.Template)

	layoutPath := filepath.Join(tmplDir, "layout.html")

	// Map of content name -> template file path
	pages := map[string]string{
		"login":       filepath.Join(tmplDir, "auth", "login.html"),
		"signup":      filepath.Join(tmplDir, "auth", "signup.html"),
		"dashboard":   filepath.Join(tmplDir, "dashboard", "index.html"),
		"new_project": filepath.Join(tmplDir, "projects", "new.html"),
		"detail":      filepath.Join(tmplDir, "projects", "detail.html"),
	}

	// Partials that may be embedded in pages
	partialFiles, _ := filepath.Glob(filepath.Join(tmplDir, "partials", "*.html"))

	for name, pagePath := range pages {
		files := []string{layoutPath, pagePath}
		files = append(files, partialFiles...)
		t, err := template.New("").Funcs(templateFuncs).ParseFiles(files...)
		if err != nil {
			return err
		}
		contentTemplates[name] = t
	}

	// Also parse partials standalone for htmx responses
	for _, pf := range partialFiles {
		base := strings.TrimSuffix(filepath.Base(pf), ".html")
		t, err := template.New("").Funcs(templateFuncs).ParseFiles(pf)
		if err != nil {
			return err
		}
		contentTemplates["partial:"+base] = t
	}

	return nil
}

// RenderPage renders a named content template inside the layout.
func RenderPage(w http.ResponseWriter, r *http.Request, _ string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["User"] = CtxUser(r)
	data["CSRF"] = CtxCSRF(r)

	contentName, _ := data["Content"].(string)
	if contentName == "" {
		contentName = "dashboard"
	}

	t, ok := contentTemplates[contentName]
	if !ok {
		http.Error(w, "Template not found: "+contentName, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// RenderPartial renders a standalone partial template (no layout).
func RenderPartial(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["User"] = CtxUser(r)
	data["CSRF"] = CtxCSRF(r)

	t, ok := contentTemplates["partial:"+name]
	if !ok {
		http.Error(w, "Partial not found: "+name, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
cd /home/kijani/kumbula-poc
git add engine/templates.go
git commit -m "fix: template rendering — proper layout+content composition with partials"
```

---

## Task 15: Traefik label for dashboard and final wiring

Add the dashboard to the `docker-compose.yml` as a Traefik route (the engine runs on the host, so we use a file provider or add a manual rule). Also update `.gitignore` and do a final compile check.

**Files:**
- Modify: `docker-compose.yml` (add Traefik route for dashboard)
- Modify: `engine/.gitignore` or root `.gitignore` (ignore binary, add static assets)

- [ ] **Step 1: Add Traefik file provider for the dashboard**

The engine runs on the host (not in Docker), so we need a Traefik file provider to route `dashboard.kumbula.local` to it. Create a Traefik dynamic config:

Create `engine/traefik-dashboard.yml`:

```yaml
http:
  routers:
    dashboard-app:
      rule: "Host(`dashboard.kumbula.local`)"
      service: dashboard-app
      entryPoints:
        - web
  services:
    dashboard-app:
      loadBalancer:
        servers:
          - url: "http://host.docker.internal:9000"
```

- [ ] **Step 2: Mount the file provider in docker-compose.yml**

Add to the Traefik service in `docker-compose.yml`:

Under `command:`, add:
```yaml
      - "--providers.file.filename=/etc/traefik/dynamic.yml"
      - "--providers.file.watch=true"
```

Under `volumes:`, add:
```yaml
      - ./engine/traefik-dashboard.yml:/etc/traefik/dynamic.yml:ro
```

Also add `extra_hosts` so `host.docker.internal` resolves:
```yaml
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

- [ ] **Step 3: Update `.gitignore`**

Add to `.gitignore`:
```
engine/kumbula-engine
engine/engine.log
```

- [ ] **Step 4: Final compile check**

Run: `cd /home/kijani/kumbula-poc/engine && go build -o kumbula-engine .`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
cd /home/kijani/kumbula-poc
git add docker-compose.yml engine/traefik-dashboard.yml .gitignore
git commit -m "feat: add Traefik routing for dashboard and finalize project config"
```

---

## Task 16: End-to-end smoke test

Verify the full flow works: start services, sign up, create project, check dashboard.

**Files:**
- Create: `test-dashboard.sh` (manual smoke test script)

- [ ] **Step 1: Create `test-dashboard.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "=== KumbulaCloud Dashboard Smoke Test ==="

ENGINE="http://localhost:9000"

echo "1. Health check..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/health")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Engine not healthy (got $STATUS)"
    exit 1
fi
echo "   PASS: Engine healthy"

echo "2. Login page loads..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/login")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Login page returned $STATUS"
    exit 1
fi
echo "   PASS: Login page loads"

echo "3. Signup page loads..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/signup")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Signup page returned $STATUS"
    exit 1
fi
echo "   PASS: Signup page loads"

echo "4. Dashboard redirects to login when not authenticated..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -L --max-redirs 0 "$ENGINE/")
if [ "$STATUS" != "303" ]; then
    echo "FAIL: Dashboard should redirect (got $STATUS)"
    exit 1
fi
echo "   PASS: Dashboard redirects to login"

echo ""
echo "=== All smoke tests passed ==="
echo ""
echo "Manual test steps:"
echo "  1. Open http://dashboard.kumbula.local"
echo "  2. Sign up with a new account"
echo "  3. Create a project"
echo "  4. Push code and watch the build log"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x /home/kijani/kumbula-poc/test-dashboard.sh`

- [ ] **Step 3: Commit**

```bash
cd /home/kijani/kumbula-poc
git add test-dashboard.sh
git commit -m "test: add dashboard smoke test script"
```
