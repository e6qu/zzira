# ZZIRA end-to-end tests (Playwright)

These browser specs prove the persona journeys listed in [docs/UI_PARITY.md](../docs/UI_PARITY.md). Each spec starts at the persona's entry point and ends at the outcome the persona can see.

## Run

Start from a freshly migrated and seeded database. The server embeds its templates and static files, so rebuild it after any UI change.

```sh
make build                     # server and wasm worker (bin/, web/static/)
make dev                       # Postgres on :5433, migrate, seed, server on :8080
cd e2e
npm ci
npx playwright install chromium
npx playwright test            # whole suite
npx playwright test backlog.spec.ts   # one spec
```

**What the harness does**
- Playwright starts `fake-hydra.mjs` on `127.0.0.1:8100`. It stands in for an OIDC identity provider in the sign-in and sign-out specs.
- Specs run one at a time (`workers: 1`) in Chromium. CI retries a failing spec twice.

**Credentials**
- `-mode=seed` creates the demo users `demo@zzira.dev` / `demo1234`.
- It also writes their API tokens to `data/seed-tokens.json` (under `DATA_DIR` if set).
- Specs call the REST API with those tokens. Set `ZZIRA_API_TOKEN` to override them.

**Environment**

| Variable | Purpose |
|---|---|
| `ZZIRA_URL` | Server to test (default `http://localhost:8080`) |
| `ZZIRA_API_TOKEN` | API token for the demo user, instead of `data/seed-tokens.json` |
| `ZZIRA_EXTERNAL_URL` | The server's external URL, for the identity provider specs |

CI (`.github/workflows/ci.yml`) runs the whole suite against a server configured with `ZZIRA_ALLOW_INSECURE_OIDC=true` and test Atlassian client credentials. Traces from failed tests are kept in `test-results/`.

## Specs

| Area | Specs |
|---|---|
| Sign-in, shell, offline | `v0`, `identity-providers`, `session-isolation`, `revocation`, `v5`, `directories` |
| Work items and search | `create`, `v1`, `v2`, `v3`, `triage`, `resolution`, `filters`, `issue_mentions`, `people` |
| Jira Software | `backlog`, `v4`, `projects`, `software`, `timeline`, `plans_view`, `plans_teams`, `releases` |
| Reports and dashboards | `reports_flow`, `reports_progress`, `report_subscriptions`, `dashboards`, `dashboard_reports`, `dashboard_subscriptions`, `v6` |
| Administration | `admin`, `global_permissions`, `issue_metadata`, `hierarchy`, `project_roles`, `permission_schemes`, `permission_helper`, `notification_schemes`, `notification_helper`, `notification_preferences`, `notifications`, `issue_security_schemes`, `screens`, `screen_schemes`, `field_configurations`, `custom_field_contexts`, `custom_field_options`, `classification_levels` |
| Automation and apps | `automation`, `apps` |
| Service Management | `service`, `service_assets` |
| Confluence | `wiki`, `wiki_content_tree`, `wiki_database`, `wiki_drafts_purge`, `wiki_live_editing`, `wiki_mentions`, `wiki_page_details`, `wiki_page_lifecycle`, `wiki_presence`, `wiki_space_admin`, `wiki_space_tools`, `wiki_watches`, `wiki_whiteboard` |
| Cross-cutting | `accessibility` ([ACCESSIBILITY.md](../docs/ACCESSIBILITY.md)), `wire-ids` ([WIRE_IDS.md](../docs/WIRE_IDS.md)) |

Each name is `<name>.spec.ts` in this directory.
