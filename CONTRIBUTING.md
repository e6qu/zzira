# Contributing

ZZIRA reimplements Atlassian's products. The rule that decides most questions:
**do what Atlassian does.** Paths, request and response bodies, status codes,
permissions, wording on screen — if Jira, Jira Service Management or Confluence
behaves a certain way, so do we. Where Atlassian publishes no specification, use
its documented and observed behavior; a missing specification is not a reason to
invent something different, and not a reason to skip the surface.

## Set up

You need Go (see `go.mod`), Docker or Podman for Postgres, and Node for the
browser tests.

```bash
docker compose up -d postgres
make seed          # migrations and a demo@zzira.dev account
make demo          # the Northwind demo company, with three months of history
make dev           # http://localhost:8080
```

Two databases are in play: the one the server uses (`DATABASE_URL`) and the one
the Go tests use (`TEST_DATABASE_URL`). Keep them apart, or a test run will
delete what you were looking at.

```bash
export TEST_DATABASE_URL='postgres://zzira:zzira@localhost:5433/zzira_gotest?sslmode=disable'
go test ./...                                   # unit and integration
cd e2e && npm i && npx playwright install chromium && npm test    # browser journeys
```

## How the code is arranged

- **`internal/commands` is the only way to write.** REST handlers, browser
  pages, automation, apps and bulk tasks all call it. Permission checks,
  validation and the action log live there, so a rule written once applies
  everywhere. A handler that writes to `internal/store` directly is a bug
  unless the store call is itself the command.
- **State and its action commit in one transaction.** The action log is what
  browsers sync and what reports read; a write that skips it is invisible to
  both.
- **Internal ids never reach a client.** Clients see Jira's numeric ids; see
  [docs/WIRE_IDS.md](docs/WIRE_IDS.md).
- **The renderer is shared.** `internal/render` compiles for the server and for
  WebAssembly, so a page renders the same offline.

## Making a change

1. **Check it is really missing.** Documents have been wrong in both directions;
   `grep` before building. Correcting a document that claims something is
   missing is legitimate work on its own.
2. **Build the whole slice**: schema, command, REST, browser page, permissions,
   the action log, tests, and the docs that own the surface.
3. **Prove it.** A Go test for the rule, and a Playwright journey when a person
   can see it. Tests assert behavior, not implementation.
4. **Keep the ledgers true.** Update the owning page in `docs/`, the status row
   in [docs/CLOUD_PARITY.md](docs/CLOUD_PARITY.md), and delete the item from
   [PLAN.md](PLAN.md) when it ships.
5. **Run everything**: `go test ./...`, `go vet ./...`, `gofmt -l`,
   `golangci-lint run`, the wasm build (`make build`), and the browser suite.

## Style

- Write for a reader who does not know the codebase: short sentences, plain
  words, no marketing.
- Comments say **why**, not what. Name the Atlassian behavior a rule comes from.
- Error messages are for the person who hit them, and match Jira's wording where
  Jira has one.
- Match the surrounding code: naming, structure, comment density.

## Demo data

The demo company is declared in [demo/company.json](demo/company.json) and
applied by `-mode=demo`. If your change needs data to be visible — a report, a
board, a portal — add it to the scenario rather than writing a one-off script:
see [docs/DEMO_DATA.md](docs/DEMO_DATA.md).

## Licence

Contributions are accepted under AGPL-3.0-or-later, the licence in
[LICENSE](LICENSE). By opening a pull request you agree your contribution is
licensed that way.
