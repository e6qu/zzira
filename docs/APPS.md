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
  "scopes": ["read:jira-work", "read:app-storage", "write:app-storage"],
  "modules": [
    {
      "key": "operations-page",
      "type": "jira:globalPage",
      "location": "jira.navigation",
      "title": "Operations companion",
      "body": "Incident and release context supplied by the app."
    }
  ]
}
```

Supported scopes are `read:jira-work`, `write:jira-work`,
`read:confluence-content`, `write:confluence-content`, `read:app-storage`,
`write:app-storage`, and `manage:webhooks`. Supported module contracts are Jira
global pages, issue panels and dashboard gadgets, plus Confluence global pages
and content byline items. This checkpoint host-renders global navigation pages;
the other validated registrations are retained for their host surfaces.

Every app request includes:

- `X-Zzira-App-Timestamp`: Unix seconds within five minutes of the server;
- `X-Zzira-App-Request-Id`: a unique 8–128 character idempotency key;
- `X-Zzira-App-Signature`: lowercase hexadecimal HMAC-SHA256.

The signed bytes are:

```text
timestamp + "\n" + requestId + "\n" + upper(method) + "\n" + escapedPath + "\n" + hex(sha256(body))
```

The runtime stores each valid request ID for 24 hours and rejects replay before
executing a lifecycle or storage mutation. Lifecycle callbacks use
`POST /apps/{appKey}/lifecycle/{enabled|disabled|upgraded|uninstalled}`. Upgrade
bodies are complete descriptors. An app may remove scopes during a signed
upgrade; added scopes require a new administrator-authorized installation.

Isolated JSON storage uses `GET`, `PUT`, and `DELETE`
`/apps/{appKey}/storage/{key}`. Reads require `read:app-storage`; writes and
deletes require `write:app-storage`. Values carry a monotonically increasing
version, and uninstall deletes the app's storage, scopes and rendered modules.

The runtime is a ZZIRA execution contract for locally hosted apps. Atlassian
Connect JWT/QSH descriptors, Forge-hosted compute, remote iframes, webhooks,
scheduled triggers, workflow modules and custom fields remain separate future
slices.
