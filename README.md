# ZZIRA

A self-hosted Jira Cloud, Jira Software, Jira Service Management and Confluence
Cloud. The UI and REST APIs behave like Atlassian's, so existing clients work by
changing their base URL. The architecture follows Linear's
[delta sync](https://linear.app/now/rebuilding-delta-sync-read-path): one command
layer writes an immutable action log, and browsers keep a permission-shaped local
replica.

- **Go + PostgreSQL** server: stateless replicas, the action log as the only write
  path, `LISTEN/NOTIFY` for live updates.
- **Local-first browser**: SQLite (WASM/OPFS) and one Go HTML renderer compiled
  for both server and browser; access is checked before replay, and revoked data
  is purged.
- **Frontend**: server-rendered HTML with HTMX and SortableJS.
- **Sign-in**: password, API tokens, and OIDC (Google, Microsoft Entra ID,
  Atlassian, any custom provider) — see [identity providers](docs/shauth-sso.md).

Status: [compatibility ledger](docs/CLOUD_PARITY.md). Remaining work:
[PLAN.md](PLAN.md). All docs: [docs index](docs/README.md).

## Quickstart (Docker)

```bash
docker compose up -d --build
docker compose exec zzira /zzira-server -mode=migrate
docker compose exec zzira /zzira-server -mode=seed   # prints a demo API token
```

## Quickstart (local Go)

```bash
docker compose up -d postgres
make seed      # applies migrations, prints a demo API token
make dev       # serves on :8080
```

Sign in as `demo@zzira.dev` / `demo1234`. Use the printed token for Basic auth:

```bash
curl -u demo@zzira.dev:<token> -X POST localhost:8080/rest/api/3/issue \
  -H 'Content-Type: application/json' \
  -d '{"fields":{"project":{"key":"ZZ"},"summary":"Hello","issuetype":{"name":"Task"}}}'
```

## Tests

```bash
go test ./...        # set TEST_DATABASE_URL for the Postgres tests
make build           # server and wasm builds
make loadtest        # sync latency by workspace size (docs/loadtest.md)
cd e2e && npm i && npx playwright install chromium && npm test   # see e2e/README.md
```

## Layout

| Path | Contents |
|---|---|
| `cmd/server` | HTTP server, routes, background workers |
| `cmd/client` | Browser sync worker (wasm) |
| `internal/commands` | The one mutation layer |
| `internal/store` | PostgreSQL access |
| `internal/api3`, `internal/agile`, `internal/confluence`, `internal/admin` | REST APIs |
| `internal/web`, `internal/render` | Browser UI; the shared renderer |
| `internal/automation`, `internal/apps` | Automation engine; app runtime |
| `migrations/` | Schema |
| `api/` | Pinned Atlassian specs and conformance data |
| `e2e/` | Playwright journeys |
| `docs/` | Surface reference ([index](docs/README.md)) |

## Author

[Adrian Mârza](https://www.linkedin.com/in/adrian-m%C3%A2rza-52606512a/)
