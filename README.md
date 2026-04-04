# KumbulaCloud PoC

A mini-PaaS that runs on a single machine. Push code, get a running app with a database and a URL — no cloud providers required.

```
git push kumbula main
# 2 minutes later...
# http://my-app.kumbula.local is live
```

## How it works

```
browser ──▶ dashboard.kumbula.local ──▶ sign up, create project
                                              │
git push ──▶ Gitea ──▶ webhook ──▶ kumbula-engine
                                        │
                                        ├── detect language (Node, Python, Go, static)
                                        ├── use existing Dockerfile or generate one
                                        ├── docker build (with build args)
                                        ├── provision PostgreSQL database
                                        ├── inject env vars
                                        └── docker run with Traefik labels
                                              │
browser ◀── Traefik ◀── *.kumbula.local ◀─────┘
```

## Components

| Component | Role |
|-----------|------|
| [Traefik](https://traefik.io/) | Reverse proxy — auto-discovers containers, routes by hostname |
| [Gitea](https://gitea.io/) | Self-hosted Git server with webhook support |
| [PostgreSQL](https://www.postgresql.org/) | Shared database — per-app databases provisioned automatically |
| [Redis](https://redis.io/) | Shared Redis — available to apps that need queues/caching |
| [MinIO](https://min.io/) | S3-compatible object storage for file uploads |
| **kumbula-engine** | Go API + web dashboard that orchestrates builds, deploys, and DB provisioning |
| [dnsmasq](https://thekelleys.org.uk/dnsmasq/doc.html) | Wildcard DNS for `*.kumbula.local` |

## Dashboard

The web dashboard at `http://dashboard.kumbula.local` provides a Railway-like experience:

- **Sign up / log in** — accounts with automatic Gitea user provisioning
- **Create projects** — provisions a Gitea repo, webhook, and PostgreSQL database
- **Project overview** — status, URL, git remote instructions
- **Build logs** — live streaming via SSE as your app builds
- **Environment variables** — add/remove env vars injected at deploy time
- **Redeploy** — one-click redeploy from the dashboard
- **Destroy** — full cleanup (container, database, Gitea repo)

Built with Go `html/template`, [htmx](https://htmx.org/), and [Pico CSS](https://picocss.com/).

## Quick start

```bash
# 1. Install prerequisites (Docker, Go 1.22+, dnsmasq, jq, psql)

# 2. Start infrastructure
cd ~/kumbula-poc
docker compose up -d

# 3. Configure Gitea (first time only)
docker exec -u git gitea gitea admin user create \
  --admin --username kumbula --password kumbula123 \
  --email admin@kumbula.local --config /data/gitea/conf/app.ini

# Create an admin API token
GITEA_ADMIN_TOKEN=$(curl -s -u "kumbula:kumbula123" \
  -X POST "http://gitea.kumbula.local/api/v1/users/kumbula/tokens" \
  -H "Content-Type: application/json" \
  -d '{"name":"admin","scopes":["all"]}' | jq -r .sha1)

# 4. Build and start the engine
cd engine
go build -o kumbula-engine .
GITEA_ADMIN_TOKEN=$GITEA_ADMIN_TOKEN ./kumbula-engine

# 5. Open http://dashboard.kumbula.local
#    Sign up, create a project, push code — app deploys automatically
```

## Deploying an app

After signing up and creating a project in the dashboard:

```bash
# Clone your existing project
git clone https://github.com/you/your-app.git
cd your-app

# Add KumbulaCloud as a remote (URL shown on project page)
git remote add kumbula http://username@gitea.kumbula.local/username/your-app.git

# Push to deploy
git push kumbula main
```

The engine will:
1. Clone your code
2. Use your `Dockerfile` if present, or auto-generate one (Node, Python, Go, static)
3. Build the Docker image
4. Provision a PostgreSQL database
5. Start the container with Traefik routing + your env vars

Your app is live at `http://your-app.kumbula.local`.

### Supported app types

| Type | Detection | Auto-generated Dockerfile |
|------|-----------|--------------------------|
| Node.js / Next.js | `package.json` | Node 20 Alpine, `npm ci`, port 3000 |
| Python | `requirements.txt` or `app.py` | Python 3.12 Slim, `pip install`, port 3000 |
| Go | `go.mod` or `main.go` | Multi-stage Go 1.22, port 3000 |
| Static | `index.html` | Nginx Alpine, port 80 |
| Custom | `Dockerfile` present | Uses your Dockerfile as-is |

Apps with their own `Dockerfile` (like Next.js with standalone output) are built as-is with build args like `NEXT_PUBLIC_APP_URL` passed automatically.

## Environment variables

Set env vars in the dashboard under the **Environment** tab. They are injected into the container on the next deploy. These are always available:

| Variable | Value |
|----------|-------|
| `PORT` | `3000` |
| `APP_NAME` | Project name |
| `APP_URL` | `http://<name>.kumbula.local` |
| `DATABASE_URL` | Auto-provisioned PostgreSQL connection string |

For apps needing Redis or MinIO, add these env vars:

```
REDIS_URL=redis://kumbula-redis:6379
MINIO_ENDPOINT=kumbula-minio
MINIO_PORT=9000
MINIO_USE_SSL=false
MINIO_ACCESS_KEY=minioadmin
MINIO_SECRET_KEY=minioadmin
```

## CLI

```bash
kc create <name>    # Create a new app (Gitea repo + webhook)
kc apps             # List deployed apps
kc logs <name>      # Stream app logs
kc destroy <name>   # Remove an app
```

## Validation

```bash
./validate.sh
```

## Project structure

```
kumbula-poc/
├── docker-compose.yml          # Traefik, Gitea, PostgreSQL, Redis, MinIO
├── engine/
│   ├── main.go                 # Entrypoint, routing, middleware wiring
│   ├── deploy.go               # Build + deploy pipeline
│   ├── db.go                   # PostgreSQL connection pool
│   ├── migrate.go              # Schema migrations (users, projects, builds, etc.)
│   ├── gitea.go                # Gitea API client
│   ├── models_*.go             # Data models (user, project, build, session, envvar)
│   ├── handlers_*.go           # HTTP handlers (auth, dashboard, projects, builds, etc.)
│   ├── middleware.go            # Session, auth, CSRF, ownership middleware
│   ├── context.go              # Request context helpers
│   ├── templates.go            # Template engine
│   ├── build_stream.go         # SSE build log broadcaster
│   ├── templates/              # HTML templates (layout, auth, dashboard, projects)
│   ├── static/                 # CSS, htmx, SSE extension
│   └── traefik-dashboard.yml   # Traefik file provider for dashboard routing
├── cli/
│   └── kc                      # Bash CLI tool
├── validate.sh                 # Automated setup validation
├── test-dashboard.sh           # Dashboard smoke test
└── docs/
    └── superpowers/
        ├── specs/              # Design specifications
        └── plans/              # Implementation plans
```

## Why

South African data sovereignty. This runs on **your** hardware — no AWS, no Google, no Azure. The PoC demonstrates the core deploy pipeline on a single machine. The production version targets rack servers at Teraco JHB.

## License

MIT
