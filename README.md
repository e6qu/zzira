# ZZIRA

A Jira-style issue tracker rebuilt on the delta-sync architecture from
Linear's ["Rebuilding delta sync read path"](https://linear.app/now/rebuilding-delta-sync-read-path).

- **Targets Jira Cloud compatibility**: implemented subsets of REST v3 and Agile 1.0, plus initial Confluence Cloud v2 wiki APIs. Full base-URL-only compatibility is not yet achieved — see [scope and gaps](docs/CLOUD_PARITY.md).
- **Local-first browser replica**: SQLite (WASM/OPFS) + an isomorphic Go renderer compiled to both server and client
- **Go + Postgres backend**: stateless replicas, an immutable action log as the single write path, Postgres LISTEN/NOTIFY for live pokes
- Frontend: HTMX + SortableJS, Jira-like UI
- **Optional OIDC SSO**: ShAuth reference-provider configuration with Discovery,
  Authorization Code + PKCE, verified claims, and server-side sessions — see
  [ShAuth SSO](docs/shauth-sso.md)

The full architecture, hard rules, slice history, and scaling story live in [PLAN.md](PLAN.md).

## Quickstart (Docker)

```bash
docker compose up -d --build   # postgres + scratch-based zzira container
docker compose exec zzira /zzira-server -mode=migrate
docker compose exec zzira /zzira-server -mode=seed   # prints demo API token
```

## Quickstart (local Go)

```bash
docker compose up -d postgres
make seed      # applies migrations, prints demo API token
make dev       # runs the server on :8080
```

Log in as `demo@zzira.dev` / `demo1234`. The seeded API token prints once —
use it for Basic auth against `/rest/api/3/...`:

```bash
curl -u demo@zzira.dev:<token> -X POST localhost:8080/rest/api/3/issue \
  -H 'Content-Type: application/json' \
  -d '{"fields":{"project":{"key":"ZZ"},"summary":"Hello","issuetype":{"name":"Task"}}}'
```

## Tests

```bash
go test ./...                       # unit + integration (TEST_DATABASE_URL for Postgres tests)
make build                          # server + wasm worker builds (wasm = CI gate)
make loadtest                       # /sync p50/p95 report vs workspace size
cd e2e && npm i && npx playwright install chromium && npm test   # browser specs
```

## Layout

`PLAN.md` (architecture + slices) · `api/` (pinned Atlassian specs + conformance) ·
`internal/render` (the one HTML renderer, server + wasm) · `internal/commands`
(the one mutation layer) · `cmd/client` (browser sync worker) · `e2e/` (Playwright).

---

## Author

Original project author: [Adrian Mârza](https://www.linkedin.com/in/adrian-m%C3%A2rza-52606512a/)
