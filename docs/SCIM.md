# SCIM 2.0 provisioning

An identity provider creates, updates and deactivates the people and groups of
one directory through SCIM 2.0 (RFC 7643 and RFC 7644), as Atlassian Guard's
user provisioning does. Every write goes through the same directory operations
an administrator's own API uses, so a person an identity provider provisions is
the same person the site already knows, with the same audit trail. Part of
[organization administration](ADMIN.md).

## Endpoints

All under `/scim/directory/{directoryId}`.

| Method and path | Behavior |
|---|---|
| `GET /ServiceProviderConfig` | What this service supports: patch and filtering yes, bulk, sort, etag and password change no. |
| `GET /ResourceTypes` | The two resources: User and Group. |
| `GET /Schemas` | The attributes each resource carries -- only the ones this directory can hold. |
| `GET /Users` | Lists the directory's people. `filter`, `startIndex` and `count`. |
| `POST /Users` | Provisions a person. 409 when the `userName` is already in the directory. |
| `GET/PUT/PATCH/DELETE /Users/{userId}` | Reads, replaces, patches or deprovisions one person. |
| `GET /Groups` | Lists the directory's groups. `filter`, `startIndex` and `count`. |
| `POST /Groups` | Provisions a group, with its members. |
| `GET/PUT/PATCH/DELETE /Groups/{groupId}` | Reads, replaces, patches or removes one group. |

## Behavior

**Authentication.** A provider holds a bearer token of an organization
administrator, as it holds an API key in Atlassian. Anyone else gets 403, and a
directory that is not this organization's is 404.

**Identity.** `id` is the site's own account or group id. `externalId` is what
the provider calls them, kept per directory and unique within it. `userName` is
the email address; a provider that sends a `userName` which is not an address
has its primary email used instead.

**Names.** `displayName` is what the site shows. `name.givenName` and
`name.familyName` are kept as the provider sends them, and are returned with a
`formatted` name so a provider that only sends parts still reads a whole one.

**Active.** A person is active when their directory membership is. `active:
false` suspends that membership -- the same suspension an administrator applies
by hand, which ends their sessions and tokens -- and `true` restores it. An
account deactivated for its own reasons never reads as active.

**Filters.** `attribute eq "value"` on `userName`, `emails.value` or
`externalId` for people, and `displayName` or `externalId` for groups. Any
other filter is 400 with `scimType: invalidFilter`, rather than a list that
quietly ignored it.

**Paging.** `startIndex` is one-based and `count` is capped at 100, which is
what `ServiceProviderConfig` advertises.

**Patch.** `add`, `replace` and `remove`. A user patch changes `active`,
`displayName`, `name.givenName`, `name.familyName` or `externalId`. A group
patch changes `displayName`, `externalId` or `members` -- including the
`members[value eq "..."]` path a provider uses to remove one member, and a
`remove` of the whole attribute, which empties the group.

**Replace.** `PUT` on a group makes its membership exactly what the request
lists. `PUT` on a user leaves `active` alone when the request does not carry
it.

**Errors** are SCIM's own shape: the error schema, the status as a string, a
`detail`, and `scimType` where one applies.

**SCIM-managed.** A directory becomes SCIM-managed the first time a provider
writes to it, and the service management organization bean then reports
`scimManaged: true`.

## In the browser

`/admin` has a **User provisioning** section: the address to point a provider
at, the directory it writes to, whether one has written yet and when it last
did, and the people and groups it manages with the external ids it knows them
by. What SCIM does not provision -- product access -- is said there too,
because that is where somebody setting it up will read it.

**Provisioning keys** are issued there as well. A key provisions one directory
and nothing else, is shown once because only its hash is kept, records when a
provider last used it, and is revoked on its own. A provider sends it as
`Authorization: Bearer`, and every change it makes is recorded against the
person who issued it. An organization administrator's own API token is still
accepted, which is how provisioning worked before directories had keys.

## Gaps

- One directory per organization, so `{directoryId}` is that directory.
- No SCIM bulk operations, sorting or ETags; `ServiceProviderConfig` says so.
- Product access is not provisioned: a person SCIM creates joins the directory
  and its groups, and the groups carry whatever access the site gave them.
- The browser page reads what a provider has written and issues the key it
  writes with; the provider itself is still configured at its own end, by
  pointing it at the address.

## Tests

`internal/scim/scim_test.go` walks a provider's whole conversation: discovery,
creating a person and a group, filtering, paging, patching members both ways,
renaming, deactivating and restoring, and deprovisioning both.

## See also

[ADMIN.md](ADMIN.md) · [PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)
