// The workflow create/edit page against a stand-in of the real API.
//
// What is proved here is the page's contract: the four sections it paints and
// their headings, what the source picker and the destination blocks offer, what
// the AI stage holds and what it hides, what a save posts and what comes back on
// a reload. The API is a stub, so the assertions are about the conversation —
// the paths and bodies that actually go out — and about the markup the page
// paints. Nothing here drives a live daemon.

import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  WorkflowForm,
  WORKFLOW_FORM_ID,
  addDestination,
  destinationBlocks,
  isCompleteDestination,
  removeDestination,
  trimChannels,
  updateBlock,
} from "@/components/automation/workflow-form"
import type {
  ChannelOptions,
  ConflictWorkflow,
  NamedOption,
  TakenWorkflow,
} from "@/components/automation/workflow-form"
import type { SourceChannel } from "@/components/automation/source-selection"
import {
  DEFAULT_POST_STATUS,
  DEFAULT_THUMBNAIL,
} from "@/components/automation/workflow-wordpress-options"
import type { SiteTerms } from "@/components/automation/workflow-wordpress-options"
import {
  clientAction as workflowAction,
  clientLoader as workflowFormLoader,
  eligibleChannels,
  workflowFormReturnTo,
} from "../../routes/_panel/automation-workflow-form"
import {
  emptyWorkflowDraft,
  readWorkflow,
  wordpressDestination,
  workflowBody,
  workflowDraftOf,
  type DestinationDraft,
  type WorkflowDraft,
} from "@/lib/automation"
import type { TelegramAccount } from "@/lib/types"

// --- harness ----------------------------------------------------------------

const html = (node: React.ReactNode) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={["/automation/workflows/new"]}>
      {node}
    </MemoryRouter>
  )

const sourceChannels: SourceChannel[] = [
  { id: 5, label: "کانال خبری", clientKey: "official", detail: "official" },
  { id: 8, label: "کانال ورزشی", clientKey: "official", detail: "official" },
]
const sites: NamedOption[] = [
  { id: 4, label: "خبرگزاری", detail: "news.example.com" },
  { id: 6, label: "وبلاگ", detail: "blog.example.com" },
]
const servers = [
  {
    key: "official",
    type: "official",
    enabled: true,
    health_checkable: true,
    state: "ready",
    authorized: true,
    transport: "mtproto",
    last_success_at: "",
    last_error: "",
  },
  {
    key: "custom",
    type: "custom",
    enabled: true,
    health_checkable: true,
    state: "ready",
    authorized: true,
    transport: "mtproto",
    last_success_at: "",
    last_error: "",
  },
] as never as Parameters<typeof WorkflowForm>[0]["servers"]

const channelOptions = (
  ...entries: [string, Partial<ChannelOptions>][]
): Map<string, ChannelOptions> =>
  new Map<string, ChannelOptions>(
    entries.map(([accountID, over]) => [
      accountID,
      {
        items: [
          { id: 77, label: "کانال مقصد", detail: "@dest" },
          { id: 78, label: "کانال دوم" },
        ],
        loading: false,
        error: null,
        ...over,
      },
    ])
  )

const emptyTerms = (): SiteTerms => ({
  categories: [],
  tags: [],
  loading: false,
  error: null,
})

const render = (
  draft: WorkflowDraft,
  over: Partial<React.ComponentProps<typeof WorkflowForm>> = {}
) =>
  html(
    <WorkflowForm
      title="افزودن ورک‌فلو"
      submitLabel="افزودن"
      draft={draft}
      onDraftChange={() => {}}
      sourceChannels={sourceChannels}
      taken={new Map<number, TakenWorkflow>()}
      servers={servers}
      sites={sites}
      accounts={[WORKFLOW_ACCOUNT, DESTINATION_ACCOUNT, OTHER_DESTINATION_ACCOUNT]}
      channelsByAccount={channelOptions(
        [DESTINATION_ACCOUNT.id, {}],
        [OTHER_DESTINATION_ACCOUNT.id, {}]
      )}
      siteTerms={new Map<number, SiteTerms>([[4, emptyTerms()]])}
      pending={false}
      onSubmit={() => {}}
      onCancel={() => {}}
      {...over}
    />
  )

const telegramRow = (
  channelID: number,
  clientKey = "official"
): DestinationDraft => ({
  type: "telegram",
  accountID:
    clientKey === "custom"
      ? OTHER_DESTINATION_ACCOUNT.id
      : DESTINATION_ACCOUNT.id,
  clientKey,
  channelID,
  siteID: null,
  options: {},
  enabled: true,
})

const withDestinations = (...rows: DestinationDraft[]): WorkflowDraft => ({
  ...emptyWorkflowDraft(),
  managedChannelID: 5,
  destinations: rows,
})

// How many times a whole phrase appears in the markup.
const count = (out: string, needle: string) => out.split(needle).length - 1

// A switch's state is the one word it paints; the row's own label sits beside
// it, so the word is read from the span SwitchState writes rather than counted
// anywhere in the page.
const on = (out: string) => count(out, '<span class="text-sm">روشن</span>')
const off = (out: string) => count(out, '<span class="text-sm">خاموش</span>')

// The page's buttons, as their opening tags. `disabled` is read as an
// attribute, never as the `disabled:` utility that every button carries.
const buttons = (out: string) => out.match(/<button[^>]*>/g) ?? []
const submitButtons = (out: string) =>
  buttons(out).filter((tag) => tag.includes('type="submit"'))
const isDisabled = (tag: string) => /\sdisabled(?=[\s=>/])/.test(tag)

// --- the page ---------------------------------------------------------------

test("create renders a full page: one form, four sections, one Save", () => {
  const out = render(emptyWorkflowDraft(), {
    title: "افزودن ورک‌فلو",
    submitLabel: "افزودن",
  })
  assert.ok(out.includes("افزودن ورک‌فلو"), "the create title")
  assert.ok(out.includes("افزودن"), "the create submit label")
  // The four sections, under their exact headings.
  assert.ok(out.includes("کانال مبدأ"))
  assert.ok(out.includes("مقصدهای انتشار"))
  assert.ok(out.includes("هوش مصنوعی"))
  assert.ok(out.includes("وضعیت ورک‌فلو"))
  // One form on the page, and one Save and one Cancel — never a second copy
  // down in the scroll area.
  assert.equal(count(out, "<form"), 1)
  assert.equal(count(out, "انصراف"), 1)
  assert.equal(submitButtons(out).length, 1, "exactly one Save")
  assert.ok(
    submitButtons(out)[0].includes(`form="${WORKFLOW_FORM_ID}"`),
    "the header's Save submits the one form"
  )
  assert.ok(out.includes(`id="${WORKFLOW_FORM_ID}"`), "the form carries the id")
})

test("edit renders the same page with its own title, label and locked source", () => {
  const out = render(
    workflowDraftOf(
      readWorkflow(viewOf(workflowBody(withDestinations(telegramRow(77)))))
    ),
    {
      title: "ویرایش ورک‌فلو",
      submitLabel: "ذخیره تغییرات",
      sourceLocked: true,
    }
  )
  assert.ok(out.includes("ویرایش ورک‌فلو"))
  assert.ok(out.includes("ذخیره تغییرات"))
  assert.match(out, /کانال مبدأ پس از ایجاد ورک‌فلو قابل تغییر نیست/)
  assert.equal(count(out, "انصراف"), 1)
  assert.equal(submitButtons(out).length, 1)
})

test("the destinations section says what it is choosing", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(
    out.includes("مشخص کنید پیام‌های این کانال در کدام مقصدها منتشر شوند.")
  )
  assert.ok(out.includes("هنوز مقصدی انتخاب نشده است"))
})

// --- source -----------------------------------------------------------------

test("the source is one managed channel, named and required", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("کانال مبدأ"))
  assert.ok(out.includes("فقط یک ورک‌فلو"))
  // One field, not a list: the picker is a single-select over the managed rows.
  assert.equal(count(out, 'for="workflow-channel"'), 1)
  assert.equal(count(out, 'aria-label="کانال مبدأ"'), 1)
})

test("the source picker offers the managed channels the loader read", async () => {
  fakeGateway()
  const data = (await workflowFormLoader({
    params: {},
    request: request("/automation/workflows/new"),
  } as never)) as {
    status: string
    creating: boolean
    sourceChannels?: SourceChannel[]
  }
  assert.equal(data.status, "ok")
  assert.equal(data.creating, true)
  assert.deepEqual(data.sourceChannels, [
    {
      id: 5,
      label: "کانال خبری",
      clientKey: "official",
      accountId: WORKFLOW_ACCOUNT.id,
      detail: "official",
    },
  ])
})

test("a duplicate source is refused and links to the workflow that holds it", () => {
  const draft = { ...emptyWorkflowDraft(), managedChannelID: 5 }
  const out = render(draft, {
    taken: new Map<number, TakenWorkflow>([
      [5, { id: 12, channelLabel: "کانال خبری" }],
    ]),
  })
  assert.match(out, /برای «کانال خبری» ورک‌فلوی دیگری ثبت شده است/)
  assert.match(out, /href="\/automation\/workflows\/12\/edit"/)
  // And the save that would create a second one is not offered at all.
  assert.equal(submitButtons(out).length, 1)
  assert.ok(
    isDisabled(submitButtons(out)[0]),
    "the save that would duplicate is not offered"
  )
})

test("a channel with no workflow is offered for a new one", () => {
  const out = render({ ...emptyWorkflowDraft(), managedChannelID: 8 })
  assert.ok(!out.includes("ورک‌فلوی دیگری ثبت شده است"))
  assert.ok(!isDisabled(submitButtons(out)[0]))
})

// --- destinations -----------------------------------------------------------

test("a destination block names its platform and can be removed", () => {
  const out = render(withDestinations(telegramRow(77)))
  assert.ok(out.includes("تلگرام"))
  assert.ok(out.includes("اتصال تلگرام"))
  assert.ok(out.includes("کانال‌های مقصد"))
  assert.equal(count(out, "حذف این مقصد"), 1, "one remove control per block")
  assert.ok(out.includes("افزودن مقصد"), "the platform picker to add one")
})

test("one account and its channels are one block, with a row each", () => {
  const draft = withDestinations(telegramRow(77), telegramRow(78))
  assert.equal(destinationBlocks(draft.destinations).length, 1)
  assert.deepEqual(
    destinationBlocks(draft.destinations)[0].channelIDs,
    [77, 78]
  )
  assert.equal(count(render(draft), "حذف این مقصد"), 1)
  assert.equal(
    (workflowBody(draft) as { destinations: unknown[] }).destinations.length,
    2,
    "but the schema still gets one row per published channel"
  )
})

test("several destinations are several blocks, each with its own address", () => {
  const out = render(
    withDestinations(
      telegramRow(77, "official"),
      telegramRow(78, "custom"),
      wordpressDestination(4)
    )
  )
  assert.equal(count(out, "حذف این مقصد"), 3, "one block per address")
  assert.ok(out.includes("سایت وردپرسی"))
  assert.ok(out.includes("کانال‌های مقصد"))
})

test("a Telegram block names its account and shows only its channels", () => {
  const out = render(withDestinations(telegramRow(77)))
  // The picker's own input holds a search, so what is actually a destination is
  // read from the chip it draws for the chosen channel.
  assert.ok(out.includes("کانال مقصد"))
  assert.ok(out.includes("کانال‌های مقصد"))
  assert.ok(out.includes(DESTINATION_ACCOUNT.id), "the selected account is named")
  assert.ok(!out.includes("برای افزودن مقصد تلگرامی"))
})

test("a block with no account yet says so instead of offering channels", () => {
  const out = render(
    withDestinations({
      ...telegramRow(0),
      accountID: "",
      channelID: null,
      clientKey: "",
    })
  )
  assert.match(out, /برای افزودن مقصد تلگرامی، اول حساب مقصد را انتخاب کنید/)
})

test("an unchosen destination is named as incomplete and is never saved", () => {
  const draft = withDestinations({
    ...telegramRow(0),
    accountID: "",
    channelID: null,
    clientKey: "",
  })
  const out = render(draft)
  assert.match(out, /این مقصد هنوز کامل نیست/)
  assert.equal(isCompleteDestination(draft.destinations[0]), false)
  assert.equal(isCompleteDestination(telegramRow(77)), true)
  assert.equal(isCompleteDestination(wordpressDestination(4)), true)
  assert.equal(
    isCompleteDestination({ ...wordpressDestination(4), siteID: null }),
    false
  )
  // A Telegram destination is a peer id beside its connection: the schema
  // refuses zero and nothing else, so any other integer is an address.
  assert.equal(isCompleteDestination(telegramRow(-100123)), true)
})

test("a WordPress block carries the site's own publication options", () => {
  const out = render(
    withDestinations({
      ...wordpressDestination(4),
      options: { category_ids: [7], tag_ids: [9] },
    }),
    {
      siteTerms: new Map<number, SiteTerms>([
        [
          4,
          {
            categories: [{ id: 7, label: "سیاسی" }],
            tags: [{ id: 9, label: "خبر" }],
            loading: false,
            error: null,
          },
        ],
      ]),
    }
  )
  assert.ok(out.includes("سایت وردپرسی"))
  assert.ok(out.includes("وضعیت انتشار"))
  assert.ok(out.includes("دسته‌بندی‌های وردپرس"))
  assert.ok(out.includes("برچسب‌های وردپرس"))
  assert.ok(out.includes("تصویر شاخص نوشته"))
  // The terms already chosen are drawn by their own names.
  assert.ok(out.includes("سیاسی"))
  assert.ok(out.includes("خبر"))
})

test("a WordPress destination writes the publisher's own vocabulary", () => {
  const draft = withDestinations({
    ...wordpressDestination(4),
    options: {
      category_ids: [7],
      tag_ids: [9],
      thumbnail: "omit",
      post_status: "draft",
    },
  })
  const body = workflowBody(draft) as {
    destinations: Record<string, unknown>[]
  }
  assert.deepEqual(body.destinations[0].options, {
    category_ids: [7],
    tag_ids: [9],
    thumbnail: "omit",
    post_status: "draft",
  })
  // And a destination that names none keeps the publisher's own defaults
  // rather than being given one this page invented.
  assert.equal(DEFAULT_THUMBNAIL, "source")
  assert.equal(DEFAULT_POST_STATUS, "publish")
})

test("changing the destination account clears old channels and scopes eligibility", () => {
  const draft = withDestinations(telegramRow(77), telegramRow(78))
  const moved = updateBlock(draft.destinations, `tg:${DESTINATION_ACCOUNT.id}`, {
    accountID: OTHER_DESTINATION_ACCOUNT.id,
    clientKey: "custom",
    channelIDs: [],
  })
  assert.equal(moved.length, 1)
  assert.equal(moved[0].accountID, OTHER_DESTINATION_ACCOUNT.id)
  const selected = updateBlock(moved, `tg:${OTHER_DESTINATION_ACCOUNT.id}`, {
    channelIDs: [78],
  })
  const kept = trimChannels(selected, OTHER_DESTINATION_ACCOUNT.id, [78])
  assert.equal(kept.length, 1)
  assert.equal(kept[0].channelID, 78)
  // The block survives with an empty picker rather than vanishing.
  const none = trimChannels(selected, OTHER_DESTINATION_ACCOUNT.id, [])
  assert.equal(destinationBlocks(none).length, 1)
  assert.equal(destinationBlocks(none)[0].complete, false)
})

test("destination channels are fetched through the exact account route", async () => {
  const calls = stubApi((call) => {
    if (call.path === `/api/v1/telegram/accounts/${OTHER_DESTINATION_ACCOUNT.id}/channels`) {
      return ok({
        channels: [
          { id: "78", title: "کانال دوم", username: "two" },
          { id: 90, title: "کانال سوم", permissions: { new: false } },
        ],
      })
    }
    return refuse(404, "not_found", "no such route " + call.path)
  })
  const items = await eligibleChannels("tok", OTHER_DESTINATION_ACCOUNT.id)
  assert.equal(calls.length, 1)
  assert.equal(
    calls[0].path,
    `/api/v1/telegram/accounts/${OTHER_DESTINATION_ACCOUNT.id}/channels`
  )
  assert.match(calls[0].query, /capability=send/)
  // Authorization is read, never assumed: a channel the account may not post
  // in is not offered as a destination. The id arrives as a decimal string and
  // is read as the number the automation contract carries.
  assert.deepEqual(
    items.map((item) => item.id),
    [78]
  )
  assert.equal(items[0].detail, "@two")
})

test("a destination block is added and removed as rows", () => {
  const empty = emptyWorkflowDraft().destinations
  const added = addDestination(empty, "wordpress")
  assert.equal(added.length, 1)
  assert.equal(destinationBlocks(added)[0].type, "wordpress")
  assert.equal(destinationBlocks(added)[0].complete, false)
  const filled = updateBlock(added, "wp:new:0", { siteID: 4 })
  assert.equal(filled.length, 1)
  assert.equal(isCompleteDestination(filled[0]), true)
  assert.equal(removeDestination(filled, "wp:4").length, 0)
})

test("multiple destinations persist as the rows the schema expects", () => {
  const draft = withDestinations(telegramRow(77), telegramRow(78), {
    ...wordpressDestination(4),
    options: { category_ids: [7] },
  })
  const blocks = destinationBlocks(draft.destinations)
  assert.equal(blocks.length, 2, "one Telegram block and one WordPress block")
  assert.deepEqual(blocks[0].channelIDs, [77, 78])
  assert.equal(blocks[1].siteID, 4)

  const body = workflowBody(draft) as {
    destinations: Record<string, unknown>[]
  }
  assert.equal(body.destinations.length, 3)
  assert.deepEqual(
    body.destinations.map((row) => [
      row.destination_type,
      row.channel_id,
      row.site_id,
    ]),
    [
      ["telegram", 77, 0],
      ["telegram", 78, 0],
      ["wordpress", 0, 4],
    ]
  )
  // A Telegram destination names no site and a WordPress one names no client.
  assert.equal(body.destinations[0].site_id, 0)
  assert.equal(body.destinations[2].client_key, "")
})

test("options this page has no editor for survive a save", () => {
  const draft = withDestinations({
    ...telegramRow(77),
    options: { thumbnail: "omit", silent: true },
  })
  const body = workflowBody(draft) as {
    destinations: Record<string, unknown>[]
  }
  assert.deepEqual(body.destinations[0].options, {
    thumbnail: "omit",
    silent: true,
  })
})

// --- AI ---------------------------------------------------------------------

test("AI is off by default and its settings are not on the page", () => {
  assert.equal(emptyWorkflowDraft().aiEnabled, false)
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("ویرایش پیام با هوش مصنوعی"))
  assert.ok(!out.includes("پرامپت اختصاصی"), "no prompt while the stage is off")
  assert.ok(!out.includes("ارسال تصاویر به هوش مصنوعی"))
  assert.ok(!out.includes("انتشار پیام در صورت خطای هوش مصنوعی"))
  assert.equal(on(out), 0)
  assert.equal(off(out), 1, "and the main switch reads off")
})

test("the AI stage carries exactly the specified labels and helpers", () => {
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  assert.ok(out.includes("ویرایش پیام با هوش مصنوعی"))
  assert.ok(out.includes("پرامپت اختصاصی"))
  assert.ok(
    out.includes("در صورت خالی بودن، متن با پرامپت پیش‌فرض ویرایش می‌شود.")
  )
  assert.ok(out.includes("ارسال تصاویر به هوش مصنوعی"))
  assert.ok(
    out.includes(
      "این گزینه فقط ورودی هوش مصنوعی را کنترل می‌کند، نه رسانه‌های ارسالی به مقصد."
    )
  )
  assert.ok(out.includes("انتشار پیام در صورت خطای هوش مصنوعی"))
  assert.ok(
    out.includes("در صورت خطای هوش مصنوعی، پیام اصلی بدون ویرایش منتشر می‌شود.")
  )
})

test("the prompt is optional", () => {
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true, aiPrompt: "" })
  assert.ok(out.includes("اختیاری"))
  // An empty prompt is a choice, not a failure: nothing marks the field.
  assert.ok(!out.includes("required"))
})

test("images to AI and publish-on-error are off until they are turned on", () => {
  assert.equal(emptyWorkflowDraft().aiSendImages, false)
  assert.equal(emptyWorkflowDraft().aiPublishOnFailure, false)
  const bothOff = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  assert.equal(on(bothOff), 1, "only the AI stage itself is on")
  assert.equal(off(bothOff), 2, "both of its flags read off")
  const bothOn = render({
    ...emptyWorkflowDraft(),
    aiEnabled: true,
    aiSendImages: true,
    aiPublishOnFailure: true,
  })
  assert.equal(on(bothOn), 3)
  assert.equal(off(bothOn), 0)
})

test("excluding images never implies excluding them from the destination", () => {
  const out = render({
    ...emptyWorkflowDraft(),
    aiEnabled: true,
    aiSendImages: false,
  })
  // The helper says exactly what the switch controls: AI input, not the media
  // published to the destination.
  assert.ok(
    out.includes(
      "این گزینه فقط ورودی هوش مصنوعی را کنترل می‌کند، نه رسانه‌های ارسالی به مقصد."
    )
  )
})

test("AI settings persist through a save and a reload", () => {
  const draft = withDestinations(telegramRow(77))
  const held: WorkflowDraft = {
    ...draft,
    aiEnabled: true,
    aiPrompt: "لحن رسمی",
    aiSendImages: true,
    aiPublishOnFailure: true,
  }
  const stored = workflowDraftOf(readWorkflow(viewOf(workflowBody(held))))
  assert.equal(stored.aiEnabled, true)
  assert.equal(stored.aiPrompt, "لحن رسمی")
  assert.equal(stored.aiSendImages, true)
  assert.equal(stored.aiPublishOnFailure, true)
})

test("turning AI off and back on drops neither the prompt nor the destinations", () => {
  const draft: WorkflowDraft = {
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    aiEnabled: true,
    aiPrompt: "لحن رسمی",
    aiSendImages: true,
    destinations: [telegramRow(77), { ...wordpressDestination(4) }],
  }
  // The switch writes one field; the rest of the draft is what it was. Off, the
  // AI settings are hidden but the destinations are still on the page.
  const hidden = render({ ...draft, aiEnabled: false })
  assert.ok(!hidden.includes("پرامپت اختصاصی"))
  assert.ok(hidden.includes("کانال‌های مقصد"), "destinations are untouched")
  assert.ok(hidden.includes("سایت وردپرسی"))
  // Back on, the typed prompt and both flags are still there.
  const shown = render({ ...draft, aiEnabled: true })
  assert.ok(shown.includes("لحن رسمی"), "the typed prompt is still held")
  assert.ok(shown.includes("کانال‌های مقصد"))
  assert.equal(on(shown), 2, "the stage and the flag that was turned on")
  assert.equal(off(shown), 1)
})

// --- status -----------------------------------------------------------------

test("the status row is one row: label on the right, switch on the left", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("وضعیت ورک‌فلو"))
  assert.ok(out.includes("فعال بودن ورک‌فلو"))
  assert.ok(
    out.includes(
      "ورک‌فلوی غیرفعال، رویدادهای تازه را نمی‌پذیرد و چیزی منتشر نمی‌کند."
    )
  )
})

test("a workflow that publishes nowhere is not offered as enabled", () => {
  const out = render(emptyWorkflowDraft())
  assert.match(out, /برای فعال‌سازی دست‌کم یک مقصد فعال لازم است/)
})

test("an edit keeps the stored enabled value", () => {
  const stored = readWorkflow(
    viewOf(
      workflowBody({
        ...emptyWorkflowDraft(),
        managedChannelID: 5,
        enabled: true,
        destinations: [telegramRow(77)],
      })
    )
  )
  const draft = workflowDraftOf(stored)
  assert.equal(draft.enabled, true, "opening the page must not switch it off")
  // The workflow's own row is the only switch in this draft whose state word is
  // its own: no destination blocks, so nothing else says فعال.
  const out = render({ ...draft, destinations: [] })
  assert.equal(count(out, '<span class="text-sm">فعال</span>'), 1)
  assert.equal(count(out, '<span class="text-sm">غیرفعال</span>'), 0)
})

// --- the real conversation: save, reload ------------------------------------

interface Call {
  method: string
  path: string
  query: string
  body: unknown
}

function stubApi(
  answer: (call: Call) => { status: number; payload: unknown }
): Call[] {
  const calls: Call[] = []
  const stored = new Map<string, string>([["telesync.panel.token", "tok"]])
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
    sessionStorage: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => void stored.set(key, value),
      removeItem: (key: string) => void stored.delete(key),
    },
  }
  ;(globalThis as Record<string, unknown>).fetch = async (
    input: unknown,
    init: RequestInit = {}
  ) => {
    const url = new URL(String(input), "http://panel.test")
    const call: Call = {
      method: init.method ?? "GET",
      path: url.pathname,
      query: url.search,
      body: init.body ? JSON.parse(String(init.body)) : undefined,
    }
    calls.push(call)
    const { status, payload } = answer(call)
    return new Response(JSON.stringify(payload), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  }
  return calls
}

const me = {
  id: 1,
  fullname: "Operator",
  username: "op",
  is_active: true,
  created_at: "",
  updated_at: "",
  access: {
    channels: true,
    categories: true,
    users: true,
    servers: true,
    automation: true,
  },
}

const ok = (data: unknown, meta: Record<string, unknown> = {}) => ({
  status: 200,
  payload: { data, meta },
})
const refuse = (status: number, code: string, message: string) => ({
  status,
  payload: { error: { code, message } },
})

const request = (url: string) => new Request(`http://panel.test${url}`)

const submitTo = (path: string, fields: Record<string, string>) => {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.set(key, value)
  return {
    url: `http://panel.test${path}`,
    formData: async () => form,
  } as unknown as Request
}

// Exactly what the gateway renders back for one workflow. `viewOf` builds it
// from a posted body, so the record a reload reads is the record the save made.
function viewOf(body: Record<string, unknown>, id = 12) {
  const destinations = (body.destinations ?? []) as Record<string, unknown>[]
  return {
    id,
    managed_channel_id: body.managed_channel_id,
    account_id: body.account_id ?? "",
    channel: {
      id: body.managed_channel_id,
      title: "کانال خبری",
      username: "news",
      client_key: "official",
      telegram_channel_id: 123456,
    },
    enabled: body.enabled,
    ai_enabled: body.ai_enabled,
    ai_prompt: body.ai_prompt,
    ai_send_images: body.ai_send_images,
    ai_publish_on_failure: body.ai_publish_on_failure,
    config_revision: 1,
    destinations: destinations.map((row, index) => ({
      id: index + 1,
      destination_type: row.destination_type,
      client_key: row.client_key,
      channel_id: row.channel_id,
      site_id: row.site_id,
      account_id: row.account_id ?? "",
      options: row.options ?? {},
      position: row.position,
      enabled: row.enabled,
    })),
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
  }
}

// The one account of the caller's own that this fake gateway knows. A workflow
// binds to it, and the source channel above is scoped to exactly this set.
const WORKFLOW_ACCOUNT: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب نخست",
  status: "connected",
}

const DESTINATION_ACCOUNT: TelegramAccount = {
  id: "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب مقصد",
  status: "connected",
}

const OTHER_DESTINATION_ACCOUNT: TelegramAccount = {
  id: "acc_cccccccccccccccccccccccccc",
  server_key: "custom",
  network_type: "custom",
  remote_display_name: "حساب مقصد دوم",
  status: "connected",
}

// A stand-in of the gateway that keeps what it was given and reads it back, so
// a save and the reload after it are one conversation.
function fakeGateway() {
  const store = new Map<number, Record<string, unknown>>()
  const calls: Call[] = []
  stubApi((call) => {
    calls.push(call)
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.path === "/api/v1/me/telegram-accounts") {
      return ok({ accounts: [WORKFLOW_ACCOUNT] })
    }
    if (call.path === "/api/v1/local/channels") {
      return ok({
        channels: [
          {
            id: 5,
            title: "کانال خبری",
            client_key: "official",
            account_id: WORKFLOW_ACCOUNT.id,
          },
        ],
      })
    }
    if (call.path === "/api/v1/telegram") return ok({ servers: [] })
    if (call.path === "/api/v1/local/wordpress") {
      return ok({
        sites: [{ id: 4, title: "خبرگزاری", domain: "news.example.com" }],
      })
    }
    if (call.path === "/api/v1/local/automation/workflows") {
      if (call.method === "POST") {
        const body = call.body as Record<string, unknown>
        store.set(12, viewOf(body))
        return {
          status: 201,
          payload: { data: { workflow: store.get(12) }, meta: {} },
        }
      }
      return ok(
        { workflows: [...store.values()] },
        { total: store.size, has_more: false }
      )
    }
    const own = /^\/api\/v1\/local\/automation\/workflows\/(\d+)$/.exec(
      call.path
    )
    if (own) {
      const id = Number(own[1])
      if (call.method === "PATCH") {
        if (!store.has(id)) return refuse(404, "not_found", "no such workflow")
        store.set(id, viewOf(call.body as Record<string, unknown>, id))
        return ok({ workflow: store.get(id) })
      }
      return store.has(id)
        ? ok({ workflow: store.get(id) })
        : refuse(404, "not_found", "no such workflow")
    }
    return refuse(404, "not_found", "no such route " + call.path)
  })
  return { store, calls }
}

const newWorkflowFields = {
  intent: "create",
  managed_channel_id: "5",
  enabled: "false",
  ai_enabled: "true",
  ai_prompt: "لحن رسمی",
  ai_send_images: "true",
  ai_publish_on_failure: "false",
  destinations: JSON.stringify([
    {
      type: "telegram",
      accountID: DESTINATION_ACCOUNT.id,
      clientKey: "official",
      channelID: 999,
      siteID: null,
      options: {},
      enabled: true,
    },
    {
      type: "wordpress",
      accountID: "",
      clientKey: "",
      channelID: null,
      siteID: 4,
      options: { category_ids: [7], tag_ids: [9], thumbnail: "omit" },
      enabled: true,
    },
  ]),
}

test("saving a new workflow posts exactly what the page holds", async () => {
  const { calls } = fakeGateway()
  const result = (await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as { error?: string; saved?: boolean; intent?: string }

  assert.equal(result.error, undefined)
  assert.equal(result.saved, true)
  assert.equal(result.intent, "create")
  const post = calls.find((call) => call.method === "POST")
  assert.equal(post?.path, "/api/v1/local/automation/workflows")
  const sent = post?.body as Record<string, unknown>
  assert.equal(sent.managed_channel_id, 5)
  assert.equal(sent.enabled, false)
  assert.equal(sent.ai_enabled, true)
  assert.equal(sent.ai_prompt, "لحن رسمی")
  assert.equal(sent.ai_send_images, true)
  assert.equal(sent.ai_publish_on_failure, false)
  assert.equal((sent.destinations as unknown[]).length, 2)
})

test("create → edit → reload keeps the same persisted configuration", async () => {
  const { store } = fakeGateway()
  await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)

  // GET: the edit page reads the stored row back.
  const loaded = (await workflowFormLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12/edit"),
  } as never)) as { status: string; workflow?: ReturnType<typeof readWorkflow> }
  assert.equal(loaded.status, "ok")
  assert.ok(loaded.workflow)
  const editDraft = workflowDraftOf(loaded.workflow as never)
  assert.equal(editDraft.managedChannelID, 5)
  assert.equal(editDraft.aiEnabled, true)
  assert.equal(editDraft.aiPrompt, "لحن رسمی")
  assert.equal(editDraft.aiSendImages, true)
  assert.equal(editDraft.aiPublishOnFailure, false)
  assert.equal(editDraft.destinations.length, 2)
  assert.deepEqual(editDraft.destinations[1].options, {
    category_ids: [7],
    tag_ids: [9],
    thumbnail: "omit",
  })

  // Save from the edit page.
  const saved = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12/edit", {
      ...newWorkflowFields,
      intent: "update",
    }),
  } as never)) as { error?: string; saved?: boolean }
  assert.equal(saved.saved, true)

  // Reload the browser: the same URL answers with what the store kept.
  const reloaded = (await workflowFormLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12/edit"),
  } as never)) as { status: string; workflow?: ReturnType<typeof readWorkflow> }
  const again = workflowDraftOf(reloaded.workflow as never)
  assert.deepEqual(
    workflowBody(again),
    workflowBody(editDraft),
    "the reload shows the configuration that was saved"
  )
  assert.equal(store.size, 1, "one workflow for one source channel")
})

test("an edit that turns the workflow on stays on after a reload", async () => {
  fakeGateway()
  await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)
  await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12/edit", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)
  const reloaded = (await workflowFormLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12/edit"),
  } as never)) as { workflow?: { enabled?: boolean } }
  assert.equal(reloaded.workflow?.enabled, true)
})

test("an incomplete destination is refused and never posted", async () => {
  const { calls } = fakeGateway()
  const result = (await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", {
      ...newWorkflowFields,
      destinations: JSON.stringify([
        {
          type: "telegram",
          clientKey: "official",
          channelID: null,
          siteID: null,
          options: {},
          enabled: true,
        },
      ]),
    }),
  } as never)) as {
    error?: string
    saved?: boolean
    fieldErrors?: Record<string, string>
  }
  assert.equal(result.saved, undefined)
  assert.ok(result.error)
  assert.ok(result.fieldErrors?.destinations)
  assert.equal(
    calls.some((call) => call.method === "POST"),
    false,
    "no destination that names no channel and no site is ever written"
  )
})

test("a 409 names the workflow that holds the channel and links to it", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.method === "POST") {
      return refuse(
        409,
        "conflict",
        "a workflow already exists for that source channel"
      )
    }
    return ok({
      workflows: [viewOf(workflowBody(withDestinations(telegramRow(77))))],
    })
  })
  const result = (await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as {
    error?: string
    conflictWorkflowID?: number | null
    conflictChannelLabel?: string
    fieldErrors?: Record<string, string>
  }
  assert.equal(result.conflictWorkflowID, 12)
  assert.equal(result.conflictChannelLabel, "کانال خبری")
  assert.ok(
    result.fieldErrors?.source,
    "the refusal points at the source field"
  )

  const conflict: ConflictWorkflow = {
    id: result.conflictWorkflowID as number,
    channelLabel: result.conflictChannelLabel as string,
  }
  const out = render(emptyWorkflowDraft(), {
    error: result.error,
    conflict,
    fieldErrors: result.fieldErrors,
  })
  assert.match(out, /href="\/automation\/workflows\/12\/edit"/)
  assert.ok(out.includes("ویرایش ورک‌فلوی «کانال خبری»"))
})

test("an enable the backend refuses is shown and put back", async () => {
  const message =
    "a workflow needs at least one enabled destination before it can be enabled"
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(400, "invalid_input", message)
  })
  const result = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12/edit", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)) as {
    error?: string
    revertEnabled?: boolean
    fieldErrors?: Record<string, string>
  }
  assert.equal(
    result.error,
    message,
    "the server's own sentence, kept as written"
  )
  assert.equal(result.revertEnabled, true, "and the switch is put back")
  assert.equal(result.fieldErrors?.status, message)

  const out = render(
    { ...emptyWorkflowDraft(), enabled: false, managedChannelID: 5 },
    { error: result.error, fieldErrors: result.fieldErrors }
  )
  assert.ok(out.includes(message))
})

test("an enable refused for having no accountable owner is shown too", async () => {
  const message =
    "enabling a workflow needs an authenticated account: a workflow publishes under the authority of the account that created it."
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(400, "invalid_input", message)
  })
  const result = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12/edit", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)) as { error?: string; revertEnabled?: boolean }
  assert.equal(result.error, message)
  assert.equal(result.revertEnabled, true)
})

test("a refused write is never reported as a save", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(403, "automation_destination_forbidden", "no")
  })
  const result = (await workflowAction({
    params: {},
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as {
    error?: string
    saved?: boolean
    fieldErrors?: Record<string, string>
  }
  assert.equal(result.saved, undefined)
  assert.ok(result.error)
  assert.ok(result.fieldErrors?.destinations)
})

test("a deleted workflow is reported as missing, not as a failure", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(404, "not_found", "no such workflow")
  })
  const data = (await workflowFormLoader({
    params: { workflowId: "99" },
    request: request("/automation/workflows/99/edit"),
  } as never)) as { status: string }
  assert.equal(data.status, "missing")
})

test("a save and a cancel both go back to the Workflows listing", () => {
  assert.equal(workflowFormReturnTo(undefined), "/automation?tab=workflows")
  assert.equal(
    workflowFormReturnTo({ from: "/automation?tab=workflows&q=news&page=2" }),
    "/automation?tab=workflows&q=news&page=2",
    "a link that came from the listing goes back to that exact view"
  )
  // A state that did not come from the listing is never followed.
  assert.equal(
    workflowFormReturnTo({ from: "https://example.com" }),
    "/automation?tab=workflows"
  )
})

// --- the polish: the dependent source and the shared shell -------------------

// A button tag with its class list taken off: the shared Button always carries
// `disabled:` variants, which say nothing about the button's own state.
const buttonTag = (out: string, type: string) =>
  ((out.match(/<button\b[^>]*>/g) ?? []).find((tag) =>
    tag.includes(`type="${type}"`)
  ) ?? "").replace(/ class="[^"]*"/, "")

test("the connection is chosen before the channel that follows from it", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("سرور تلگرام"), "the connection is asked for")
  assert.ok(
    out.indexOf('id="workflow-source-server"') <
      out.indexOf('id="workflow-channel"'),
    "and it is above the channel picker"
  )
  assert.ok(out.includes("نخست سرور تلگرام را انتخاب کنید"))
})

test("a workflow with no source cannot be saved at all", () => {
  const blank = render(emptyWorkflowDraft())
  assert.ok(
    / disabled[ =>/]/.test(buttonTag(blank, "submit")),
    "no connection and no channel: nothing to save"
  )
  const sourced = render({ ...emptyWorkflowDraft(), managedChannelID: 5 })
  assert.ok(
    !/ disabled[ =>/]/.test(buttonTag(sourced, "submit")),
    "one channel on the chosen connection is exactly the source this needs"
  )
})

test("the actions are Cancel then Save, so Save is the outermost control on the left", () => {
  const out = render({ ...emptyWorkflowDraft(), managedChannelID: 5 })
  const cancel = out.indexOf('type="button"')
  const submit = out.indexOf('type="submit"')
  assert.ok(cancel > -1 && submit > cancel, out.slice(cancel, submit + 20))
  assert.ok(out.includes("انصراف"))
})

test("the page is drawn in the shared automation layout", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("max-w-4xl"), "the shared content width")
  assert.ok(out.includes("mx-auto"), "centred in the panel's own column")
})

test("this form has no advanced-only settings, so it takes no accordion", () => {
  // Everything on it is essential — the source, the destinations, the AI stage
  // and the status — and none of it is hidden away behind a section that would
  // only make it harder to reach. The webhook is the one with tuning to spare.
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  assert.ok(!/data-slot="accordion-trigger"/.test(out), "no accordion here")
  assert.ok(out.includes("کانال مبدأ"))
  assert.ok(out.includes("مقصدهای انتشار"))
  assert.ok(out.includes("پرامپت اختصاصی"), "the AI stage stays on the page")
  assert.ok(out.includes("وضعیت ورک‌فلو"))
})
