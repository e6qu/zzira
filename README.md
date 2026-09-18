# ZZIRA

A self-hosted Jira Cloud, Jira Software, Jira Service Management and Confluence
Cloud. The screens and the REST APIs behave like Atlassian's, so a client works
by pointing at your own server instead of `*.atlassian.net`. Inside, it follows
Linear's [delta sync](https://linear.app/now/rebuilding-delta-sync-read-path)
architecture: one command layer writes an immutable action log, and every
browser keeps a permission-shaped local replica.

- **Jira** — projects, work types on a configurable hierarchy, fields, screens,
  workflows, permissions, JQL, bulk operations.
- **Jira Software** — boards, backlogs, sprints, releases, plans, reports and
  delivery (DORA) metrics.
- **Jira Service Management** — portals, request types, queues, SLAs,
  approvals, customers and satisfaction.
- **Confluence** — spaces, pages, blog posts, comments, whiteboards, databases
  and CQL search.
- **Automation and apps** — rules with triggers, conditions and actions; signed
  Connect-compatible apps.

## Try it

```bash
docker compose up -d --build            # Postgres and the server
docker compose exec zzira /zzira-server -mode=migrate
docker compose exec zzira /zzira-server -mode=demo   # a company with three months of history
```

Or run it from source against a local Postgres:

```bash
docker compose up -d postgres
make seed      # migrations plus a demo@zzira.dev account
make demo      # the Northwind demo company
make dev       # serve on http://localhost:8080
```

`-mode=demo` builds **Northwind**: three projects, a board with closed and
active sprints, two shipped releases, ninety days of deployments and incidents,
a service desk with customers and satisfaction ratings, and the knowledge base
the teams wrote. The people, their passwords and API tokens are written to
`data/demo-credentials.json`. Everything it builds is declared in
[demo/company.json](demo/company.json) — change it, run the mode again, and you
have your own company: a second run raises only what the scenario has gained
and leaves the rest as it is.

It builds into the workspace the instance serves — `-workspace`, else
`WORKSPACE_SLUG`, else the scenario's own slug — so seeding a deployment lands
where the server will show it: see [docs/DEMO_DATA.md](docs/DEMO_DATA.md).

```bash
WORKSPACE_SLUG=acme go run ./cmd/server -mode=demo
```

An instance published behind single sign-on should set
`ZZIRA_LOCAL_CREDENTIALS=off` before it is seeded: the passwords and API tokens
the demo mints are then not a way in, and only the identity provider's sessions
are accepted ([docs/shauth-sso.md](docs/shauth-sso.md#single-sign-on-only)).

With `make seed`, sign in as `demo@zzira.dev` / `demo1234`. The seeded API token
is printed once; use it for Basic auth:

```bash
curl -u demo@zzira.dev:<token> -X POST localhost:8080/rest/api/3/issue \
  -H 'Content-Type: application/json' \
  -d '{"fields":{"project":{"key":"ZZ"},"summary":"Hello","issuetype":{"name":"Task"}}}'
```

## Where to go next

| You want to | Start here |
|---|---|
| See what works and what doesn't | [docs/CLOUD_PARITY.md](docs/CLOUD_PARITY.md) |
| Read about one surface | [docs index](docs/README.md) |
| Know what is being built next | [PLAN.md](PLAN.md) |
| Change the demo company | [docs/DEMO_DATA.md](docs/DEMO_DATA.md) |
| Contribute | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Point an existing client at it | [docs/CLOUD_PARITY.md](docs/CLOUD_PARITY.md#compatibility-promise) |

## Layout

| Path | Contents |
|---|---|
| `cmd/server` | HTTP server, routes, background workers |
| `cmd/client` | Browser sync worker (WebAssembly) |
| `internal/commands` | The one mutation layer: every write goes through it |
| `internal/store` | PostgreSQL access and the action log |
| `internal/api3`, `internal/agile`, `internal/confluence`, `internal/admin` | The REST APIs |
| `internal/web`, `internal/render` | Browser pages; the renderer shared by server and browser |
| `internal/automation`, `internal/apps` | Automation engine; app runtime |
| `internal/demo` | The declarative demo scenarios |
| `migrations/` | Schema |
| `api/` | Pinned Atlassian specifications and conformance evidence |
| `e2e/` | Playwright journeys |
| `docs/` | Reference for every surface ([index](docs/README.md)) |

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE) and [NOTICE](NOTICE). If you run a
modified copy as a network service, the licence asks you to offer its source to
the people using it.

ZZIRA is an independent reimplementation. Jira, Jira Service Management,
Confluence and Atlassian are trademarks of Atlassian Pty Ltd, which does not
endorse this project.

## Author

[Adrian Mârza](https://www.linkedin.com/in/adrian-m%C3%A2rza-52606512a/)
