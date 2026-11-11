import assert from "node:assert/strict"
import { test } from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { ChannelMessagesPane } from "@/components/channel-explorer/channel-messages-pane"
import type { ChannelDetail } from "@/lib/types"
import { clientLoader } from "./channel-messages"

test("local detail maps local channel 2 to cat/remote 98 and enables granted message controls", async () => {
  const calls: string[] = []
  const storage = new Map([["telesync.panel.token", "test-token"]])
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
    sessionStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => void storage.set(key, value),
      removeItem: (key: string) => void storage.delete(key),
    },
  }
  ;(globalThis as Record<string, unknown>).fetch = async (input: unknown) => {
    const path = new URL(String(input), "http://panel.test").pathname
    calls.push(path)
    if (path === "/api/v1/auth/me") {
      return new Response(
        JSON.stringify({
          data: {
            id: 1,
            fullname: "admin",
            username: "admin",
            is_active: true,
          },
        }),
        { status: 200 }
      )
    }
    if (path === "/api/v1/local/channels/2") {
      return new Response(
        JSON.stringify({
          data: {
            client_key: "cat",
            channel: {
              id: "98",
              type: "channel",
              title: "لینکدونی",
              avatar_available: false,
              is_member: true,
              is_managed: true,
              membership_status: "member",
              permissions: { new: true, edit: true, delete: true, read: true },
              verified: false,
              scam: false,
              fake: false,
              participants_count: 2,
            },
          },
        }),
        { status: 200 }
      )
    }
    throw new Error(`unexpected API request ${path}`)
  }

  const loaded = await clientLoader({
    request: new Request("http://panel.test/admin/channels/2/messages"),
    params: { id: "2" },
  } as never)
  const lookup = (
    loaded as {
      lookup: { state: string; channel: ChannelDetail; clientKey: string }
    }
  ).lookup
  assert.equal(lookup.state, "ok")
  assert.equal(lookup.clientKey, "cat")
  assert.equal(lookup.channel.id, "98")
  assert.deepEqual(lookup.channel.permissions, {
    new: true,
    edit: true,
    delete: true,
    read: true,
  })
  assert.deepEqual(calls, ["/api/v1/auth/me", "/api/v1/local/channels/2"])

  const markup = renderToStaticMarkup(
    <ChannelMessagesPane
      channel={lookup.channel as ChannelDetail}
      channelId={2}
      client={lookup.clientKey}
    />
  )
  assert.match(
    markup,
    /پیام خود را بنویسید/,
    "Send composer should be available"
  )
  assert.doesNotMatch(markup, /مقصد یا مجوز کانال در دسترس نیست/)
  assert.doesNotMatch(markup, /امکان ارسال پیام برای این کانال فعال نیست/)
})
