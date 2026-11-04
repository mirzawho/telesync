---
paths:
  - "internal/wordpress/**"
  - "internal/secrets/**"
  - "internal/gateway/wordpress.go"
  - "internal/channelstore/wordpress.go"
---

# WordPress safeguards

Keep outbound requests HTTPS-only, public-host-only, resolve-once, no-redirect
and no-proxy. Keep application passwords AES-256-GCM sealed at rest and only
usable by the backend; never return or log site credentials. Treat Telegram,
WordPress and SQLite as separate systems and report partial failure.
