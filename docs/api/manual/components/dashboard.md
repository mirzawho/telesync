<!-- component: dashboard -->
<!-- manual: dashboard -->
# Dashboard counts

`GET /api/v1/local/stats/dashboard` answers the panel's four count cards —
categories, channels, WordPress sites and administrators — in one request,
instead of one list call per card. It only reads: nothing is written, marked
read or published.

## What each number is

Every number counts exactly the rows the matching list route would paginate
through for the same caller, so a card and a list can never disagree. It is the
ordinary visibility rule — an explicit `view:deny` takes a row out of a global
`<kind>.view`, and without that global right only an explicit `view:allow` puts
one in — applied to `categories`, `channels` and `wordpress_sites`.

`administrators` is the one number that is never narrowed by row: it is every
account, and it is reported only to a caller holding `users.view` or
`users.manage`.

## Null is not zero

A collection the caller may not see at all is `null`, never `0`. The two answers
would otherwise tell a caller apart "there is nothing there" and "you may not
look", which the API does not do. All four keys are always present, so a
missing key is never how "may not look" is expressed.

A field is `null` exactly when the caller holds neither the global `<kind>.view`
permission nor any assignment for that kind. A create right alone changes
nothing: it admits no row, so the field stays `null`. The panel renders a card
only for a field that is not `null`, and sizes the grid from how many cards
there are.

## Statuses

| Status | When |
| --- | --- |
| `200` | the counts, with `null` wherever the caller may not look |
| `401` | no usable credential |
| `405` | any method but GET, or a path below the route; the answer carries `Allow: GET` |
| `503 worker_unavailable` | the account, channel or category store is not configured; or sites are registered but the WordPress side of the configuration is not |
| `500 store_error` | a store did not answer; a broken store is never reported as a count of `0` |

A deployment with no WordPress site store configured answers `200` and
`"wordpress_sites": null`: there are no sites to count, and that is not a
failure. With a site store but no way to read it, the route answers `503` — the
same answer the WordPress routes give — rather than a number over a store it
cannot reach.
