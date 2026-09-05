Pinned SQLite WASM: 3.53.4 (sqlite.org/2026/sqlite-wasm-3530400.zip)
Pinned htmx: 2.0.4 (unpkg.com/htmx.org@2.0.4)

Atlassian Cloud OpenAPI specifications are pinned in `api/specs/pins.json`
with source URL, retrieval date, version and SHA-256. Vendored documents cover
Jira platform v3, Jira Software and Confluence v2 (940 operations at this pin).

Run `python3 api/conformance/inventory.py --check` to verify checksums and the
complete generated operation inventory. This is a reproducible contract scope,
not semantic conformance certification. See `docs/CLOUD_PARITY.md`.
