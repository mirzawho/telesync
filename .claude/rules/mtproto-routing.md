---
paths:
  - "internal/profile/**"
  - "internal/routing/**"
  - "internal/keys/**"
  - "internal/sessionstore/**"
  - "internal/gotdclient/**"
  - "internal/worker/**"
---

# MTProto isolation

Official profiles leave gotd DC, resolver, DC-list and public-key options zero.
Custom profiles set every endpoint/DC/key explicitly. Never allow a custom
resolver to consume server-reported addresses or fall back to Telegram's CDN,
bundled keys, or another client. Preserve separate credentials, session files,
peer stores and worker sockets. Load the custom-routing or gotd skill for the
verified dial paths and lifecycle details.
