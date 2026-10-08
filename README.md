# ZZIRA

ZZIRA reimplements Jira Cloud, Jira Software, Jira Service Management and
Confluence for a self-hosted site. It serves the pinned REST operations and
browser journeys, with compatibility still assessed as partial; see the
[coverage ledger](docs/CLOUD_PARITY.md) for evidence and gaps.

The architecture follows Linear's
[delta sync](https://linear.app/now/rebuilding-delta-sync-read-path): one command
layer writes an immutable action log, and browsers keep permission-shaped local
replicas of work items.

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

```sh
docker compose up -d --build
docker compose exec zzira /zzira-server -mode=seed
```

Open <http://localhost:8080> and sign in as `demo@zzira.dev` / `demo1234`.
For source setup, credential locations and persistent files, use the
[getting-started guide](docs/GETTING_STARTED.md).

Add the populated Northwind company with
`docker compose exec zzira /zzira-server -mode=demo`. Its projects, teams,
history and wiki content are declared in [demo/company.json](demo/company.json).
[Demo data](docs/DEMO_DATA.md) explains how to customize and reapply it.

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
