# ZZIRA app runtime

ZZIRA apps are installed per workspace by a site administrator. The runtime
accepts a small JSON descriptor, encrypts the app's shared secret with
`ZZIRA_IDENTITY_ENCRYPTION_KEY`, persists granted scopes and modules, and keeps
installation, suspension, upgrade and uninstall events in both the app
lifecycle ledger and organization audit log.

```json
{
  "key": "example.operations",
  "name": "Operations companion",
  "baseUrl": "https://apps.example.com/operations",
  "version": "1.0.0",
  "scopes": ["read:jira-work", "read:app-storage", "write:app-storage", "manage:webhooks"],
  "modules": [
    {
      "key": "operations-page",
      "type": "jira:globalPage",
      "location": "jira.navigation",
      "title": "Operations companion",
      "body": "Incident and release context supplied by the app."
    }
  ],
  "lifecycle": {
    "installed": "/lifecycle/installed",
    "disabled": "/lifecycle/disabled",
    "uninstalled": "/lifecycle/uninstalled"
  },
  "webhooks": [
    {
      "key": "issue-events",
      "url": "/webhooks/issues",
      "events": ["jira:issue_created", "jira:issue_updated"],
      "jql": "project = OPS"
    }
  ],
  "scheduledTriggers": [
    {"key": "hourly-sync", "url": "/scheduled/hourly", "interval": "hour"}
  ]
}
```

Supported scopes are `read:jira-work`, `write:jira-work`,
`read:confluence-content`, `write:confluence-content`, `read:app-storage`,
`write:app-storage`, and `manage:webhooks`. Supported module contracts are Jira
global pages, issue panels and dashboard gadgets, plus Confluence global pages
and content byline items. Active global pages join product navigation, issue
panels join visible work-item views, app gadgets can be added and positioned on
custom dashboards, and byline items join published wiki pages. Module text is
host-rendered and escaped.

Every app request includes:

- `X-Zzira-App-Key`: the installed descriptor key when calling product APIs;
- `X-Zzira-App-Timestamp`: Unix seconds within five minutes of the server;
- `X-Zzira-App-Request-Id`: a unique 8–128 character idempotency key;
- `X-Zzira-App-Signature`: lowercase hexadecimal HMAC-SHA256.

The signed bytes are:

```text
timestamp + "\n" + requestId + "\n" + upper(method) + "\n" + requestTarget + "\n" + hex(sha256(body))
```

`requestTarget` is the escaped path followed by `?` and the original raw query
when one is present, so filters and pagination are signed as well as the body.

Existing Connect clients may authenticate product and app-storage requests with
`Authorization: JWT <token>` instead of ZZIRA headers. ZZIRA accepts HS256 app
tokens with mandatory `iss`, `iat`, `exp`, and `qsh` claims, derives the app key
from `iss`, opens that installation's encrypted shared secret, and verifies the
signature before trusting any claim. QSH validation follows Connect canonical
method, product-context path, sorted query/form parameter, repeated-value and
percent-encoding rules. Context JWTs are not accepted on product APIs. The
verified request still receives the installation's normal product scopes and
stable app principal.

The runtime stores each valid request ID for 24 hours and rejects replay before
executing a signed request. Lifecycle callbacks use
`POST /apps/{appKey}/lifecycle/{enabled|disabled|upgraded|uninstalled}`. Upgrade
bodies are complete descriptors. An app may remove scopes during a signed
upgrade; added scopes require a new administrator-authorized installation.

Isolated JSON storage uses `GET`, `PUT`, and `DELETE`
`/apps/{appKey}/storage/{key}`. Reads require `read:app-storage`; writes and
deletes require `write:app-storage`. Values carry a monotonically increasing
version, and uninstall deletes the app's storage, scopes and rendered modules.

Descriptors can declare relative outbound callback paths. Lifecycle supports
`installed`, `enabled`, `disabled`, `upgraded`, and `uninstalled`. Webhooks
support issue create/update/delete, comment create/delete and attachment create
events; declaring them requires `manage:webhooks`. Each webhook begins at its
installation or upgrade watermark, may carry a JQL filter, and advances through
the workspace action stream transactionally. Scheduled triggers support
`fiveMinute`, `hour`, `day`, and `week`, with no more than five schedules and
one five-minute schedule in an app.

The outbound worker sends JSON with `POST` to the descriptor base URL plus the
relative callback path. It uses the same timestamp, request ID and HMAC headers
described above, adds `X-Zzira-App-Key` and `X-Zzira-App-Event`, and signs the
callback path and raw query. Lifecycle and webhook failures retry durably with
bounded exponential backoff and stop after five attempts. A failed scheduled
invocation is terminal; the next configured occurrence still runs. Database
row claims and a recovery lease make delivery safe across restarts and multiple
server replicas. Reinstallation clears callbacks left by the old credential
generation. Site administrators can inspect callback declarations, the next
scheduled time and the five most recent delivery states and errors.

Each installation owns a stable `app_principal_*` account. A signed request to
Jira REST v3, Agile REST, Jira Service Management REST, Confluence REST v1, or
Confluence REST v2 runs as that account after the runtime verifies
`X-Zzira-App-Key`. Safe HTTP methods require the corresponding
`read:jira-work` or `read:confluence-content` grant; mutations require the
matching `write:*` grant. The app account follows the same workspace, issue
security, page restriction and command-audit paths as other callers. It is
disabled on uninstall and restored with the same ID on an authorized
reinstallation.

The runtime is a ZZIRA execution contract for remotely hosted apps. Atlassian
Full Connect descriptor translation, Forge-hosted compute, remote iframes,
workflow modules, custom fields and upgrade migrations remain separate future
slices.
