<!-- title: Examples -->
<!-- manual: examples index -->
# Examples

Every request the documentation relies on lives in `api.http` in the format the
tests execute; the index below names each request and the id it captures for the
requests after it.

| Method | Route | Captured id | Example |
| --- | --- | --- | --- |
| GET | `/healthz` | — | below |
| GET | `/docs` | — | below |
| POST | `/api/v1/auth/login` | — | below |
| GET | `/api/v1/auth/me` | — | below |
| GET | `/api/v1/telegram` | — | below |
| GET | `/api/v1/telegram/{key}/health` | — | below |
| GET | `/api/v1/telegram/{key}/profile` | — | below |
| GET | `/api/v1/telegram/{key}/channels` | — | below |
| GET | `/api/v1/telegram/{key}/channels/find` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}` | — | below |
| POST | `/api/v1/telegram/{key}/channels/{channel_id}/join` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/avatar` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media` | — | below |
| POST | `/api/v1/telegram/{key}/messages` | — | below |
| GET | `/api/v1/telegram/{key}/custom_emoji/{document_id}` | — | below |
| GET | `/api/v1/local/channels` | — | below |
| GET | `/api/v1/local/channels/{id}` | — | below |
| GET | `/api/v1/local/channels/{id}/messages` | — | below |
| GET | `/api/v1/local/channels/{id}/avatar` | — | below |
| GET | `/api/v1/local/channels/{id}/categories` | — | below |
| PUT | `/api/v1/local/channels/{id}/categories` | — | below |
| POST | `/api/v1/local/channels/{id}/leave` | — | below |
| GET | `/api/v1/local/categories` | — | below |
| POST | `/api/v1/local/categories` | `category` | below |
| GET | `/api/v1/local/categories/{category_id}` | — | below |
| GET | `/api/v1/local/categories/{category_id}/channels` | — | below |
| PUT | `/api/v1/local/categories/{category_id}` | — | below |
| DELETE | `/api/v1/local/categories/{category_id}` | — | below |
| GET | `/api/v1/local/wordpress` | — | below |
| POST | `/api/v1/local/wordpress` | `site` | below |
| GET | `/api/v1/local/wordpress/{site_id}` | — | below |
| PATCH | `/api/v1/local/wordpress/{site_id}` | — | below |
| POST | `/api/v1/local/wordpress/{site_id}/posts` | — | below |
| DELETE | `/api/v1/local/wordpress/{site_id}` | — | below |
| GET | `/api/v1/local/users` | — | below |
| POST | `/api/v1/local/users` | `user` | below |
| GET | `/api/v1/local/users/{user_id}` | — | below |
| PUT | `/api/v1/local/users/{user_id}` | — | below |
| DELETE | `/api/v1/local/users/{user_id}` | — | below |
| GET | `/api/v1/local/users/{user_id}/permissions` | — | below |
| PUT | `/api/v1/local/users/{user_id}/permissions` | — | below |

`category`, `site` and `user` are ids the API assigns: `api.http` creates them,
captures them and substitutes them into the later requests, and the test runs the file
in order. `{key}`, `{channel_id}` and `{message_id}` are the table's variable
names; the file uses `official`, `1671633563` and `1233` as example values.

The curl form of every request is in
[the legacy examples page](../manual/legacy/examples/README.md), which predates
the generated pages and is kept for its worked commands.
