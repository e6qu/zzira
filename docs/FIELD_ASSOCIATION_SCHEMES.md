# Field association schemes

Updated: 2026-09-11

A field association scheme decides which fields a project's forms show, which
work types each field reaches, and whether the form requires it. This is Jira's
newer API over the same idea the
[field configurations](FIELD_CONFIGURATIONS.md) surface already serves.

## One model, two APIs

**A field association scheme is this product's field configuration scheme.**
Same row, same id, same projects, same per-field rules. `parameters` are the
field configuration item's `isRequired` and `description`; `restrictedToWorkTypes`
is the scheme's work-type mapping; the scheme's projects are its project
assignments.

Serving one model through both APIs is deliberate. Two parallel models would be
two answers to "does this project's form show this field", and one of them would
eventually be wrong.

## Jira Cloud REST surface

All 17 pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/config/fieldschemes` | Pages the workspace's schemes, filtered by `projectId` or a name and description `query`. |
| `POST /rest/api/3/config/fieldschemes` | Creates a scheme, starting from the workspace's default rules. |
| `GET/PUT/DELETE /rest/api/3/config/fieldschemes/{id}` | Reads, renames or removes one scheme. |
| `POST /rest/api/3/config/fieldschemes/{id}/clone` | Copies a scheme's associations into a new one. |
| `GET /rest/api/3/config/fieldschemes/{id}/fields` | Pages the fields a scheme associates, with their rules and work type restrictions. |
| `PUT/DELETE /rest/api/3/config/fieldschemes/fields` | Associates fields with schemes, or removes them, answering per item. |
| `GET /rest/api/3/config/fieldschemes/{id}/fields/{fieldId}/parameters` | Reads one field's rules and its per-work-type overrides. |
| `PUT/DELETE /rest/api/3/config/fieldschemes/fields/parameters` | Sets or removes per-work-type overrides. |
| `GET /rest/api/3/config/fieldschemes/{id}/projects` | Pages a scheme's projects. |
| `GET/PUT /rest/api/3/config/fieldschemes/projects` | Reports or sets which scheme a project uses. |
| `PUT/DELETE /rest/api/3/field/association` | Associates fields with every work type on a set of projects. |

## Writing through a scheme never changes another one

A new scheme points every work type at the workspace's default configuration,
which other schemes also use. A naive write would therefore change every project
in the workspace.

So a write clones first: when a scheme is asked to change rules on a
configuration it shares with another scheme — or on the workspace default — it
takes its own copy and remaps just that work type to the copy. The other schemes
keep the configuration they had. The test proves it: removing a field from a
clone leaves the original untouched.

The same clone happens when a work type with no mapping of its own is given a
rule. Without it, the write would land on the scheme's fallback and reach every
other work type too, and a restriction would mean nothing.

That copy has a consequence worth knowing: a work type that has been given its
own configuration no longer follows later edits to the fallback. That is how
Jira's field configuration schemes behave as well.

## What a restriction means

`restrictedToWorkTypes` replaces any previous work type association. Restricting
a field to one work type therefore hides it in the scheme's fallback and in
every other work type, not just adds it to the named one — otherwise the field
would still reach everything through the fallback.

A read reports a restriction only when it is a real subset: a field reached
through the fallback, or listed for every work type, is not restricted. A work
type whose rules match the fallback is not reported as an override either,
because it is not one.

## `PUT`/`DELETE /field/association`

These work on projects rather than schemes: the fields are associated with every
work type on the projects given. A project reaches its rules through the scheme
it uses, so a project sharing a scheme with another sees the same change — which
is what Jira documents for these operations.

Associating also widens a custom field's context to the project when the context
does not already cover it. A rule alone does not put a field on a form; the
field's context has to reach the project too. Unassociating needs no context
change, because hiding the field in the configuration the project uses is what
takes it off the forms.

## Evidence and current boundary

- `internal/api3/field_schemes_test.go` covers all 17 operations and, more to
  the point, that a scheme governs `createmeta`: a field restricted to one work
  type is on that work type's create form and off the other's. It also covers
  the per-item answer for an unknown field, the administration permission, the
  404 for an unknown scheme and for a field the scheme does not associate, the
  clone that leaves its original alone, and the override that returns to the
  fallback when it is removed.

Jira's `rendererType` parameter, the `matchedFilters` a listing can report,
scheme-level `allowedOperations` beyond read and write, and the association
contexts other than `PROJECT_ID` remain.
