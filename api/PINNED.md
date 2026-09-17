# Pinned dependencies and contracts

| Pin | Version | Source |
|---|---|---|
| SQLite WASM (`web/static/sqlite/`) | 3.53.4 | sqlite.org/2026/sqlite-wasm-3530400.zip |
| htmx (`web/static/htmx/`) | 2.0.4 | unpkg.com/htmx.org@2.0.4 |
| Atlassian Cloud OpenAPI specs (`api/specs/`) | per `api/specs/pins.json` | dac-static.atlassian.com |

`api/specs/pins.json` records each spec's contract family, host class, source
URL, retrieval date, version and SHA-256. The vendored specs cover Jira
Platform v3, Jira Software, Jira Service Management, Confluence v1 and v2,
Automation, and organization administration: 1,207 operations in total.

CI (`.github/workflows/ci.yml`) runs these checks:

```sh
python3 -m unittest api/conformance/test_inventory.py api/conformance/test_coverage.py
python3 api/conformance/inventory.py --check          # pins, checksums, operation ids
python3 api/conformance/coverage.py --check           # assessments -> cloud-coverage.json
python3 api/conformance/app_scopes.py --check         # per-operation Connect scopes
python3 api/conformance/anonymous_operations.py --check
```

The inventory (`cloud-operations.json`) and the reviewed coverage
(`cloud-coverage.json`) are kept separate. Neither one certifies semantic
conformance. See [MATRIX.md](conformance/MATRIX.md) and
[CLOUD_PARITY.md](../docs/CLOUD_PARITY.md).
