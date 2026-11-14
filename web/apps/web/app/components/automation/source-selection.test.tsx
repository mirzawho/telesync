import assert from "node:assert/strict"
import { test } from "node:test"

import { listOwnedSourceChannels } from "@/components/automation/source-selection"
import type { TelegramAccount } from "@/lib/types"

const OWNED: TelegramAccount = {
  id: "acc_owned",
  server_key: "official",
  network_type: "telegram",
  status: "connected",
}

test("automation source inventory follows pagination before ownership filtering", async () => {
  const requests: string[] = []
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
    sessionStorage: {
      getItem: () => "tok",
      setItem: () => undefined,
      removeItem: () => undefined,
    },
  }
  ;(globalThis as Record<string, unknown>).fetch = async (input: unknown) => {
    const url = new URL(String(input), "http://panel.test")
    requests.push(url.search)
    const page = Number(url.searchParams.get("page"))
    const rows =
      page === 1
        ? Array.from({ length: 100 }, (_, index) => ({
            id: index + 1,
            title: `foreign-${index + 1}`,
            client_key: "official",
            account_id: "acc_other",
          }))
        : [
            {
              id: 101,
              title: "owned source",
              client_key: "official",
              account_id: OWNED.id,
            },
          ]
    return new Response(
      JSON.stringify({
        data: { channels: rows },
        meta: { page, per_page: 100, has_more: page === 1 },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    )
  }

  const rows = await listOwnedSourceChannels("tok", [OWNED])
  assert.deepEqual(rows.map((row) => row.id), [101])
  assert.equal(requests.length, 2)
  assert.ok(requests[0].includes("page=1"))
  assert.ok(requests[1].includes("page=2"))
})
