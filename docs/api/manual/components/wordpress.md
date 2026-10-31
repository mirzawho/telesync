<!-- component: wordpress -->
<!-- manual: wordpress -->
# WordPress sites

A WordPress site is a publishing destination the operator registers once. The
four routes below **register, validate, store, update, list and delete that
registration** and nothing else: they do not create posts, categories or media,
and nothing about Telegram is involved. Registration reads the site's title and
icon through its REST API; after that the record is local and the routes answer
from SQLite alone.

The application password is stored encrypted (AES-256-GCM) so a later
publishing feature can present it to the site. It is **never** returned by any
route, and no route returns the ciphertext, its nonce, or an `Authorization`
header.

## Objects

A site is:

```json
{
  "id": 3,
  "category_ids": [1, 4],
  "url": "https://blog.example.com",
  "domain": "blog.example.com",
  "title": "My Blog",
  "site_icon_url": "https://blog.example.com/wp-content/uploads/icon.png",
  "username": "editor",
  "created_at": "2026-10-29T12:00:00Z",
  "updated_at": "2026-10-29T12:00:00Z"
}
```

- `id` is the local row id the other WordPress routes address the site by.
- `url` is the canonical URL: scheme and host lower-cased, no trailing slash.
- `domain` is that URL's host, for display.
- `title` is the site's WordPress title as read at registration or at the last
  update. It is `""` when the site has no title.
- `site_icon_url` is `null` when the site has no icon or its icon could not be
  resolved; it is never the numeric attachment id.
- `username` is the WordPress user the application password belongs to. The
  password itself is not part of the object.
- `category_ids` are the **global categories** (the same ones channels are
  filed under, `GET /api/v1/local/categories`) the site publishes into, by id.
  A site may be in several or none; an id whose category was deleted later
  disappears with it.

The list body is `{"data":{"sites":[…]},"meta":{…}}` with the same site
objects, newest first and not paginated. An empty list is `[]`, never `null`.

A successful `POST` or `PATCH` answers `{"data":{"site":{…},"saved":{"id","refresh":true,"redirect_to"}}}`
and a `DELETE` answers `{"data":{"deleted":true,"id","redirect_to":…}}`. These
three routes answer with a **document, not a redirect** (unlike the categories'
writes): a browser client calling them with `fetch` reads any non-2xx answer,
a 3xx included, as a failure, so the answer itself states that the write
happened and what to reload.

## Rules

### Request bodies

- **Register** (`POST`) takes `{"url", "username", "password"}`; all three are
  required and an empty `username` or `password` is `400 invalid_input`.
- **Update** (`PATCH`) takes any of `{"url", "username", "password"}`. A field
  that is omitted keeps its stored value — including the encrypted password,
  which is preserved untouched when `password` is omitted. A body with none of
  the three is `400`. There is no way to blank a field.
- **Categories** are part of both bodies: `POST` takes `category_ids` (the
  site's categories), and `PATCH` takes exactly one of `category_ids` (replace
  the whole set), `add_category_ids` or `remove_category_ids` (toggle one
  without listing the rest). A category id that does not exist is `400
  invalid_input`, and a create is undone rather than stored without it. A
  `PATCH` that only changes categories does not contact the site.
- Unknown fields are refused (`400`), not ignored; the body must be
  `application/json` (otherwise `415`) and exactly one JSON object.

### The URL

The URL is validated before anything is fetched:

- `https` only. Plain `http` is refused; there is no configuration that
  enables it.
- An absolute URL with a host and an optional path for an installation in a
  subdirectory (`https://example.com/blog`). No port, no username or password
  in the URL, no query, no fragment, no `..` in the path.
- A public domain name: not an IP address, not a single label, and not a name in
  an internal zone (`localhost`, `.local`, `.internal`, `.home.arpa`,
  `metadata.google.internal`, …). An embedded or non-ASCII host is written in
  its `xn--` form.

Refusals are `400 invalid_input`.

### Registration

1. The URL is normalized as above.
2. `GET {url}/wp-json/wp/v2/settings` is requested with HTTP Basic
   authentication (`username:password`), `Accept: application/json`.
3. `title` is read from the answer; `site_icon` is read only as an attachment
   **id** (`0` or `false` means none). That id is resolved with
   `GET {url}/wp-json/wp/v2/media/{id}`, and the media item's `source_url` is
   kept when it is a plain `https` URL on a public host. A failure to resolve the
   icon leaves `site_icon_url` null; it does not fail the registration.
4. Only after the site answers validly are the URL, title, icon, username and
   encrypted password stored. A failure at any step stores nothing, so there is
   no partial record.

The password should be a WordPress **Application Password** (Users → Profile →
Application Passwords), which is revocable on its own and is what the REST API's
Basic authentication accepts. The WordPress user needs the `manage_options`
capability to read `wp/v2/settings`; in practice that means an Administrator (a
custom role can be granted it). Application passwords require HTTPS, which is
the only scheme this API accepts.

### `GET /api/v1/local/wordpress/{id}`

One site, in the same shape as a list row. `404` when no site has that id, or
when the caller may not see it (no global `wordpress.view` and no per-site
`view: allow`).

### Updates

A `PATCH` that supplies `url`, `username` or `password` is **validated against
the remote site before anything is written**: the same `wp/v2/settings` call is
made with the new URL, the new (or current) username and the new (or current)
password. A failed validation answers with the error below and leaves the
stored record exactly as it was — a broken rotation or a wrong domain never
replaces a working one.

`title` and `site_icon_url` are refreshed from the site the validation call
answered, so a renamed site or a changed icon is recorded, and an icon the site
no longer reports is cleared rather than left stale.

### Deletion

`DELETE` removes the local registration and its encrypted credential, and the
per-site permission assignments that reference it. It does **not** contact the
site and makes no change to it; `{"data":{"deleted":true,"id":3}}` is the
answer. A second delete is `404 not_found`.

### Access

- `GET` (the list): requires `wordpress.view`, or at least one per-site `view`
  assignment. Without the global permission only the sites the caller has an
  explicit per-site `view: allow` for are listed.
- `POST`, `PATCH`, `DELETE`: require the global `wordpress.create` permission.
  There is deliberately no per-site write grant: once a record exists its
  credential can be replaced, so writing is one global right. The operator
  token holds it.

Per-site assignments are managed with `PUT /api/v1/local/users/{id}/permissions`
under `"wordpress_permissions": [{"site_id": …, "permissions": {"view": "allow"}}]`
and are reported under `wordpress` in the permissions object.

## Errors

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_input` | A body, field or URL value is not acceptable. |
| 400 | `wordpress_blocked_address` | The URL resolves to a loopback, private, link-local, metadata or otherwise non-public address. |
| 401 | `wordpress_unauthorized` | The site refused the username or application password (HTTP 401). |
| 403 | `wordpress_forbidden` | The account cannot read the site settings (HTTP 403): it lacks `manage_options`. |
| 502 | `wordpress_incompatible` | The site answered, but not as a WordPress REST API (an HTML page, the REST route missing, an unexpected status or shape, a redirect, an oversized answer). |
| 502 | `wordpress_unreachable` | The site could not be connected to or resolved, or its TLS certificate could not be verified. |
| 504 | `wordpress_timeout` | The site did not answer within the request timeout. |
| 404 | `not_found` | No site has that id. |
| 409 | `duplicate_site` | A site with that canonical URL is already registered. |
| 503 | `worker_unavailable` | The key `api.wordpress.encryption_key_env` (or `api.wordpress.encryption_key`) is not configured, so no credential can be stored. |

## What the server will not do

Every request to a site goes through one client with these rules, and no
request is retried, redirected or sent anonymously:

- **No redirects.** A 3xx is reported as `wordpress_incompatible`; the client
  never follows it, so a redirect cannot carry the request (or the credential)
  to another host.
- **No internal destinations.** The host is resolved once, every answer must be
  a public address, and the connection goes to one of exactly those addresses —
  so a DNS answer that changes after validation (DNS rebinding) cannot reach a
  different address. Loopback, private, link-local (including
  `169.254.169.254`), carrier-grade NAT, multicast, documentation, benchmarking
  and IPv6 unique-local ranges are refused at the dial. No proxy is used, so
  `HTTPS_PROXY` in the environment cannot redirect the request either.
- **Bounded.** A 10 second timeout covers the connect, TLS handshake and the
  whole answer; a response body over 1 MiB is refused; only a JSON object is
  accepted.

## Configuration

The key that encrypts application passwords comes from the configuration:

```json
"api": {
  "wordpress": { "encryption_key_path": "/etc/telesync/wordpress.key" }
}
```

The key may come from a file (`api.wordpress.encryption_key_path`, resolved
against the configuration's directory), from an environment variable
(`api.wordpress.encryption_key_env`), or from a literal in the configuration
(`api.wordpress.encryption_key`, which makes the file itself a secret). Set one.
The file and the variable hold 32 bytes of key material, base64-encoded
(`openssl rand -base64 32`) or raw. A missing variable, or a value that is not 32 bytes, fails
`telesync config validate` and is reported by name, never by value. Without a
key, the WordPress routes answer `503` rather than store a credential in the
clear.

The key is used with AES-256-GCM and is **not** generated at startup: keep it.
A different key cannot open credentials sealed with the old one, and the record
must then be registered again. Each stored credential records its format
version (currently `1`), so a future format change can migrate rows instead of
breaking them.
