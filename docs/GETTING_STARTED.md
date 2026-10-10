# Run ZZIRA locally

Use a minimal seeded site to explore the UI or run browser tests. Add the
[Northwind demo company](DEMO_DATA.md) when you want a populated site with
history. Current product coverage and gaps are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Docker Compose

From the repository root:

```sh
docker compose up -d --build
docker compose exec zzira /zzira-server -mode=seed
```

Open <http://localhost:8080> and sign in as `demo@zzira.dev` / `demo1234`.
PostgreSQL data is kept in the `pgdata` volume. The server applies migrations
when it starts; a separate migration command is unnecessary.

For a populated site, also run:

```sh
docker compose exec zzira /zzira-server -mode=demo
```

Compose uses `DATA_DIR=/data` inside the server container. Its credential files
and uploaded files are in that container, not the repository's `data/` folder.
The current Compose configuration persists PostgreSQL only; mount `/data` if
you need those files to survive server-container replacement.

## Run from source

Use the Go version in [go.mod](../go.mod), Docker Compose for PostgreSQL, and
Node.js 24 LTS for browser tests. Compose uses PostgreSQL 17.11; existing
PostgreSQL 17 development volumes remain compatible.

```sh
docker compose up -d --wait postgres
make build
make dev
```

`make build` compiles the server and browser worker and copies the WASM runtime
assets. `make dev` migrates, seeds the minimal site and starts the server on
<http://localhost:8080>. Rebuild after changing embedded templates or shared Go
renderer code; restart the server to use the new templates.

To add Northwind, stop the server, run `make demo`, then run `make dev` again.
Repeated demo runs add missing scenario data and preserve existing edits; see
[DEMO_DATA.md](DEMO_DATA.md) for workspace selection and credentials.

## Credentials and configuration

| Setting or file | Purpose |
|---|---|
| `DATABASE_URL` | Server database; default `postgres://zzira:zzira@localhost:5433/zzira?sslmode=disable` |
| `WORKSPACE_SLUG` | Required when starting the server directly; Compose and `make dev` set `zzira` |
| `SERVER_PORT` | Source-run HTTP port; default `8080` |
| `DATA_DIR` | Files and local credential artifacts; default `data` for source runs |
| `seed-tokens.json` under `DATA_DIR` | Seeded users' API tokens and passwords, used by the browser harness |
| `demo-credentials.json` under `DATA_DIR` | Northwind accounts, passwords and API tokens |

Seed tokens are written to the local file with restricted permissions, never
printed to stdout. Each seed run creates fresh tokens and rewrites the file.
For Basic auth, use the account email and its token from that file:

```sh
curl --user 'demo@zzira.dev:YOUR_API_TOKEN' http://localhost:8080/rest/api/3/myself
```

For identity-provider setup and an instance that disables local credentials,
see [Identity provider sign-in](shauth-sso.md).

## Tests and troubleshooting

- [CONTRIBUTING.md](../CONTRIBUTING.md#validation) owns Go, lint and build checks.
- [e2e/README.md](../e2e/README.md) owns browser setup, credentials and journeys.
- A browser suite needs a freshly seeded database. `make reset` destroys the
  selected development database; use a disposable test instance rather than a
  site whose data you want to keep.
- If browser rendering differs from the server, rerun `make build` and restart
  the server. This refreshes both the shared templates and WASM assets.
- If the demo appears empty, check `WORKSPACE_SLUG` against the scenario's
  target workspace ([DEMO_DATA.md](DEMO_DATA.md#which-workspace-it-builds-into)).
