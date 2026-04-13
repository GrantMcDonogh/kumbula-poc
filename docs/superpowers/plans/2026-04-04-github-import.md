# GitHub Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow users to import a GitHub repo into a Kumbula project — clone from GitHub, push to Gitea, deploy automatically.

**Architecture:** A new `github.go` file handles the clone-and-push logic. A user settings page stores the GitHub PAT. The project detail Overview tab gets an "Import from GitHub" card. On submit, the engine clones the repo, pushes to Gitea, and the existing webhook triggers a deploy.

**Tech Stack:** Go, `os/exec` (git CLI), PostgreSQL, Go html/template, htmx

**Spec:** `docs/superpowers/specs/2026-04-04-github-import-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `engine/github.go` | `GitHubCloneAndPush()` — mirror clone from GitHub, push to Gitea |
| Create | `engine/templates/settings.html` | User settings page template (GitHub token management) |
| Modify | `engine/migrate.go` | Add `github_token` column to `users`, `github_url` column to `projects` |
| Modify | `engine/models_user.go` | Add `GithubToken` field to `User` struct, update all scans/queries, add `UpdateGithubToken()` |
| Modify | `engine/models_project.go` | Add `GithubURL` field to `Project` struct, update all scans/queries, add `UpdateProjectGithubURL()` |
| Modify | `engine/main.go` | Add `/settings` route, add `import` case to `routeProject` |
| Modify | `engine/handlers_projects.go` | Add `handleGitHubImport()` handler |
| Modify | `engine/handlers_settings.go` | Add `handleUserSettings()` and `handleUserSettingsSave()` |
| Modify | `engine/templates.go` | Register `settings` in `shellPages` |
| Modify | `engine/templates/layout_shell.html` | Add "Settings" link to sidebar footer |
| Modify | `engine/templates/partials/tab_overview.html` | Add "Import from GitHub" card |

---

### Task 1: Database Schema — Add `github_token` and `github_url` Columns

**Files:**
- Modify: `engine/migrate.go:6-60`

- [ ] **Step 1: Add migration statements**

In `engine/migrate.go`, add these two `ALTER TABLE` statements to the end of the `tables` slice (before the closing `}`), after the `sessions` table DDL:

```go
		// --- GitHub import columns ---
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS github_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS github_url TEXT NOT NULL DEFAULT ''`,
```

Insert these after line 49 (`}`,` — the sessions table closing), before the closing `}` of the `tables` slice.

- [ ] **Step 2: Verify migration runs**

Run:
```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

Then verify the columns exist by starting the engine briefly or running:
```bash
PGPASSWORD=kumbula_secret_2024 psql -h localhost -U kumbula_admin -d kumbula_system -c "\d users" | grep github_token
PGPASSWORD=kumbula_secret_2024 psql -h localhost -U kumbula_admin -d kumbula_system -c "\d projects" | grep github_url
```

Expected: columns appear (they may already exist from a previous run — `IF NOT EXISTS` handles that).

- [ ] **Step 3: Commit**

```bash
git add engine/migrate.go
git commit -m "feat(db): add github_token and github_url columns for import feature"
```

---

### Task 2: Update User Model — Add `GithubToken` Field

**Files:**
- Modify: `engine/models_user.go`

- [ ] **Step 1: Add field to User struct**

In `engine/models_user.go`, add `GithubToken` to the `User` struct (after `GiteaToken`):

```go
type User struct {
	ID            int
	Username      string
	Email         string
	PasswordHash  string
	GiteaPassword string
	GiteaToken    string
	GithubToken   string
	CreatedAt     time.Time
}
```

- [ ] **Step 2: Update CreateUser query and scan**

Update the `CreateUser` function's RETURNING clause and Scan to include `github_token`:

```go
func CreateUser(username, email, password, giteaPassword string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	u := &User{}
	err = DB.QueryRow(
		`INSERT INTO users (username, email, password_hash, gitea_password)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at`,
		username, email, string(hash), giteaPassword,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}
```

- [ ] **Step 3: Update GetUserByUsername query and scan**

```go
func GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at
		 FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}
```

- [ ] **Step 4: Update GetUserByID query and scan**

```go
func GetUserByID(id int) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}
```

- [ ] **Step 5: Add UpdateGithubToken function**

Add after the existing `UpdateGiteaToken` function:

```go
func UpdateGithubToken(userID int, token string) error {
	_, err := DB.Exec(`UPDATE users SET github_token = $1 WHERE id = $2`, token, userID)
	return err
}
```

- [ ] **Step 6: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 7: Commit**

```bash
git add engine/models_user.go
git commit -m "feat(models): add GithubToken field to User"
```

---

### Task 3: Update Project Model — Add `GithubURL` Field

**Files:**
- Modify: `engine/models_project.go`

- [ ] **Step 1: Add field to Project struct**

In `engine/models_project.go`, add `GithubURL` after `DatabaseURL`:

```go
type Project struct {
	ID          int
	UserID      int
	Name        string
	GiteaRepo   string
	ContainerID sql.NullString
	Status      string
	URL         string
	DatabaseURL sql.NullString
	GithubURL   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
```

- [ ] **Step 2: Update CreateProject**

```go
func CreateProject(userID int, name, giteaRepo, url string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`INSERT INTO projects (user_id, name, gitea_repo, url)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, gitea_repo, container_id, status, url, database_url, github_url, created_at, updated_at`,
		userID, name, giteaRepo, url,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.GithubURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}
```

- [ ] **Step 3: Update GetProjectsByUser**

```go
func GetProjectsByUser(userID int) ([]*Project, error) {
	rows, err := DB.Query(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, github_url, created_at, updated_at
		 FROM projects WHERE user_id = $1 ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.GithubURL, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
```

- [ ] **Step 4: Update GetProjectByName**

```go
func GetProjectByName(name string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, github_url, created_at, updated_at
		 FROM projects WHERE name = $1`, name,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.GithubURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}
```

- [ ] **Step 5: Add UpdateProjectGithubURL function**

Add after `UpdateProjectDatabaseURL`:

```go
func UpdateProjectGithubURL(projectID int, githubURL string) error {
	_, err := DB.Exec(
		`UPDATE projects SET github_url = $1, updated_at = now() WHERE id = $2`,
		githubURL, projectID,
	)
	return err
}
```

- [ ] **Step 6: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 7: Commit**

```bash
git add engine/models_project.go
git commit -m "feat(models): add GithubURL field to Project"
```

---

### Task 4: GitHub Clone and Push Logic

**Files:**
- Create: `engine/github.go`

- [ ] **Step 1: Create `engine/github.go`**

```go
package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// GitHubCloneAndPush clones a GitHub repo (mirror) and pushes all refs to Gitea.
// githubToken may be empty for public repos.
func GitHubCloneAndPush(githubURL, githubToken, giteaRemoteURL, giteaUsername, giteaPassword string) error {
	// Build authenticated GitHub clone URL
	cloneURL := githubURL
	if !strings.HasSuffix(cloneURL, ".git") {
		cloneURL += ".git"
	}
	if githubToken != "" {
		parsed, err := url.Parse(cloneURL)
		if err != nil {
			return fmt.Errorf("parse github URL: %w", err)
		}
		parsed.User = url.UserPassword("x-access-token", githubToken)
		cloneURL = parsed.String()
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "kumbula-github-import-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone mirror from GitHub
	log.Printf("  [github-import] Cloning %s ...", githubURL)
	cmd := exec.Command("git", "clone", "--mirror", cloneURL, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}

	// Build authenticated Gitea push URL
	parsed, err := url.Parse(giteaRemoteURL)
	if err != nil {
		return fmt.Errorf("parse gitea URL: %w", err)
	}
	parsed.User = url.UserPassword(giteaUsername, giteaPassword)
	pushURL := parsed.String()

	// Set push remote and push mirror
	log.Printf("  [github-import] Pushing to Gitea ...")
	setRemote := exec.Command("git", "-C", tmpDir, "remote", "set-url", "origin", pushURL)
	if out, err := setRemote.CombinedOutput(); err != nil {
		return fmt.Errorf("set remote failed: %w\n%s", err, string(out))
	}

	push := exec.Command("git", "-C", tmpDir, "push", "--mirror")
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %w\n%s", err, string(out))
	}

	log.Printf("  [github-import] Import complete.")
	return nil
}
```

- [ ] **Step 2: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 3: Commit**

```bash
git add engine/github.go
git commit -m "feat: add GitHubCloneAndPush for importing repos"
```

---

### Task 5: Import Handler

**Files:**
- Modify: `engine/handlers_projects.go`
- Modify: `engine/main.go:150-168` (routeProject switch)

- [ ] **Step 1: Add `handleGitHubImport` to `engine/handlers_projects.go`**

Append this function at the end of the file:

```go
// handleGitHubImport clones a GitHub repo and pushes it to Gitea, triggering a deploy.
func handleGitHubImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// CSRF check
	csrf := r.FormValue("_csrf")
	if csrf == "" {
		csrf = r.Header.Get("X-CSRF-Token")
	}
	if csrf != CtxCSRF(r) {
		http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
		return
	}

	project := CtxProject(r)
	user := CtxUser(r)

	githubURL := strings.TrimSpace(r.FormValue("github_url"))

	// Validate URL
	if !strings.HasPrefix(githubURL, "https://github.com/") {
		RenderShell(w, r, "detail", importErrorData(r, project, "Invalid URL. Must start with https://github.com/"))
		return
	}

	// Save github_url on the project
	if err := UpdateProjectGithubURL(project.ID, githubURL); err != nil {
		log.Printf("import: failed to save github_url for %s: %v", project.Name, err)
		RenderShell(w, r, "detail", importErrorData(r, project, "Failed to save import URL. Please try again."))
		return
	}

	// Load fresh user for tokens
	freshUser, err := GetUserByID(user.ID)
	if err != nil {
		log.Printf("import: failed to reload user %d: %v", user.ID, err)
		UpdateProjectGithubURL(project.ID, "")
		RenderShell(w, r, "detail", importErrorData(r, project, "Internal error. Please try again."))
		return
	}

	// Build Gitea remote URL
	giteaRemoteURL := fmt.Sprintf("http://%s:3000/%s.git", getContainerIP("gitea"), project.GiteaRepo)

	// Clone and push
	log.Printf("import: importing %s -> %s for user %s", githubURL, project.Name, freshUser.Username)
	if err := GitHubCloneAndPush(githubURL, freshUser.GithubToken, giteaRemoteURL, freshUser.Username, freshUser.GiteaPassword); err != nil {
		log.Printf("import: GitHubCloneAndPush failed for %s: %v", project.Name, err)
		UpdateProjectGithubURL(project.ID, "")
		RenderShell(w, r, "detail", importErrorData(r, project, "Could not import repository. Check the URL and ensure your GitHub token is set for private repos."))
		return
	}

	log.Printf("import: %s imported successfully from %s", project.Name, githubURL)
	http.Redirect(w, r, "/projects/"+project.Name, http.StatusSeeOther)
}

// importErrorData builds the template data for rendering the detail page with an import error.
func importErrorData(r *http.Request, project *Project, errMsg string) map[string]interface{} {
	builds, _ := GetBuildsByProject(project.ID)
	envVars, _ := GetEnvVarsByProject(project.ID)
	data := map[string]interface{}{
		"Title":         project.Name,
		"Project":       project,
		"Tab":           "overview",
		"ActiveProject": project.Name,
		"ImportError":   errMsg,
		"Builds":        builds,
		"BuildCount":    len(builds),
		"EnvVarCount":   len(envVars),
	}
	if len(envVars) > 3 {
		data["EnvVarsPreview"] = envVars[:3]
	} else {
		data["EnvVarsPreview"] = envVars
	}
	if len(builds) > 0 {
		data["LastBuild"] = builds[0]
	}
	return data
}
```

- [ ] **Step 2: Add `"import"` case to `routeProject` in `engine/main.go`**

In `engine/main.go`, in the `routeProject` function's switch on `action` (around line 152), add the `"import"` case before the `default`:

```go
	case "import":
		handleGitHubImport(w, r)
```

- [ ] **Step 3: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 4: Commit**

```bash
git add engine/handlers_projects.go engine/main.go
git commit -m "feat: add GitHub import handler and route"
```

---

### Task 6: User Settings Page — Handler and Template

**Files:**
- Modify: `engine/handlers_settings.go`
- Create: `engine/templates/settings.html`
- Modify: `engine/templates.go:93-97` (shellPages map)
- Modify: `engine/main.go` (add /settings route)

- [ ] **Step 1: Add settings handlers to `engine/handlers_settings.go`**

Add these two functions at the top of the file, before `handleProjectSettings`:

```go
// handleUserSettings renders the user settings page.
func handleUserSettings(w http.ResponseWriter, r *http.Request) {
	user := CtxUser(r)
	freshUser, err := GetUserByID(user.ID)
	if err != nil {
		log.Printf("settings: failed to reload user %d: %v", user.ID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Title":          "Settings",
		"HasGithubToken": freshUser.GithubToken != "",
	}
	RenderShell(w, r, "settings", data)
}

// handleUserSettingsSave processes the settings form submission.
func handleUserSettingsSave(w http.ResponseWriter, r *http.Request) {
	// CSRF check
	csrf := r.FormValue("_csrf")
	if csrf != CtxCSRF(r) {
		http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
		return
	}

	user := CtxUser(r)
	action := r.FormValue("action")

	switch action {
	case "save_github_token":
		token := strings.TrimSpace(r.FormValue("github_token"))
		if err := UpdateGithubToken(user.ID, token); err != nil {
			log.Printf("settings: failed to update GitHub token for user %d: %v", user.ID, err)
			RenderShell(w, r, "settings", map[string]interface{}{
				"Title":          "Settings",
				"HasGithubToken": false,
				"Error":          "Failed to save token. Please try again.",
			})
			return
		}
		RenderShell(w, r, "settings", map[string]interface{}{
			"Title":          "Settings",
			"HasGithubToken": token != "",
			"Success":        "GitHub token saved.",
		})
	case "remove_github_token":
		if err := UpdateGithubToken(user.ID, ""); err != nil {
			log.Printf("settings: failed to remove GitHub token for user %d: %v", user.ID, err)
		}
		RenderShell(w, r, "settings", map[string]interface{}{
			"Title":          "Settings",
			"HasGithubToken": false,
			"Success":        "GitHub token removed.",
		})
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}
```

- [ ] **Step 2: Create `engine/templates/settings.html`**

```html
{{define "content"}}
<div class="topbar">
    <div class="topbar-breadcrumb">
        <span class="crumb-current">Settings</span>
    </div>
    <div class="topbar-actions"></div>
</div>

<div class="content">
    <div style="max-width:640px;">
        {{if .Error}}
        <div class="error-msg">{{.Error}}</div>
        {{end}}
        {{if .Success}}
        <div class="success-msg">{{.Success}}</div>
        {{end}}

        <div class="card">
            <div class="card-header">
                <span class="card-title">GitHub Integration</span>
                {{if .HasGithubToken}}
                <span class="card-badge teal">Connected</span>
                {{end}}
            </div>

            <p style="font-size:12px;color:var(--text-tertiary);margin-bottom:16px;">
                Add a Personal Access Token to import private repositories from GitHub.<br>
                Generate one at
                <a href="https://github.com/settings/tokens/new?scopes=repo&description=KumbulaCloud" target="_blank" style="color:var(--accent);">
                    GitHub &rarr; Settings &rarr; Developer settings &rarr; Personal access tokens
                </a>.
                Needs <code>repo</code> scope for private repos.
            </p>

            {{if .HasGithubToken}}
            <div class="info-row" style="margin-bottom:16px;">
                <span class="info-key">Status</span>
                <span class="info-val accent">Token saved</span>
            </div>
            <form method="POST" action="/settings">
                <input type="hidden" name="_csrf" value="{{.CSRF}}">
                <input type="hidden" name="action" value="remove_github_token">
                <button type="submit" class="btn-sm danger">Remove token</button>
            </form>
            {{else}}
            <form method="POST" action="/settings">
                <input type="hidden" name="_csrf" value="{{.CSRF}}">
                <input type="hidden" name="action" value="save_github_token">
                <div class="env-add-row">
                    <input type="password" name="github_token" placeholder="ghp_xxxxxxxxxxxxxxxxxxxx" required style="flex:1;">
                    <button type="submit" class="btn-sm primary">Save token</button>
                </div>
            </form>
            {{end}}
        </div>
    </div>
</div>
{{end}}
```

- [ ] **Step 3: Register settings template in `engine/templates.go`**

In `engine/templates.go`, add `"settings"` to the `shellPages` map (around line 93-97):

```go
	shellPages := map[string]string{
		"dashboard":   "dashboard/index.html",
		"new_project": "projects/new.html",
		"detail":      "projects/detail.html",
		"settings":    "settings.html",
	}
```

- [ ] **Step 4: Add /settings route in `engine/main.go`**

Add these lines after the `/logout` route (around line 97) and before the authenticated routes section:

```go
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
```

- [ ] **Step 5: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 6: Commit**

```bash
git add engine/handlers_settings.go engine/templates/settings.html engine/templates.go engine/main.go
git commit -m "feat: add user settings page with GitHub token management"
```

---

### Task 7: Sidebar Settings Link

**Files:**
- Modify: `engine/templates/layout_shell.html:52-62`

- [ ] **Step 1: Add Settings link to sidebar footer**

In `engine/templates/layout_shell.html`, replace the sidebar footer section (lines 52-62) with:

```html
        <div class="sidebar-footer">
            <div class="user-avatar">{{initials .User.Username}}</div>
            <div class="user-info">
                <div class="user-name">{{.User.Username}}</div>
                <div class="user-plan">{{len .SidebarProjects}} project{{if ne (len .SidebarProjects) 1}}s{{end}}</div>
            </div>
            <a href="/settings" class="sidebar-settings-btn" title="Settings">&#9881;</a>
            <form method="POST" action="/logout" style="margin:0">
                <input type="hidden" name="_csrf" value="{{.CSRF}}">
                <button type="submit" class="logout-btn">Logout</button>
            </form>
        </div>
```

The only change is adding the `<a href="/settings" ...>` line before the logout form.

- [ ] **Step 2: Add CSS for the settings button**

In `engine/static/style.css`, add at the end of the file:

```css
/* Settings button in sidebar footer */
.sidebar-settings-btn {
    color: var(--text-tertiary);
    text-decoration: none;
    font-size: 16px;
    padding: 4px;
    border-radius: 4px;
    transition: color 0.15s;
}
.sidebar-settings-btn:hover {
    color: var(--text-primary);
}

/* Success message */
.success-msg {
    background: rgba(52, 211, 153, 0.1);
    border: 1px solid rgba(52, 211, 153, 0.3);
    color: #34d399;
    padding: 10px 14px;
    border-radius: 8px;
    font-size: 12.5px;
    margin-bottom: 16px;
}
```

- [ ] **Step 3: Build and verify visually**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

Start the engine and visit `http://dashboard.kumbula.local/settings` to verify the page renders. Check the sidebar footer has the gear icon.

- [ ] **Step 4: Commit**

```bash
git add engine/templates/layout_shell.html engine/static/style.css
git commit -m "feat: add settings link to sidebar footer"
```

---

### Task 8: Import Card on Overview Tab

**Files:**
- Modify: `engine/templates/partials/tab_overview.html`
- Modify: `engine/handlers_detail.go`

- [ ] **Step 1: Pass `HasGithubToken` to the overview tab**

In `engine/handlers_detail.go`, inside the `case "overview":` block (after line 38), add:

```go
		freshUser, _ := GetUserByID(CtxUser(r).ID)
		if freshUser != nil {
			data["HasGithubToken"] = freshUser.GithubToken != ""
		}
```

Also pass through any import error from the query string (for redirect-based errors, though we render inline — this is for the `ImportError` key already used in `importErrorData`):

No additional change needed — `importErrorData` already sets `ImportError` when rendering inline.

- [ ] **Step 2: Add the Import from GitHub card to `tab_overview.html`**

In `engine/templates/partials/tab_overview.html`, insert the following block after the opening `<div>` on line 3 (before the `<!-- Deployment Overview -->` comment on line 4):

```html
        {{if and (eq .Project.Status "created") (eq .Project.GithubURL "")}}
        <!-- Import from GitHub -->
        <div class="card">
            <div class="card-header">
                <span class="card-title">Import from GitHub</span>
            </div>
            <p style="font-size:12px;color:var(--text-tertiary);margin-bottom:12px;">
                Clone a GitHub repo into this project and deploy it immediately.
                {{if not .HasGithubToken}}
                <br>For private repos, <a href="/settings" style="color:var(--accent);">add a GitHub token in Settings</a>.
                {{end}}
            </p>
            {{if .ImportError}}
            <div class="error-msg">{{.ImportError}}</div>
            {{end}}
            <form method="POST" action="/projects/{{.Project.Name}}/import">
                <input type="hidden" name="_csrf" value="{{.CSRF}}">
                <div class="env-add-row">
                    <input type="text" name="github_url" placeholder="https://github.com/user/repo" required style="flex:1;">
                    <button type="submit" class="btn-sm primary">Fetch &amp; Deploy</button>
                </div>
            </form>
        </div>
        {{end}}

        {{if ne .Project.GithubURL ""}}
        <div style="font-size:11px;color:var(--text-tertiary);margin-bottom:12px;">
            Imported from <a href="{{.Project.GithubURL}}" target="_blank" style="color:var(--accent);">{{.Project.GithubURL}}</a>
        </div>
        {{end}}

```

- [ ] **Step 3: Build and verify**

```bash
cd ~/kumbula-poc/engine && go build -o kumbula-engine . && echo "Build OK"
```

- [ ] **Step 4: Commit**

```bash
git add engine/templates/partials/tab_overview.html engine/handlers_detail.go
git commit -m "feat: add Import from GitHub card on project overview"
```

---

### Task 9: End-to-End Test

**Files:** None (manual testing)

- [ ] **Step 1: Restart the engine**

```bash
pkill -f kumbula-engine || true
sleep 2
cd ~/kumbula-poc/engine
GITEA_ADMIN_TOKEN=<token> ./kumbula-engine > /tmp/kumbula-engine.log 2>&1 &
sleep 2
curl -s http://localhost:9000/health | jq .
```

- [ ] **Step 2: Verify settings page**

Sign up or log in at `http://dashboard.kumbula.local`. Click the gear icon in the sidebar footer. Verify the Settings page loads with the GitHub Integration card.

Save a GitHub PAT (or test with an empty save). Verify "Token saved" message. Verify "Connected" badge appears. Test "Remove token" flow.

- [ ] **Step 3: Test public repo import**

1. Create a new project (e.g. `import-test`)
2. On the Overview tab, verify the "Import from GitHub" card is visible
3. Enter a public repo URL: `https://github.com/expressjs/express`
4. Click "Fetch & Deploy"
5. Verify: redirect to project detail, status changes to "building", build logs stream

- [ ] **Step 4: Test private repo import (if applicable)**

1. Save a valid GitHub PAT in Settings
2. Create a new project
3. Enter a private repo URL
4. Click "Fetch & Deploy"
5. Verify it clones and deploys

- [ ] **Step 5: Test error cases**

1. Try importing with an invalid URL (not starting with `https://github.com/`) — verify error message
2. Try importing a nonexistent repo without a token — verify error message
3. Verify the import card disappears after a successful import
4. Verify the "Imported from github.com/..." line appears on the Overview tab

- [ ] **Step 6: Check engine logs**

```bash
tail -30 /tmp/kumbula-engine.log
```

Verify no errors, import logged correctly.

- [ ] **Step 7: Final commit (if any test-driven fixes were made)**

```bash
git add -A
git commit -m "fix: address issues found during GitHub import e2e testing"
```

---
