<!-- component: account-login -->
`POST /api/v1/me/telegram-accounts/login/start` requires the signed-in user's
Bearer token and `Content-Type: application/json`. Send exactly one JSON object:
`server_key` names an enabled configured server, `method` is an advertised
login method, and `phone_number` is required for `phone`. The current daemon
advertises phone login only. The caller does not choose the network type: the
gateway derives `telegram` or `custom` from the selected server definition. An
optional legacy `network_type` is accepted only when it matches that definition.

The shared decoder accepts surrounding JSON whitespace, rejects unknown fields
and invalid field types, and refuses empty, malformed, non-object, or
concatenated/trailing JSON. The body is limited to 64 KiB. Invalid bodies return
`400 invalid_input` (oversize bodies return `413`); an absent or disabled server
returns `404 unknown_client`, a missing phone number returns `400 invalid_input`,
and an unsupported method returns `400 unsupported_login_method`. A `201`
response is returned only after the login service accepts the request and
completes its first authentication step. That step can send a real login
challenge; never use a personal number in a test or documentation probe.
