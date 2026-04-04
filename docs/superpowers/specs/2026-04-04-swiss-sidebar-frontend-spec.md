# KumbulaCloud Frontend Redesign Spec — Swiss Minimalist Sidebar

**Design reference:** `design-concepts/kumbula-01/kumbula-01-concept-01.html`
**Date:** 2026-04-04
**Status:** Draft

---

## 1. Design Overview

Replace the current Pico CSS + HTMX server-rendered UI with a Swiss minimalist design system built on:

- **Typography:** Space Grotesk (headings/UI) + IBM Plex Mono (code/data)
- **Color system:** Dark base (#0a0a0f) with teal (#00d4aa) / cyan (#00b8d4) accents
- **Layout:** Persistent 260px sidebar + main content area (replaces current top-nav layout)
- **Framework:** Keep Go templates + HTMX (no JS framework migration), drop Pico CSS entirely

The sidebar persists across all authenticated views, showing the project list with live status dots. The main content area changes based on the current route.

---

## 2. Design Tokens (CSS Custom Properties)

All colors, spacing, and typography are defined as CSS variables for consistency. These replace all Pico CSS defaults.

```css
:root {
  /* Backgrounds */
  --bg-base:        #0a0a0f;
  --bg-surface:     #0f0f17;
  --bg-elevated:    #14141e;
  --bg-overlay:     #1a1a2e;
  --bg-hover:       #1e1e30;
  --bg-active:      #20203a;

  /* Borders */
  --border-subtle:  rgba(255,255,255,0.05);
  --border-default: rgba(255,255,255,0.09);
  --border-strong:  rgba(255,255,255,0.15);

  /* Text */
  --text-primary:   #f0f0f8;
  --text-secondary: #9090b0;
  --text-tertiary:  #5a5a78;
  --text-disabled:  #3a3a52;

  /* Accents */
  --accent-teal:    #00d4aa;
  --accent-cyan:    #00b8d4;
  --accent-teal-dim: rgba(0,212,170,0.12);
  --accent-teal-glow: rgba(0,212,170,0.25);

  /* Status */
  --status-running:     #00d4aa;
  --status-building:    #f0a500;
  --status-failed:      #e05560;
  --status-idle:        #5a5a78;
  --status-running-bg:  rgba(0,212,170,0.10);
  --status-building-bg: rgba(240,165,0,0.10);
  --status-failed-bg:   rgba(224,85,96,0.10);
  --status-idle-bg:     rgba(90,90,120,0.10);

  /* Layout */
  --sidebar-width: 260px;

  /* Radii */
  --radius-sm: 4px;
  --radius-md: 6px;
  --radius-lg: 10px;

  /* Fonts */
  --font-sans: 'Space Grotesk', sans-serif;
  --font-mono: 'IBM Plex Mono', monospace;

  /* Shadows */
  --shadow-sm:   0 1px 3px rgba(0,0,0,0.4);
  --shadow-md:   0 4px 16px rgba(0,0,0,0.5);
  --shadow-lg:   0 8px 32px rgba(0,0,0,0.6);
  --shadow-teal: 0 0 24px rgba(0,212,170,0.15);

  /* Transitions */
  --transition-fast: 120ms ease;
  --transition-base: 200ms ease;
  --transition-slow: 350ms cubic-bezier(0.4, 0, 0.2, 1);
}
```

---

## 3. Layout Architecture

### Current layout (to be replaced)

```
┌─────────────────────────────────────────────┐
│  Top navbar (logo, user, logout)            │
├─────────────────────────────────────────────┤
│                                             │
│              Page content                   │
│         (full-width, stacked)               │
│                                             │
└─────────────────────────────────────────────┘
```

### New layout

```
┌──────────┬──────────────────────────────────┐
│          │  Top bar (breadcrumb + actions)   │
│  Sidebar │──────────────────────────────────│
│  260px   │                                  │
│          │  Project header + URL             │
│  Brand   │──────────────────────────────────│
│  +New    │  Tabs (Overview|Builds|Env|Set.)  │
│  Project │──────────────────────────────────│
│  list    │                                  │
│  Nav     │  Content area (scrollable)       │
│  User    │  Two-column grid on Overview     │
│          │                                  │
└──────────┴──────────────────────────────────┘
```

### Key structural changes

| Element | Current | New |
|---|---|---|
| Navigation | Top bar with logo + logout | Persistent left sidebar |
| Project list | Dashboard page with card grid | Always visible in sidebar |
| Project detail | Separate page, tabs via query param | Main content area, sidebar stays |
| Auth pages | Full-width centered card | Full-width centered card (no sidebar) |
| New project | Separate page | Separate page (no sidebar) OR modal |

---

## 4. Template Architecture Changes

### 4.1 Replace `layout.html` with two layouts

**`layout_shell.html`** — Authenticated layout with sidebar

```
├── Google Fonts link (Space Grotesk + IBM Plex Mono)
├── <link> to /static/style.css (new design system)
├── <link> to /static/htmx.min.js
├── <link> to /static/sse.js
├── .loading-bar (page load animation)
├── .shell (flex container)
│   ├── <aside class="sidebar">
│   │   ├── .sidebar-brand (logo + "KumbulaCloud" + "Platform as a Service")
│   │   ├── .sidebar-new-project (<a> to /projects/new)
│   │   ├── .sidebar-section-label "Projects"
│   │   ├── .sidebar-projects (HTMX partial target, polls every 5s)
│   │   │   └── {{template "sidebar_projects" .}}
│   │   ├── .sidebar-divider
│   │   ├── .sidebar-nav (Settings, Documentation links)
│   │   └── .sidebar-footer (user avatar initials, email, logout link)
│   └── <main class="main">
│       └── {{template "content" .}}
```

**`layout_auth.html`** — Unauthenticated layout (login/signup)

```
├── Google Fonts link
├── <link> to /static/style.css
├── Full-screen dark background
├── Centered .auth-card
│   └── {{template "content" .}}
```

### 4.2 New partial: `sidebar_projects.html`

Replaces `project_cards.html` for the sidebar context. Rendered by the existing `/partials/project-cards` endpoint (renamed to `/partials/sidebar-projects`).

```html
{{range .Projects}}
<a href="/projects/{{.Name}}" class="project-item {{if eq .Name $.ActiveProject}}active{{end}}">
  <span class="status-dot {{.Status}}"></span>
  <div class="project-info">
    <div class="project-name">{{.Name}}</div>
    <div class="project-meta">
      {{if eq .Status "building"}}Building…
      {{else if eq .Status "running"}}Running
      {{else if eq .Status "failed"}}Failed
      {{else}}Idle · no builds
      {{end}}
    </div>
  </div>
  <span class="project-arrow">›</span>
</a>
{{else}}
<div class="sidebar-empty">No projects yet</div>
{{end}}
```

**HTMX polling** (on the sidebar container):
```html
<div class="sidebar-projects"
     hx-get="/partials/sidebar-projects"
     hx-trigger="every 5s"
     hx-swap="innerHTML">
```

### 4.3 Dashboard page (`dashboard/index.html`)

The dashboard no longer needs its own project grid — the sidebar IS the project list. The main content area for `/` shows a welcome/overview state:

**When user has projects:** Redirect to the first project's detail page, or show a summary dashboard with:
- Total projects count, running/failed counts
- Recent build activity (last 5 builds across all projects)
- Quick links

**When user has no projects:** Show an onboarding empty state with a prominent "Create Your First Project" CTA and a brief explanation of the workflow (create → push → deploy).

### 4.4 Project detail (`projects/detail.html`)

This is the primary view. Replaces the current template entirely.

```
<div class="topbar">
  <div class="topbar-breadcrumb">
    Projects / <span class="crumb-current">{{.Project.Name}}</span>
  </div>
  <div class="topbar-actions">
    <div class="status-pill {{.Project.Status}}">
      <span class="dot"></span>
      {{.Project.Status | upper}}
    </div>
    <a href="/projects/{{.Project.Name}}?tab=builds" class="topbar-btn ghost">Logs</a>
    <form method="POST" action="/projects/{{.Project.Name}}/redeploy">
      <input type="hidden" name="_csrf" value="{{.CSRF}}">
      <button class="topbar-btn primary">↑ Deploy</button>
    </form>
  </div>
</div>

<div class="project-header">
  <div class="project-title">{{.Project.Name}}</div>
  <div class="project-url">
    <span class="url-icon">↗</span>
    <a href="{{.Project.URL}}" class="project-url-text">{{.Project.URL}}</a>
  </div>
</div>

<div class="tabs-bar">
  <a href="?tab=overview" class="tab {{if eq .Tab "overview"}}active{{end}}">Overview</a>
  <a href="?tab=builds" class="tab {{if eq .Tab "builds"}}active{{end}}">Builds</a>
  <a href="?tab=env" class="tab {{if eq .Tab "env"}}active{{end}}">Environment</a>
  <a href="?tab=settings" class="tab {{if eq .Tab "settings"}}active{{end}}">Settings</a>
</div>

<div class="content">
  {{if eq .Tab "overview"}}
    {{template "tab_overview" .}}
  {{else if eq .Tab "builds"}}
    {{template "tab_builds" .}}
  {{else if eq .Tab "env"}}
    {{template "tab_envvars" .}}
  {{else if eq .Tab "settings"}}
    {{template "tab_settings" .}}
  {{end}}
</div>
```

### 4.5 Tab partials (replacing current partials)

#### `tab_overview.html` (NEW — does not exist in current codebase)

Two-column grid layout:

**Left column (main):**
1. **Deployment Overview card** — Build count, last build time, status
   - Stats row: Total builds | Last build time | Status
   - Last deployment row: commit SHA (7 chars), commit message, timestamp, duration
2. **Git Remote Setup card** — Step-by-step copy-paste instructions
   - Step 1: `git remote add kumbula http://{{.User.Username}}@gitea.kumbula.local/{{.Project.GiteaRepo}}.git`
   - Step 2: `git push kumbula main`
   - Each step has a copy button (vanilla JS clipboard API)

**Right column (340px sidebar):**
1. **Project Info card** — Key/value rows: Status, Runtime (auto-detected), Port (3000), Repository, Live URL
2. **Environment Variables card** — Count display, masked preview of first 3 vars, "Manage variables →" link to env tab
3. **Build Health card** — Sparkline of last 30 builds (colored bars: green=success, amber=warning, red=failed)

#### `tab_builds.html` (replaces `build_list.html`)

Redesigned builds table with the new card styling:

```
<div class="card">
  <div class="card-header">
    <span class="card-title">Build History</span>
  </div>
  <table class="builds-table">
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
        <td class="build-num">#{{.ID}}</td>
        <td><span class="status-pill {{.Status}}"><span class="dot"></span>{{.Status}}</span></td>
        <td class="mono">{{slice .CommitSHA 0 7}}</td>
        <td class="mono">{{.StartedAt.Time.Format "Jan 2, 15:04"}}</td>
        <td class="mono">{{/* duration calc */}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
</div>

{{/* Active build log (SSE) */}}
{{range .Builds}}
  {{if eq .Status "building"}}
  <div class="card">
    <div class="card-header">
      <span class="card-title">Live Build Log — #{{.ID}}</span>
      <span class="status-pill building"><span class="dot"></span>Building</span>
    </div>
    <div class="build-log"
         hx-ext="sse"
         sse-connect="/projects/{{$.Project.Name}}/builds/{{.ID}}/stream"
         sse-swap="log"
         hx-swap="beforeend">
      <pre><code></code></pre>
    </div>
  </div>
  {{end}}
{{end}}
```

#### `tab_envvars.html` (replaces `envvar_form.html`)

Redesigned with card layout:

```
<div id="envvar-section" class="envvar-section">
  <div class="card">
    <div class="card-header">
      <span class="card-title">Environment Variables</span>
      <span class="card-badge teal">{{len .EnvVars}} vars</span>
    </div>

    {{/* Auto-managed DATABASE_URL */}}
    {{if .Project.DatabaseURL.Valid}}
    <div class="env-item managed">
      <span class="env-key">DATABASE_URL</span>
      <span class="env-val-mask">••••••••••</span>
      <span class="env-managed-badge">Auto-managed</span>
    </div>
    {{end}}

    {{/* User-defined vars */}}
    {{range .EnvVars}}
    <div class="env-item">
      <span class="env-key">{{.Key}}</span>
      <span class="env-val-mask">••••••••••</span>
      <button class="btn-delete"
              hx-delete="/projects/{{$.Project.Name}}/env?id={{.ID}}"
              hx-headers='{"X-CSRF-Token": "{{$.CSRF}}"}'
              hx-target="#envvar-section"
              hx-swap="outerHTML"
              hx-confirm="Delete {{.Key}}?">×</button>
    </div>
    {{end}}
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">Add Variable</span>
    </div>
    <form hx-post="/projects/{{.Project.Name}}/env"
          hx-headers='{"X-CSRF-Token": "{{.CSRF}}"}'
          hx-target="#envvar-section"
          hx-swap="outerHTML">
      <div class="env-add-row">
        <input name="key" placeholder="KEY" class="input-mono" required>
        <input name="value" placeholder="value" class="input-mono" required>
        <button type="submit" class="btn-sm primary">Add</button>
      </div>
    </form>
  </div>
</div>
```

#### `tab_settings.html` (replaces `settings_form.html`)

```
<div class="card danger-card">
  <div class="card-header">
    <span class="card-title" style="color: var(--status-failed)">Danger Zone</span>
  </div>
  <p class="settings-warning">
    Destroying this project will permanently delete:
  </p>
  <ul class="settings-delete-list">
    <li>Docker container and image</li>
    <li>PostgreSQL database and user</li>
    <li>Gitea repository</li>
    <li>All environment variables and build history</li>
  </ul>
  <form method="POST" action="/projects/{{.Project.Name}}/settings?action=destroy"
        onsubmit="return confirm('Permanently destroy {{.Project.Name}}? This cannot be undone.')">
    <input type="hidden" name="_csrf" value="{{.CSRF}}">
    <button type="submit" class="qa-btn danger">
      Destroy {{.Project.Name}}
    </button>
  </form>
</div>
```

### 4.6 Auth pages (login.html, signup.html)

Keep as centered cards but restyle with the new design tokens:

- Dark full-screen background (#0a0a0f)
- Centered card with `--bg-elevated` background, `--border-subtle` border
- Space Grotesk typography
- Teal accent on submit buttons
- Brand logo at top of card
- No sidebar (uses `layout_auth.html`)

### 4.7 New project page (`projects/new.html`)

Two options:

**Option A (recommended):** Keep as a separate page using `layout_shell.html` (sidebar visible). The main content area shows a simple centered form:

```
<div class="new-project-form">
  <h2>Create a new project</h2>
  <p>Name your project. This becomes your URL: <code>{name}.kumbula.local</code></p>
  <form method="POST" action="/projects/new">
    <input type="hidden" name="_csrf" value="{{.CSRF}}">
    <input name="name" pattern="[a-z][a-z0-9-]{1,48}[a-z0-9]"
           placeholder="my-awesome-app" class="input-mono" required autofocus>
    <button type="submit" class="topbar-btn primary">Create Project</button>
  </form>
</div>
```

**Option B:** Modal overlay triggered from sidebar button (more complex, deferred).

---

## 5. Backend Changes Required

### 5.1 Template system (`templates.go`)

**Changes:**
- Register two layout templates: `layout_shell` and `layout_auth`
- Add new template function: `initials` — extracts first letter(s) of username for avatar
- Add new template function: `timeAgo` — formats `time.Time` as "4m ago", "2h ago", etc.
- Add new template function: `slice` — substring helper for commit SHA truncation
- Add new template function: `buildDuration` — calculates duration between StartedAt and FinishedAt
- Update `RenderPage` to accept a layout parameter or auto-detect based on auth state

```go
// New template functions to add to funcMap
funcMap := template.FuncMap{
    "upper":         strings.ToUpper,
    "statusClass":   statusClass,
    "initials":      func(s string) string { /* first 2 chars uppercased */ },
    "timeAgo":       func(t time.Time) string { /* "4m ago", "2h ago", "3d ago" */ },
    "shortSHA":      func(s string) string { if len(s) >= 7 { return s[:7] }; return s },
    "buildDuration": func(b Build) string { /* "1m 22s" or "—" if not finished */ },
}
```

**New render helpers:**
```go
// RenderShell renders a page with the sidebar layout
func RenderShell(w http.ResponseWriter, r *http.Request, templateName string, data map[string]interface{}) {
    // Auto-inject: User, CSRF, Projects (for sidebar), ActiveProject
    user := CtxUser(r)
    data["User"] = user
    data["CSRF"] = CtxCSRF(r)

    // Load user's projects for sidebar
    projects, _ := GetProjectsByUser(db, user.ID)
    data["SidebarProjects"] = projects

    // Determine active project from URL
    if project := CtxProject(r); project != nil {
        data["ActiveProject"] = project.Name
    }

    // Render with layout_shell
    ...
}

// RenderAuth renders a page with the auth layout (no sidebar)
func RenderAuth(w http.ResponseWriter, r *http.Request, templateName string, data map[string]interface{}) {
    // Only inject CSRF
    data["CSRF"] = CtxCSRF(r)
    // Render with layout_auth
    ...
}
```

### 5.2 Handlers changes

#### `handlers_dashboard.go`

```go
// handleDashboard — now renders the shell layout with a dashboard/welcome view
func (app *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
    user := CtxUser(r)
    projects, _ := GetProjectsByUser(app.db, user.ID)

    if len(projects) > 0 {
        // Option: redirect to first project
        // http.Redirect(w, r, "/projects/"+projects[0].Name, http.StatusFound)
        // OR: render a summary dashboard
        // Fetch recent builds across all projects for the activity feed
        recentBuilds, _ := GetRecentBuildsForUser(app.db, user.ID, 5)
        RenderShell(w, r, "dashboard", map[string]interface{}{
            "Projects":     projects,
            "RecentBuilds": recentBuilds,
            "RunningCount": countByStatus(projects, "running"),
            "FailedCount":  countByStatus(projects, "failed"),
        })
    } else {
        RenderShell(w, r, "dashboard_empty", nil)
    }
}
```

#### `handlers_detail.go`

```go
// handleProjectDetail — now includes Overview tab data
func (app *App) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
    project := CtxProject(r)
    tab := r.URL.Query().Get("tab")
    if tab == "" {
        tab = "overview"
    }

    data := map[string]interface{}{
        "Project": project,
        "Tab":     tab,
    }

    switch tab {
    case "overview":
        builds, _ := GetBuildsByProject(app.db, project.ID)
        envVars, _ := GetEnvVarsByProject(app.db, project.ID)
        data["Builds"] = builds
        data["BuildCount"] = len(builds)
        data["EnvVarCount"] = len(envVars)
        data["EnvVarsPreview"] = firstN(envVars, 3) // first 3 for preview
        if len(builds) > 0 {
            data["LastBuild"] = builds[0]
        }
    case "builds":
        builds, _ := GetBuildsByProject(app.db, project.ID)
        data["Builds"] = builds
    case "env":
        envVars, _ := GetEnvVarsByProject(app.db, project.ID)
        data["EnvVars"] = envVars
    case "settings":
        // No extra data needed
    }

    RenderShell(w, r, "project_detail", data)
}
```

#### New HTMX endpoint: sidebar projects

```go
// Add new route in main.go
mux.HandleFunc("GET /partials/sidebar-projects", app.requireAuth(app.handleSidebarProjectsPartial))

func (app *App) handleSidebarProjectsPartial(w http.ResponseWriter, r *http.Request) {
    user := CtxUser(r)
    projects, _ := GetProjectsByUser(app.db, user.ID)
    // Get active project from Referer header or query param
    activeProject := r.URL.Query().Get("active")
    RenderPartial(w, "sidebar_projects", map[string]interface{}{
        "Projects":      projects,
        "ActiveProject": activeProject,
    })
}
```

### 5.3 Model additions

#### `models_build.go` — new query

```go
// GetRecentBuildsForUser returns the N most recent builds across all user's projects
func GetRecentBuildsForUser(db *sql.DB, userID int, limit int) ([]BuildWithProject, error) {
    query := `
        SELECT b.id, b.project_id, b.status, b.commit_sha, b.started_at, b.finished_at,
               p.name as project_name
        FROM builds b
        JOIN projects p ON p.id = b.project_id
        WHERE p.user_id = $1
        ORDER BY b.started_at DESC
        LIMIT $2
    `
    // ...
}

// BuildWithProject extends Build with project name for cross-project views
type BuildWithProject struct {
    Build
    ProjectName string
}
```

### 5.4 Route changes

| Current Route | New Route | Change |
|---|---|---|
| `GET /partials/project-cards` | `GET /partials/sidebar-projects` | Rename, new partial |
| All authenticated routes | — | Use `RenderShell` instead of `RenderPage` |
| Auth routes | — | Use `RenderAuth` instead of `RenderPage` |

No routes are removed. No new public routes. The `/partials/project-cards` endpoint is renamed.

### 5.5 Middleware changes

No changes to middleware logic. The `SessionMiddleware`, `RequireAuth`, `RequireProjectOwner`, and CSRF handling all remain identical.

One minor addition to `RequireProjectOwner`: ensure it stores the project name in the template data context so the sidebar can highlight the active project.

---

## 6. Static Assets Changes

### 6.1 `style.css` — Complete rewrite

Replace the entire Pico CSS override file with the new design system. The new file is ~1200 lines covering:

- Design tokens (CSS custom properties)
- Reset and base styles
- Grid texture overlay (body::before)
- Shell layout (sidebar + main flex)
- Sidebar: brand, new-project button, project list, nav, user footer
- Main: topbar, project header, tabs bar, content grid
- Card system (elevated surfaces with borders)
- Status dots and pills (with animations: pulse, blink)
- Code blocks with syntax highlighting classes
- Copy buttons
- Build log terminal styling
- Environment variable rows
- Danger zone styling
- Form inputs (mono style)
- Quick actions bar
- Loading bar animation
- Tooltips
- Builds table
- Auth card (centered login/signup)
- Responsive scrollbars
- Animations: slideInLeft, fadeSlideIn, cardReveal, slideUp, fadeIn, loadBar, pulse, blink

### 6.2 Remove Pico CSS

Remove the Pico CSS `<link>` from the layout template. It is no longer used.

### 6.3 Add Google Fonts

Add to the layout `<head>`:
```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@300;400;500;600;700&family=IBM+Plex+Mono:wght@300;400;500&display=swap" rel="stylesheet">
```

**Self-hosting consideration:** For a self-hosted PaaS that may run without internet, consider downloading the font files into `/static/fonts/` and using `@font-face` declarations instead.

### 6.4 Keep `htmx.min.js` and `sse.js`

No changes to these files.

### 6.5 Add `clipboard.js` (NEW, ~20 lines)

Small vanilla JS for copy-to-clipboard functionality on git remote setup blocks:

```javascript
document.addEventListener('click', function(e) {
    var btn = e.target.closest('[data-copy]');
    if (!btn) return;
    var text = btn.getAttribute('data-copy');
    navigator.clipboard.writeText(text).then(function() {
        var original = btn.textContent;
        btn.textContent = 'Copied!';
        setTimeout(function() { btn.textContent = original; }, 2000);
    });
});
```

---

## 7. File-by-File Change Summary

### Files to CREATE

| File | Purpose |
|---|---|
| `engine/templates/layout_shell.html` | Authenticated layout with sidebar |
| `engine/templates/layout_auth.html` | Unauthenticated layout (login/signup) |
| `engine/templates/partials/sidebar_projects.html` | Sidebar project list partial |
| `engine/templates/partials/tab_overview.html` | Overview tab with deploy info + git setup |
| `engine/templates/partials/tab_builds.html` | Builds tab (replaces build_list) |
| `engine/templates/partials/tab_envvars.html` | Env vars tab (replaces envvar_form) |
| `engine/templates/partials/tab_settings.html` | Settings tab (replaces settings_form) |
| `engine/templates/dashboard/empty.html` | Empty state for new users |
| `engine/static/clipboard.js` | Copy-to-clipboard helper |

### Files to MODIFY

| File | Changes |
|---|---|
| `engine/static/style.css` | Complete rewrite — new design system |
| `engine/templates.go` | Dual layout support, new template functions, new render helpers |
| `engine/handlers_dashboard.go` | Use RenderShell, add recent builds query, empty state |
| `engine/handlers_detail.go` | Add overview tab data loading (builds, env preview) |
| `engine/handlers_auth.go` | Use RenderAuth instead of RenderPage |
| `engine/handlers_projects.go` | Use RenderShell |
| `engine/handlers_builds.go` | Use RenderShell |
| `engine/handlers_envvars.go` | Use RenderShell (partial rendering unchanged) |
| `engine/handlers_settings.go` | Use RenderShell |
| `engine/models_build.go` | Add GetRecentBuildsForUser, BuildWithProject type |
| `engine/main.go` | Rename partial route, add sidebar-projects route |

### Files to DELETE

| File | Reason |
|---|---|
| `engine/templates/layout.html` | Replaced by layout_shell + layout_auth |
| `engine/templates/partials/project_cards.html` | Replaced by sidebar_projects |
| `engine/templates/partials/build_list.html` | Replaced by tab_builds |
| `engine/templates/partials/envvar_form.html` | Replaced by tab_envvars |
| `engine/templates/partials/settings_form.html` | Replaced by tab_settings |

---

## 8. Component Reference

### Status Dot (sidebar)

```html
<span class="status-dot running"></span>  <!-- green + pulse animation -->
<span class="status-dot building"></span> <!-- amber + blink animation -->
<span class="status-dot failed"></span>   <!-- red + glow -->
<span class="status-dot idle"></span>     <!-- gray, no animation -->
```

Status is derived from `Project.Status` field. Map: `"running"` → running, `"building"` → building, `"failed"` → failed, `"created"` → idle.

### Status Pill (topbar, tables)

```html
<div class="status-pill running"><span class="dot"></span>Running</div>
<div class="status-pill building"><span class="dot"></span>Building</div>
<div class="status-pill failed"><span class="dot"></span>Failed</div>
```

### Card

```html
<div class="card">
  <div class="card-header">
    <span class="card-title">SECTION TITLE</span>
    <span class="card-badge teal">Badge</span>
  </div>
  <!-- content -->
</div>
```

### Code Block (git instructions)

```html
<div class="code-block">
  <code>
    <span class="cmd-keyword">git</span>
    remote add kumbula
    <span class="cmd-url">http://user@gitea.kumbula.local/user/project.git</span>
  </code>
  <button class="copy-btn" data-copy="git remote add kumbula http://...">Copy</button>
</div>
```

### Buttons

```html
<button class="topbar-btn primary">↑ Deploy</button>       <!-- Teal accent -->
<button class="topbar-btn ghost">Logs</button>              <!-- Outline -->
<button class="btn-new-project">+ New Project</button>      <!-- Sidebar CTA -->
<button class="btn-sm primary">Add</button>                 <!-- Small inline -->
<button class="btn-sm ghost">Manage variables →</button>    <!-- Small outline -->
<button class="qa-btn danger">Delete Project</button>       <!-- Red destructive -->
```

---

## 9. Animation Inventory

| Animation | Used on | Duration | Trigger |
|---|---|---|---|
| `slideInLeft` | Sidebar | 0.5s | Page load |
| `fadeIn` | Main content | 0.5s | Page load (0.1s delay) |
| `fadeSlideIn` | Sidebar project items | 0.4s | Page load (staggered 0.08s per item) |
| `slideUp` | Project header, tabs | 0.45s | Page load (staggered) |
| `cardReveal` | Content cards | 0.5s | Page load (staggered 0.25s+) |
| `loadBar` | Top loading bar | 1.2s | Page load |
| `pulse` | Running status dots | 2s | Infinite loop |
| `blink` | Building status dots | 1.2s | Infinite loop |

---

## 10. Data Flow Diagram

```
Browser loads /projects/api-gateway
  │
  ├─ Server: handleProjectDetail
  │   ├─ CtxProject(r) → project from middleware
  │   ├─ GetProjectsByUser() → sidebar projects
  │   ├─ GetBuildsByProject() → builds for overview
  │   ├─ GetEnvVarsByProject() → env count + preview
  │   └─ RenderShell("project_detail", data)
  │       ├─ layout_shell.html
  │       │   ├─ sidebar_projects.html (with active highlight)
  │       │   └─ project_detail.html
  │       │       └─ tab_overview.html
  │       └─ Response → browser
  │
  ├─ Every 5s: HTMX GET /partials/sidebar-projects?active=api-gateway
  │   └─ Returns updated sidebar_projects.html partial
  │
  └─ If building: SSE /projects/api-gateway/builds/{id}/stream
      └─ Appends log lines to build-log <pre> (unchanged from current)
```

---

## 11. Migration Strategy

1. **Phase 1 — Static assets:** Rewrite `style.css`, add Google Fonts, add `clipboard.js`
2. **Phase 2 — Templates:** Create new layout files, sidebar partial, tab partials
3. **Phase 3 — Backend:** Update `templates.go` with dual layout + new functions, update all handlers to use RenderShell/RenderAuth
4. **Phase 4 — Routes:** Rename partial endpoint, add sidebar-projects route
5. **Phase 5 — Models:** Add `GetRecentBuildsForUser` query
6. **Phase 6 — Cleanup:** Delete old templates, remove Pico CSS reference

Each phase can be tested independently. The sidebar partial and HTMX polling can be verified in isolation before wiring into the full layout.

---

## 12. Out of Scope (Future)

- Modal-based project creation (keep as separate page for now)
- Custom domain management UI
- Team/collaboration features
- Mobile responsive breakpoints (this is a server admin dashboard, desktop-first is fine)
- Build health sparkline (requires storing build duration data — can show simple success/fail bars from existing data)
- Uptime percentage (requires health check monitoring, not currently implemented)
- Region display (single-machine deployment, no regions)
