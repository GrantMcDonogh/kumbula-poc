# KumbulaCloud Frontend Dashboard — Design Spec

## Overview

A web dashboard for KumbulaCloud that gives individual developers a Railway-like experience: sign up, create projects, link to Git repos, manage builds, and view logs. Built as an extension of the existing Go engine using `html/template` and htmx for dynamic interactions.

**Scope:** Auth + project dashboard + build/deploy management (env vars, build logs, redeploy).  
**Target users:** Individual developers.  
**Git integration:** Gitea-native only (no external providers).  
**Stack:** Go monolith with html/template + htmx. Single binary, no JS build step.

---

## Architecture

The existing Go engine expands to serve both the dashboard UI and the API from a single binary. The dashboard is available at `dashboard.kumbula.local` via Traefik.

### Package structure

```
engine/
├── main.go              # entrypoint, router setup
├── handlers/
│   ├── auth.go          # signup, login, logout
│   ├── dashboard.go     # project list, overview
│   ├── projects.go      # create, settings, env vars, destroy
│   ├── builds.go        # build log streaming, redeploy
│   └── webhook.go       # existing webhook logic, extracted
├── middleware/
│   ├── session.go       # cookie-based sessions
│   └── auth.go          # require-login middleware
├── models/
│   ├── user.go          # user CRUD
│   ├── project.go       # project/app CRUD
│   └── build.go         # build log persistence
├── templates/
│   ├── layout.html      # base layout (nav, htmx script)
│   ├── auth/
│   │   ├── login.html
│   │   └── signup.html
│   ├── dashboard/
│   │   └── index.html   # project cards grid
│   └── projects/
│       ├── detail.html  # single project view
│       ├── builds.html  # build history
│       ├── logs.html    # live build log partial (SSE)
│       └── settings.html# env vars, danger zone
├── static/
│   ├── style.css        # minimal custom CSS
│   └── htmx.min.js     # vendored htmx
├── deploy.go            # existing deploy logic, extracted
└── db.go                # database connection + migrations
```

### Key architectural decisions

- Templates use Go's `html/template` with a shared layout
- htmx is vendored as a single JS file — no npm, no build step
- Pico CSS for sensible defaults, plus a small custom stylesheet for KumbulaCloud-specific styling
- All state moves from the in-memory `deployments` map to PostgreSQL
- Sessions stored in PostgreSQL (token + expiry)

---

## Data Model

All tables live in the existing `kumbula_system` PostgreSQL database.

### users

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| username | VARCHAR(50) UNIQUE | login name |
| email | VARCHAR(255) UNIQUE | |
| password_hash | VARCHAR(255) | bcrypt, cost 12 |
| gitea_password | VARCHAR(255) | generated random password for Gitea account |
| gitea_token | VARCHAR(255) | API token for Gitea operations |
| created_at | TIMESTAMPTZ | |

### projects

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| user_id | INT FK -> users | owner |
| name | VARCHAR(100) UNIQUE | maps to Gitea repo name and subdomain |
| gitea_repo | VARCHAR(255) | full Gitea repo path (e.g. `username/my-app`) |
| container_id | VARCHAR(64) | current running container, nullable |
| status | VARCHAR(20) | `created`, `building`, `running`, `stopped`, `failed` |
| url | VARCHAR(255) | e.g. `http://my-app.kumbula.local` |
| database_url | TEXT | provisioned DB connection string, nullable |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

### builds

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| project_id | INT FK -> projects | |
| status | VARCHAR(20) | `pending`, `building`, `success`, `failed` |
| log | TEXT | full build output |
| commit_sha | VARCHAR(40) | from webhook payload |
| started_at | TIMESTAMPTZ | |
| finished_at | TIMESTAMPTZ | nullable |

### project_env_vars

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| project_id | INT FK -> projects | |
| key | VARCHAR(255) | e.g. `STRIPE_KEY` |
| value | TEXT | plaintext for PoC |
| created_at | TIMESTAMPTZ | |

### sessions

| Column | Type | Notes |
|--------|------|-------|
| token | VARCHAR(64) PK | random hex token (32 bytes crypto/rand) |
| user_id | INT FK -> users | |
| expires_at | TIMESTAMPTZ | |

### Data model decisions

- Project names are globally unique (they map to subdomains) — enforced at DB level
- Build logs stored as plain text, streamed live via SSE during builds
- Sessions are DB-backed rather than JWT — simpler to invalidate
- `DATABASE_URL` is system-managed and read-only in the UI

---

## User Flows

### Signup & Login

1. User visits `dashboard.kumbula.local` -> redirected to `/login` if no session
2. `/signup` — username, email, password form. On submit:
   - Create user row in PostgreSQL
   - Create matching Gitea user via Gitea Admin API (`POST /api/v1/admin/users`)
   - Generate and store Gitea API token (`POST /api/v1/users/{username}/tokens`)
   - Create session, set cookie, redirect to dashboard
   - If Gitea user creation fails, whole signup rolls back
3. `/login` — username + password, bcrypt verify, create session, redirect
4. `/logout` — delete session row, clear cookie, redirect to login

### Dashboard (project list)

- Grid of project cards: name, status badge, URL link, last deploy time
- "New Project" button top-right
- htmx polls `/partials/project-cards` every 5s to refresh status badges

### Create Project

1. User clicks "New Project" -> form with project name only
2. On submit, engine:
   - Validates name: `^[a-z][a-z0-9-]{1,48}[a-z0-9]$`
   - Checks against reserved names blacklist
   - Creates Gitea repo under user's Gitea account (using stored token)
   - Adds webhook pointing to `/webhook`
   - Creates project row in PostgreSQL
   - Returns project detail page with git remote instructions

### Project Detail Page

Single page with sections:

- **Overview** — status, URL (clickable), container ID, uptime, last deploy time
- **Builds** — list of builds with status, commit SHA, timestamp. Click to expand log. Latest build streams live via SSE when in progress.
- **Environment Variables** — key/value table with add/edit/delete via htmx inline forms. Changing vars does NOT auto-redeploy; user must click "Redeploy".
- **Settings** — rename project (with warning), destroy project (confirmation required). Destroy stops container, drops DB, deletes Gitea repo.

### Redeploy

- Button on project detail page
- Triggers same deploy flow as webhook push — clones latest, builds, replaces container

### Build Log Streaming

- In-progress builds auto-connect to `/projects/{name}/builds/{id}/stream` via SSE
- htmx SSE extension swaps in new log lines as they arrive
- Final event sent on build completion, status badge updates

---

## Gitea Account Management

Gitea is an implementation detail — users only interact with KumbulaCloud's UI.

**On signup:** Engine creates a Gitea user via Admin API with the same username, a generated random password, and matching email. An API token is generated and stored. The engine requires a Gitea admin token (from existing setup) to create new users.

**On project create:** Engine uses the user's stored Gitea token to create repos and webhooks. Clone URLs include user credentials: `http://username:password@gitea.kumbula.local/username/my-app.git`.

**On webhook receive:** Engine looks up the project by repo name, verifies it exists and maps to a valid user. Replaces current single-user `kumbula` admin approach.

---

## Routing & Middleware

### Routes

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/login` | GET/POST | No | Login page & handler |
| `/signup` | GET/POST | No | Signup page & handler |
| `/logout` | POST | Yes | Clear session |
| `/` | GET | Yes | Dashboard — project grid |
| `/projects/new` | GET/POST | Yes | Create project form & handler |
| `/projects/{name}` | GET | Yes | Project detail page |
| `/projects/{name}/redeploy` | POST | Yes | Trigger redeploy |
| `/projects/{name}/env` | GET/POST/DELETE | Yes | Env var management (htmx) |
| `/projects/{name}/settings` | GET/POST | Yes | Rename, destroy |
| `/projects/{name}/builds/{id}/stream` | GET (SSE) | Yes | Live build log stream |
| `/partials/project-cards` | GET | Yes | htmx polling partial |
| `/webhook` | POST | No | Gitea webhook (existing) |
| `/health` | GET | No | Health check (existing) |
| `/apps` | GET | No | API — list apps (existing, CLI compat) |

### Middleware stack

1. **Session loader** — reads cookie, loads user from DB, attaches to request context. All routes.
2. **Auth required** — all routes except `/login`, `/signup`, `/webhook`, `/health`, `/apps`. Redirects to `/login`.
3. **Project ownership** — on `/projects/{name}` routes, verifies project belongs to logged-in user. Returns 404 (not 403) to avoid leaking project existence.

### Security

- Passwords: bcrypt, cost 12
- Session tokens: 32 bytes `crypto/rand`, hex-encoded
- CSRF: per-session token checked on all POST/DELETE requests
- Project name validation: `^[a-z][a-z0-9-]{1,48}[a-z0-9]$`
- Reserved names blacklist: `traefik`, `gitea`, `dashboard`, `postgres`, `api`, `admin`, `www`
- Gitea credentials stored in PostgreSQL — acceptable for single-machine PoC, flagged as production concern
