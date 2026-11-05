# Verifying media publishing live

The fixes for "the dialog says `دریافت عکس ناموفق بود`" and "publish returns
`publish_partial_failure` / *The source attachment could not be read*" are
covered by unit and contract tests. Those tests use scripted workers. This
runbook is the part they cannot cover: a real Telegram account reading a real
source message and a real destination connection accepting the picture.

Run it from an unrestricted shell on a machine that already has the two
accounts and the custom backend's address. Nothing here invents a credential,
an endpoint or a key — every `<angle-bracket>` is a value you already hold.

**What this can prove:** that the source picture is read through the source
connection, transferred to the destination connection, and published with its
caption, and that the two failure stages report differently.

**What this cannot prove for you:** anything you do not run. Every step has an
observable outcome; a step skipped is not a pass.

---

## 0. What you need

| | |
| --- | --- |
| Official Telegram client | `app_id`, `app_hash`, a phone number, and a channel you can read with a photo message in it |
| Custom `cat` backend | `app_id`, `app_hash`, phone, and its `host`, `port`, `dc_id`, `rsa_public_key_path`, `transport`, `routing` |
| Destination | a channel on `cat` this account **may post to** (below it is `کت فیلم`) |
| Node | for the panel (`make build` embeds it) |
| Go | 1.x, to build |

Take the values from the operator or your existing deployment. If you do not
have the `cat` backend's address and RSA key, stop: scenarios 2–8 cannot be run
and nothing below should be reported as verified.

Set these once for the session. Replace every placeholder.

```bash
export TELESYNC_CONFIG="$PWD/telesync.json"
export OFFICIAL_API_HASH='<the official app hash>'
export OFFICIAL_PHONE='<+15551234567>'
export CAT_API_HASH='<the cat app hash>'
export CAT_PHONE='<+15550000000>'
export CAT_HOST='<the cat backend host>'
export CAT_PORT='<its port>'
export CAT_DC_ID='<its datacenter id>'
export CAT_RSA_KEY='<absolute path to its RSA public key PEM>'
```

---

## 1. Build

```bash
make build
```

This builds the panel, regenerates `docs/api`, and produces `bin/telesync`
carrying both. `./bin/telesync` with no arguments writes a starter config to
`~/.telesync/config.json` if there is none.

---

## 2. Configure both clients

Write `telesync.json` from
[`telesync.example.json`](telesync.example.json). Two things matter here more
than anything else in the file:

- **The source connection and the destination connection are two different
  clients.** The source message belongs to the official one. The destination is
  `cat`. They have separate `app_id`/`app_hash`, separate phones, separate
  session files and separate peer stores.
- **A custom client leaves nothing to default.** `server.host`, `server.port`,
  `server.dc_id`, `server.rsa_public_key_path`, `server.transport` and
  `server.routing` are all set explicitly. A custom client never falls back to
  official Telegram's datacenter list, resolver or bundled keys.

```json
{
  "api": {
    "listen_addr": "127.0.0.1:8080",
    "request_timeout": "30s",
    "jwt": { "secret_env": "TELESYNC_JWT_SECRET", "token_ttl": "1h" }
  },
  "runtime": {
    "run_dir": "$PWD/state/run",
    "session_dir": "$PWD/state/sessions",
    "data_dir": "$PWD/state/db",
    "cache_dir": "$PWD/state/cache/avatars",
    "peer_dir": "$PWD/state/peers",
    "reconnect": 10
  },
  "clients": [
    {
      "key": "official",
      "type": "official",
      "enabled": true,
      "app_id": 0,
      "app_hash_env": "OFFICIAL_API_HASH",
      "phone_env": "OFFICIAL_PHONE"
    },
    {
      "key": "cat",
      "type": "custom",
      "enabled": true,
      "app_id": 0,
      "app_hash_env": "CAT_API_HASH",
      "phone_env": "CAT_PHONE",
      "server": {
        "host": "$CAT_HOST",
        "port": 443,
        "dc_id": 2,
        "rsa_public_key_path": "$CAT_RSA_KEY",
        "transport": "intermediate",
        "routing": "single-dc",
        "obfuscated": false
      }
    }
  ]
}
```

Fill in both `app_id` values. The `key` is what the API path and the panel's
server picker use — the reported failure used `cat`; substitute your own key
everywhere below if it differs. The `host`/`port`/`dc_id`/transport values above
are the ones the project has been verified against before; use the backend's
real ones.

**Never put an app hash in the JSON.** It comes from the environment, through
`app_hash_env`.

```bash
export TELESYNC_JWT_SECRET='<a long random string>'
mkdir -p state/run state/sessions state/db state/cache/avatars state/peers
./bin/telesync config validate
```

Expect `valid`. A missing RSA key file or a missing runtime directory fails here
rather than at 3am.

---

## 3. Authenticate both clients, once

```bash
./bin/telesync auth --key official
./bin/telesync auth --key cat
```

Each prompts for the phone, the login code and a two-step password, and reports
success only when the account really is signed in. Auth-key generation against a
slow endpoint takes 10–15 s, so keep the default `--timeout 5m`.

**Check:** `auth` prints success for *both*. An account left in `needs_auth` is
not restarted in a loop — it waits for this command.

---

## 4. Start and create an administrator

```bash
./bin/telesync daemon
```

In a second terminal:

```bash
make admin
```

Then log in and keep the token:

```bash
export API=http://127.0.0.1:8080/admin/api/v1
TOKEN=$(curl -s -X POST "$API/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"<admin username>","password":"<admin password>"}' | jq -r .access_token)
curl -s -H "Authorization: Bearer $TOKEN" "$API/auth/me" | jq
```

The account needs the global **`content.publish`** permission — the permission
the administrators dialog keeps in its draft and reports read-only in its
`گزارش دسترسی‌ها` tab.

**Check:** both workers are ready and both connections are authorized.

```bash
curl -s -H "Authorization: Bearer $TOKEN" "$API/telegram/official/health" | jq
curl -s -H "Authorization: Bearer $TOKEN" "$API/telegram/cat/health"     | jq
```

Both must report `"state": "connected"` and `"authorized": true`. If `cat`
reports `needs_auth`, go back to step 3 for that key; if it reports
`disconnected`, the backend's host/port/DC/routing is wrong — the custom client
never falls back to official Telegram, so a wrong endpoint stays wrong.

---

## 5. Find the source message and its attachment

```bash
export SRC_LOCAL_ID=1          # the row in /admin/channels/1/messages
export SRC_TG_ID=<source channel's Telegram id>
export SRC_MSG_ID=<message id of the post with the picture>
export DEST_TG_ID=<the id of کت فیلم on the cat connection>
```

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$API/telegram/official/channels/$SRC_TG_ID/messages?page=1&per_page=20" | jq
```

From the message with the picture, record its `media` descriptor:

```bash
export MEDIA_ID='<media.id exactly as reported>'
export MEDIA_URL='<media.url exactly as reported>'
```

### Check 5a — the media URL names the SOURCE connection

```bash
echo "$MEDIA_URL"
```

It must contain `/telegram/official/`, **not** `/telegram/cat/`. This is the
direct probe for the root cause of the publishing failure: the URL is built from
the connection that recorded the message, and nothing may re-point it at the
destination.

If it says `cat`, stop and report it — the fix is not doing its job.

### Check 5b — the bytes fetch with the bearer credential

```bash
curl -s -o /tmp/source.jpg -D - \
  -H "Authorization: Bearer $TOKEN" "$API$MEDIA_URL" | head -20
file /tmp/source.jpg
```

Expect `200`, `Content-Type: image/jpeg`, `Content-Disposition: attachment` with
`filename*=UTF-8''…` when the sender named the file, `Accept-Ranges: bytes`, and
a JPEG. A photo carries **no** `filename` of its own — Telegram only names
documents — so its absence here is correct and is not a defect.

### Check 5c — the same URL fails without a credential

```bash
curl -s -o /dev/null -w '%{http_code}\n' "$API$MEDIA_URL"
```

Expect `401`. Private media must never be reachable without the token, and the
token must never appear in a URL.

---

## 6. Scenario 1 — the preview shows the picture

In the panel:

1. Open `/admin/channels/1/messages`.
2. Open the message with the picture. **It must render in the timeline.**
3. Click `ارسال این پیام`. Choose `کانال`.
4. **The picture must appear in the centre column under `رسانه‌های پیوست‌شده`,
   and again in the left column's `پیش‌نمایش`.**

**Pass:** the image is drawn, with a loading state before it and the sender's
filename beneath it.

**Fail — the reported symptom:** `دریافت عکس ناموفق بود` in either column. If it
appears *instantly* on open with no loading state, the dialog is again drawing a
fetch that was never attempted.

**Fail — a real fetch failure** shows the same words *after* a loading state,
together with `تلاش مجدد` and `حذف پیوست`. Press `تلاش مجدد`:

- **Pass:** the picture appears.
- **Fail:** it stays failed. Take the request id from the browser's network tab
  (step 11) and report it.

---

## 7. Scenario 2 — publish to `cat` with the picture attached

Back in the dialog:

1. Server: `cat`.
2. Destination channel: `کت فیلم`.
3. Leave the picture **attached** (`پیوست‌شده`).
4. Edit the caption to something unmistakable, e.g. `تست انتشار رسانه ۱۴۰۴`.
5. Publish.

**Pass:**

- the dialog reports success;
- `کت فیلم` has **one** new post carrying the picture **and** the caption;
- the picture is a picture, not a generic document card.

Check the operation record and the response:

```bash
curl -s -X POST "$API/telegram/cat/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: verify-media-1" \
  -d "{
    \"source\":      {\"local_channel_id\": $SRC_LOCAL_ID, \"message_id\": $SRC_MSG_ID},
    \"destination\": {\"channel_id\": \"$DEST_TG_ID\"},
    \"content\":     {\"text\": \"تست انتشار رسانه ۱۴۰۴\", \"entities\": []},
    \"media\":       {\"mode\": \"preserve_source\", \"attachment_id\": \"$MEDIA_ID\"},
    \"options\":     {\"silent\": false, \"disable_web_page_preview\": false}
  }" | jq
```

**Pass:** `200` with `data.message` and `data.destination.client_key` = `cat`.

**Fail — the reported symptom:** `500` with `publish_partial_failure` and
*The source attachment could not be read*. That is the bug: the source picture
was fetched through `cat`, which cannot see the source channel. Report it with
the request id.

**Fail — the corrected behaviour on a genuine source problem:** `502` with
`publish_source_media_unavailable`, and **nothing at all posted**. That is the
fix working as designed on a real source failure; go to step 10 to see which it
was.

### Check 7a — the source is untouched

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$API/telegram/official/channels/$SRC_TG_ID/messages/$SRC_MSG_ID" | jq
```

**Pass:** identical to step 5 — same text, same `media.id`, no new views, not
marked read. Publishing writes to the destination and never to the source.

---

## 8. Scenario 3 — changing the destination does not change the source media

Repeat step 7's API call with `"Idempotency-Key": "verify-media-2"` and
`"destination": {"channel_id": "$OTHER_DEST"}`, where `$OTHER_DEST` is a
*different* channel, and once more with `"Idempotency-Key": "verify-media-3"`
back on `$DEST_TG_ID`.

**Pass:** every call reads the source the same way and succeeds. The source
attachment's identity and its `media.url` are unchanged by which destination you
pick.

Watch the daemon's stderr while doing this:

```
gateway: source media download failed: client=... channel=... message=...
```

**Pass:** no such line. If one appears, it must name `client=official` and the
source message — never `client=cat`.

---

## 9. Scenario 4 — explicit detach publishes text only

1. Open the dialog again on the same source message.
2. Press `حذف پیوست` next to the picture. The badge must read `جدا شده`, and the
   picture must stay in the source message — only the outgoing draft changed.
3. Publish to `کت فیلم` with a different caption.

```bash
curl -s -X POST "$API/telegram/cat/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: verify-media-omit" \
  -d "{
    \"source\":      {\"local_channel_id\": $SRC_LOCAL_ID, \"message_id\": $SRC_MSG_ID},
    \"destination\": {\"channel_id\": \"$DEST_TG_ID\"},
    \"content\":     {\"text\": \"تست بدون پیوست ۱۴۰۴\", \"entities\": []},
    \"media\":       {\"mode\": \"omit\"},
    \"options\":     {\"silent\": false, \"disable_web_page_preview\": false}
  }" | jq
```

**Pass:** `200`; the destination gets the text and **no** picture; the source
message still has its picture.

This is the only way a post leaves without its attachment. A draft that says
`preserve_source` is never quietly downgraded.

---

## 10. Scenario 5 — an unreadable source is blocked, never dropped

This is the safety property that matters most: **a draft that says
`preserve_source` is never published as text alone.**

Make the source unavailable without touching the destination. The reliable way
is to delete the source message in Telegram after the dialog is opened:

1. Open the dialog on a message with a picture. Leave it attached.
2. Delete that message in Telegram, from the source account.
3. Publish.

```bash
curl -s -o /tmp/err.json -w '%{http_code}\n' -X POST "$API/telegram/cat/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: verify-media-unreadable" \
  -d "{
    \"source\":      {\"local_channel_id\": $SRC_LOCAL_ID, \"message_id\": $SRC_MSG_ID},
    \"destination\": {\"channel_id\": \"$DEST_TG_ID\"},
    \"content\":     {\"text\": \"این نباید ارسال شود\", \"entities\": []},
    \"media\":       {\"mode\": \"preserve_source\", \"attachment_id\": \"$MEDIA_ID\"},
    \"options\":     {\"silent\": false, \"disable_web_page_preview\": false}
  }"
jq . /tmp/err.json
```

**Pass:**

- `404` and `"code": "publish_source_unavailable"` — the whole message is gone;
- `کت فیلم` has **no** post carrying `این نباید ارسال شود`, and in particular no
  text-only post;
- the dialog keeps the draft and offers `تلاش مجدد` / `حذف پیوست`.

**Fail:** anything reaching `cat`, or a post there with the text and no picture.

If instead the message is still readable and only its **attachment** cannot be
fetched, the answer is `502` with `"code": "publish_source_media_unavailable"`
and one line on the daemon's stderr:

```
gateway: source media download failed: client=official channel=… message=…
```

That specific code needs a readable message with an unreadable attachment, which
is awkward to provoke deliberately. If you cannot, record it as **not
exercised** here — it is covered by
`TestPublishRefusesToSendTextAloneWhenTheSourceAttachmentCannotBeRead`, and the
property this scenario exists for (nothing is sent) is checked either way.

Then press `حذف پیوست` and publish again with `{"mode": "omit"}` — that must
succeed as text only, which is scenario 4.

### Distinguishing the two stages

| The response says | It means | Nothing was |
| --- | --- | --- |
| `publish_source_unavailable` | the source message is gone | sent anywhere |
| `publish_source_media_unavailable` | the **source** connection could not read the attachment | sent anywhere; retry, or `omit` |
| `publish_media_upload_failed` | the attachment was read and the **destination** would not take it | published |
| `publish_unsupported` | `cat` cannot take this kind of content at all | published |
| `publish_media_too_large` | over the 32 MiB send limit | published |
| `publish_outcome_unknown` | no answer arrived; **not** retried automatically | confirmed either way |

A source failure must never read as a destination failure or the reverse. The
old behaviour collapsed both halves into `publish_partial_failure` — if you see
that code on a source read, the fix is not in place.

---

## 11. Scenario 6 — a real destination failure is named as one

Make `cat` refuse without touching the source. The panel's picker only offers
channels this account may post in, so this one is an API call: use a `cat`
channel the account can read but cannot post in.

```bash
curl -s -X POST "$API/telegram/cat/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: verify-media-denied" \
  -d "{
    \"source\":      {\"local_channel_id\": $SRC_LOCAL_ID, \"message_id\": $SRC_MSG_ID},
    \"destination\": {\"channel_id\": \"<a channel this account may not post in>\"},
    \"content\":     {\"text\": \"تست\", \"entities\": []},
    \"media\":       {\"mode\": \"preserve_source\", \"attachment_id\": \"$MEDIA_ID\"},
    \"options\":     {\"silent\": false, \"disable_web_page_preview\": false}
  }" | jq
```

**Pass:** `403` with `publish_permission_denied` (or `404` with
`publish_destination_unavailable` if `cat` cannot see it at all). The error names
the destination and **never** the source attachment — and the daemon logs no
`source media download failed` line, because the destination is refused before
any attachment is fetched. The source was fine; the destination was not.

**Fail:** `publish_source_media_unavailable` or `publish_partial_failure` here.
That would mean a destination refusal is still being reported as an unreadable
source.

---

## 12. Scenario 7 — partial success across destinations, and no duplicate on retry

Publish to two `cat` channels at once from the panel's dialog, where one is
postable and one is not.

**Pass:**

- the report names each destination separately, one ✓ and one ✕;
- the failure text is Persian and names the stage;
- the line beneath says the successful ones are not resent.

Then press publish again **without changing anything**, with the same
`Idempotency-Key`:

```bash
curl -s -D - -X POST "$API/telegram/cat/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: verify-media-1" \
  -d "<the exact body you sent in step 7>" | head -20
```

**Pass:**

- `Idempotent-Replay: true`, and the **same** message id comes back;
- `کت فیلم` still has exactly **one** post from step 7 — count them.

With a *different* body under the same key: `409` `publish_idempotency_conflict`.
After an ambiguous outcome (a timeout, a dropped connection): `409`
`publish_outcome_unknown`, and the app does **not** resend. Count the posts on
the destination before assuming anything failed.

---

## 13. Scenario 8 — a stale file reference recovers

Telegram expires file references on its own schedule. Force it by leaving the
dialog open and waiting, or by reading the same message repeatedly over a long
idle period, then publish.

**Pass:** the publish succeeds. A stale reference is recovered inside the source
read by re-reading the source message once and taking its fresh reference; it is
bounded and it happens **before** anything is sent.

**Fail:** a publish that dies with a file-reference error after the message was
already read. Report it — that means the refresh is not being applied where it
should be.

If you cannot provoke it, record this scenario as **not exercised**. Do not
record it as passed.

---

## 14. Other media types

Repeat step 7 for a **video**, an **audio**, a **voice note** and a **document**
(a named file, so `filename` is populated).

| Type | Pass |
| --- | --- |
| photo | arrives as a photo with its caption |
| video | arrives as a video |
| audio / voice | arrives playable |
| document | arrives as a file under the **sender's** filename, not `فایل` |

A poll, a location or a link preview has no bytes to send: publishing one is
text only, by design. A type `cat` cannot take answers `publish_unsupported` and
is never reported as a successful publish.

---

## 15. Correlating a failure by request id

Every error body carries `request_id`, and the panel shows it as `کد پیگیری`.

```bash
export RID='<the request_id from the error body>'
grep "$RID" <(./bin/telesync daemon 2>&1) 
```

Or keep the daemon's stderr in a file from the start:

```bash
./bin/telesync daemon 2> daemon.log &
...
grep "$RID" daemon.log
```

**Pass:** a line naming the stage and the source:

```
gateway: source media download failed: client=official channel=… message=…
```

The log names the source connection and message. It never carries a session, a
phone number, an auth key, a file reference or the media bytes.

---

## 16. Recording the result

Write down, per scenario, **pass / fail / not exercised**, with the request id
for anything that failed. In particular state explicitly:

- whether the preview showed the picture **before** any destination was chosen;
- whether switching the destination to `cat` left the preview working;
- whether `cat` received the picture **with** the caption;
- whether any text-only post appeared that was not explicitly asked for;
- whether a retry duplicated a confirmed send.

Do not report a scenario as verified because the tests pass. The tests use
scripted workers; only this run exercises a real `cat` backend.

## 17. Cleanup

```bash
kill -INT <daemon pid>      # not kill -9: that orphans the workers
```

Delete the test posts from `کت فیلم` by hand. Session files under
`state/sessions/` grant account access — keep them out of any web-served path
and treat a backup of them as a credential.
