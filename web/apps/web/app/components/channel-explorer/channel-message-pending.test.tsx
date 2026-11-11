import assert from "node:assert/strict"
import { test } from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { PendingMessage, type PendingSend } from "@/components/channel-explorer/channel-messages-pane"

const pending: PendingSend = {
  localID: "local-send-1",
  operationID: "operation-1",
  identity: "cat\u001f98",
  status: "processing",
  text: "private draft text must not appear",
  file: null,
  mediaKind: null,
  error: null,
}

test("pending send is a compact status bubble and never renders its draft text", () => {
  const markup = renderToStaticMarkup(<PendingMessage pending={pending} onRestore={() => undefined} />)
  assert.match(markup, /در حال ارسال/)
  assert.match(markup, /rounded-2xl/)
  assert.match(markup, /motion-reduce:animate-none/)
  assert.doesNotMatch(markup, /private draft text must not appear/)
  assert.doesNotMatch(markup, /data-message-id/)
})

test("failed send offers draft recovery without an automatic resend action", () => {
  const markup = renderToStaticMarkup(
    <PendingMessage pending={{ ...pending, status: "failed", error: "ارسال انجام نشد" }} onRestore={() => undefined} />
  )
  assert.match(markup, /ارسال انجام نشد/)
  assert.match(markup, /بازیابی پیش‌نویس/)
  assert.doesNotMatch(markup, /ارسال دوباره/)
})

test("sent status remains a synchronization placeholder rather than a success notice", () => {
  const markup = renderToStaticMarkup(<PendingMessage pending={{ ...pending, status: "sent" }} onRestore={() => undefined} />)
  assert.match(markup, /در حال همگام‌سازی پیام/)
  assert.doesNotMatch(markup, /پیام ارسال شد/)
  assert.doesNotMatch(markup, /private draft text must not appear/)
})
