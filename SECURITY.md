# Security policy

## Reporting a vulnerability

Do not open a public issue for a vulnerability. Use GitHub's private vulnerability reporting (Security › Report a vulnerability) or contact the maintainer directly. Reports are triaged within 7 days.

## Supported surface

- `cmd/server`: every HTTP edge, including the Jira, Jira Software, Jira Service Management and Confluence REST APIs, the organization admin API (`/admin/v1`, `/admin/v2`), sign-in (`/login`, `/auth/...`), sync, and the web UI.
- `cmd/client`: the browser sync worker (wasm).

## Design rules

- **Permission decisions** live in `internal/authz` and in the PostgreSQL functions it and the store call: `jira_has_project_permission` for project permissions and `jira_issue_security_visible` for issue security.
- **Writes** record an immutable action in the same transaction as the state change; the action log is what sync (`GET /sync`) replays to browsers.
- **Enforced in review:** no fallbacks, no deferrals, no dead code, no swallowed errors.

See [docs/ADMIN.md](docs/ADMIN.md) for authorization roles and IP allowlists, [docs/shauth-sso.md](docs/shauth-sso.md) for sign-in, and [docs/PERMISSION_SCHEMES.md](docs/PERMISSION_SCHEMES.md) for known permission gaps.

## Automated checks

On every pull request to `main`:

| Check | Where |
| --- | --- |
| CodeQL (`go`, `javascript-typescript`, `security-extended`) | `.github/workflows/codeql.yml` |
| Semgrep (`p/golang`, `p/secrets`, `p/xss`) | `.github/workflows/sast.yml` |
| gosec (SARIF to code scanning) | `.github/workflows/ci.yml` |
| govulncheck | `.github/workflows/ci.yml` |
| `npm audit --audit-level=high` (e2e toolchain) | `.github/workflows/ci.yml` |

Dependabot updates Go modules, npm packages and GitHub Actions (`.github/dependabot.yml`). GitHub secret scanning and push protection are enabled in the repository settings.
