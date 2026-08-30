# Security Policy

## Reporting a vulnerability

Do **not** open a public issue for security vulnerabilities.
Use GitHub's **private vulnerability reporting** (Security → Report a
vulnerability) or contact the maintainer directly. Reports are triaged within
7 days.

## Supported surface

- `cmd/server` — HTTP edges (`/rest/api/3`, `/rest/agile/1.0`, web)
- `cmd/client` — browser sync worker (wasm)
- All permission decisions live in `internal/authz`; the action log is the
  only write path.

## Hard rules (enforced in review)

No fallbacks, no deferrals, no dead code, no swallowed errors — see PLAN.md.

## Automated coverage

- CodeQL (go + javascript, security-extended) — every PR + weekly
- gosec SAST (SARIF → Code Scanning)
- govulncheck (Go stdlib/module vulnerabilities)
- npm audit (e2e toolchain)
- Dependabot (gomod, npm, github-actions)
- GitHub secret scanning / push protection (repo settings)
