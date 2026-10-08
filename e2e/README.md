# ZZIRA end-to-end tests (Playwright)

These browser specs prove the persona journeys listed in [docs/UI_PARITY.md](../docs/UI_PARITY.md). Each spec starts at the persona's entry point and ends at the outcome the persona can see.

## Run

Use [Run ZZIRA locally](../docs/GETTING_STARTED.md) to prepare a disposable
instance. Browser journeys require a freshly migrated and seeded database;
reusing a previous run's data can invalidate fixture assumptions. Keep the
server database separate from `TEST_DATABASE_URL` used by Go tests.

```sh
# Repository root; stop the development server before resetting its database.
make build
make reset
make dev
```

In another terminal:

```sh
cd e2e
npm ci
npx playwright install chromium
npx playwright test
# Or run the journey you changed:
npx playwright test backlog.spec.ts
```

`make reset` destroys the selected development database. Never use it against
a site whose data you want to keep. Rebuild and restart after changing embedded
templates; `make build` refreshes both server and browser WASM assets.

**What the harness does**

- Playwright starts the fake OIDC provider (`fake-hydra.mjs`, port 8100) and
  SAML provider (`fake-saml-idp.mjs`, port 8200). Local runs can reuse them.
- Specs run one at a time (`workers: 1`) in Chromium. CI retries a failing spec twice.

**Credentials**

Specs use [auth.ts](auth.ts) to read the tested instance's seeded tokens.
`ZZIRA_API_TOKEN` overrides the demo account only. Journeys involving other
personas also need `ZZIRA_SEED_TOKENS` or the default `data/seed-tokens.json`.
Credential artifacts are described in the [setup guide](../docs/GETTING_STARTED.md#credentials-and-configuration).

**Environment**

| Variable | Purpose |
|---|---|
| `ZZIRA_URL` | Server to test (default `http://localhost:8080`) |
| `ZZIRA_API_TOKEN` | API token for the demo user, instead of `data/seed-tokens.json` |
| `ZZIRA_SEED_TOKENS` | Path to the tested instance's credentials file, including other personas' tokens and seeded passwords |
| `ZZIRA_EXTERNAL_URL` | The server's external URL, for the identity provider specs |

CI (`.github/workflows/ci.yml`) runs the whole suite against a server configured with `ZZIRA_ALLOW_INSECURE_OIDC=true` and test Atlassian client credentials. Traces from failed tests are kept in `test-results/`.

`make e2e` builds assets and runs the suite against an already running server.
Restart that server if its embedded templates changed; the build alone does not
update a running process.

## Find a journey

[docs/UI_PARITY.md](../docs/UI_PARITY.md) maps personas to the specs that prove
their journeys. Each evidence name there is `<name>.spec.ts` in this directory.
List the current tests from `e2e/` without starting a server:

```sh
npx playwright test --list
```
