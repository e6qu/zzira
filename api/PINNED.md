Pinned SQLite WASM: 3.53.4 (sqlite.org/2026/sqlite-wasm-3530400.zip)
Pinned htmx: 2.0.4 (unpkg.com/htmx.org@2.0.4)

Atlassian Cloud OpenAPI specifications are pinned in `api/specs/pins.json`
with contract family, host class, source URL, retrieval date, version and
SHA-256. Vendored documents cover Jira Platform v3, Jira Software, Jira Service
Management, Confluence v1 and v2, Automation, and organization administration
(1,207 operations at this pin).

Run the two Python conformance tests, then
`python3 api/conformance/inventory.py --check` and
`python3 api/conformance/coverage.py --check`. They verify pin metadata,
checksums, versions, stable operation identities, reviewed coverage evidence,
and generated files. Contract inventory and delivered coverage remain separate;
neither alone is semantic conformance certification. See
`docs/CLOUD_PARITY.md`.
