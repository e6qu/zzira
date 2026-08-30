# ZZIRA E2E (Playwright)

These specs are the automated version of each slice's demo script.

## Run

```bash
make dev            # postgres + migrate + seed + server on :8080
make build          # server + wasm worker into bin/ and web/static/
cd e2e
npm install
npx playwright install chromium
npm test
```

Set `ZZIRA_URL` to target a non-default instance.

## Specs (V0)

| Spec | Proves |
|---|---|
| API contract smoke | `serverInfo` + `POST /rest/api/3/issue` return Jira shapes |
| UI create | login → create dialog → HX-Redirect to `/browse/ZZ-N` |
| Worker boots | wasm worker posts `ready` and syncs (banner text) |
| Offline reload | service worker serves the cached shell; the wasm worker re-renders the issue from local SQLite with the network cut |
| Two-browser convergence | issue created through the REST edge appears in an independent browser via `/sync` |
