# KumbulaCloud PoC

A mini-PaaS that runs on a single machine. Push code, get a running app with a database and a URL — no cloud providers required.

```
git push kumbula main
# 10 seconds later...
# http://my-app.kumbula.local is live
```

## How it works

```
git push ──▶ Gitea ──▶ webhook ──▶ kumbula-engine
                                        │
                                        ├── detect language (Node, Python, Go, static)
                                        ├── generate Dockerfile
                                        ├── docker build
                                        ├── provision PostgreSQL database
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
| **kumbula-engine** | Go API that orchestrates builds, deploys, and DB provisioning |
| [dnsmasq](https://thekelleys.org.uk/dnsmasq/doc.html) | Wildcard DNS for `*.kumbula.local` |

## Quick start

See [`kumbula-guide.md`](kumbula-guide.md) for the full step-by-step build guide. The short version:

```bash
# 1. Install prerequisites (Docker, Go 1.22+, dnsmasq, jq, psql)
# 2. Start infrastructure
cd ~/kumbula-poc
docker compose up -d

# 3. Configure Gitea (set INSTALL_LOCK, create admin user, get API token)
# 4. Build and start the engine
cd engine && go mod tidy && go build -o kumbula-engine . && ./kumbula-engine

# 5. Create a repo, add webhook, push code — app deploys automatically
```

## CLI

```bash
kc create <name>    # Create a new app (Gitea repo + webhook)
kc apps             # List deployed apps
kc logs <name>      # Stream app logs
kc destroy <name>   # Remove an app
```

## Validation

Run the validation script to check that everything is working:

```bash
./validate.sh
```

```
============================================
 KumbulaCloud PoC — Validation
============================================
  PASS: Docker installed (29.3.1)
  PASS: Go installed (go1.22.5)
  PASS: *.kumbula.local resolves to 192.168.1.251 (1ms)
  PASS: traefik is running
  PASS: gitea is running
  PASS: kumbula-postgres is running
  PASS: Traefik sees 4 Docker route(s)
  PASS: Gitea API responding
  PASS: Engine is healthy on :9000
  PASS: PostgreSQL connection works
  PASS: hello-world -> container running, HTTP 200 via Traefik
============================================
 Results: 15 passed, 0 failed, 0 warnings
 KumbulaCloud is ready for the demo!
```

## Project structure

```
kumbula-poc/
├── docker-compose.yml   # Traefik, Gitea, PostgreSQL
├── engine/
│   └── main.go          # Webhook handler, builder, deployer
├── cli/
│   └── kc               # Bash CLI tool
├── validate.sh          # Automated setup validation
└── kumbula-guide.md     # Full build guide with troubleshooting
```

## Why

South African data sovereignty. This runs on **your** hardware — no AWS, no Google, no Azure. The PoC demonstrates the core deploy pipeline on a single machine. The production version targets rack servers at Teraco JHB.

## License

MIT
