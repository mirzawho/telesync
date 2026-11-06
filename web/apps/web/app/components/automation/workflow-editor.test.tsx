import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  WorkflowForm,
  type NamedOption,
  type TakenWorkflow,
} from "@/components/automation/workflow-form"
import {
  DEFAULT_THUMBNAIL,
  WordPressDestinationOptions,
  thumbnailOf,
  thumbnailPolicyLabels,
  type SiteTerms,
} from "@/components/automation/workflow-wordpress-options"
import { RunStatusBadge } from "@/components/automation/status"
import { faDigits } from "@/components/persian-digits"
import {
  clientAction as workflowAction,
  clientLoader as workflowEditorLoader,
} from "../../routes/_panel/automation-workflow-editor"
import { WorkflowsTable } from "../../routes/_panel/automation-workflows"
import { RunsTable } from "../../routes/_panel/automation-runs"
import { RunDetail } from "../../routes/_panel/automation-run"
import {
  emptyWorkflowDraft,
  readRun,
  readWorkflow,
  telegramDestination,
  wordpressDestination,
  workflowBody,
  workflowDraftOf,
  type DestinationDraft,
  type WorkflowDraft,
} from "@/lib/automation"

const html = (node: React.ReactNode) =>
  renderToStaticMarkup(<MemoryRouter initialEntries={["/automation"]}>{node}</MemoryRouter>)

const sourceChannels: NamedOption[] = [
  { id: 5, label: "کانال خبری", detail: "official" },
  { id: 8, label: "کانال ورزشی", detail: "sport" },
]
const sites: NamedOption[] = [
  { id: 4, label: "خبرگزاری", detail: "news.example.com" },
]

const telegram = (channelID: number, clientKey = "official"): DestinationDraft => ({
  type: "telegram",
  clientKey,
  channelID,
  siteID: null,
  options: {},
  enabled: true,
})

const render = (
  draft: WorkflowDraft,
  over: Partial<React.ComponentProps<typeof WorkflowForm>> = {}
) =>
  html(
    <WorkflowForm
      draft={draft}
      onDraftChange={() => {}}
      sourceChannels={sourceChannels}
      taken={new Map<number, TakenWorkflow>()}
      servers={[
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
      ]}
      sites={sites}
      destinationChannels={[{ id: 77, label: "کانال مقصد" }]}
      loadingChannels={false}
      pending={false}
      onSubmit={() => {}}
      onCancel={() => {}}
      {...over}
    />
  )

// --- source -----------------------------------------------------------------

test("the source is one managed channel, named", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("مبدأ"))
  assert.ok(out.includes("کانال مبدأ"))
  assert.ok(out.includes("فقط یک"))
  // One field, not a list: the picker is a single-select over the managed rows.
  assert.equal(out.match(/for="workflow-channel"/g)?.length, 1)
  assert.deepEqual(
    sourceChannels.map((channel) => channel.label),
    ["کانال خبری", "کانال ورزشی"]
  )
})

test("a channel that already has a workflow points at it instead", () => {
  const draft = { ...emptyWorkflowDraft(), managedChannelID: 5 }
  const out = render(draft, {
    taken: new Map<number, TakenWorkflow>([
      [5, { id: 12, channelLabel: "کانال خبری" }],
    ]),
  })
  assert.match(out, /برای «کانال خبری» ورک‌فلوی دیگری ثبت شده است/)
  assert.ok(out.includes("ویرایش ورک‌فلو"), "the way out is offered")
  assert.match(out, /href="\/automation\/workflows\/12"/)
  // The warning names the rule the schema enforces.
  assert.ok(out.includes("فقط یک ورک‌فلو"))
})

// --- destinations -----------------------------------------------------------

test("destinations cover a Telegram connection, its channels and the sites", () => {
  const draft: WorkflowDraft = {
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [telegram(77), { ...telegram(0), type: "wordpress", siteID: 4 }],
  }
  const out = render(draft, { destinationChannels: [{ id: 77, label: "کانال مقصد" }] })
  assert.ok(out.includes("مقاصد"))
  assert.ok(out.includes("کانال‌های تلگرامی"))
  assert.ok(out.includes("اتصال تلگرام"))
  assert.ok(out.includes("کانال‌های مقصد"))
  assert.ok(out.includes("سایت‌های وردپرسی"))
  assert.ok(out.includes("سایت‌های مقصد"))
  // The summary names what will actually be saved, in the terms of the record.
  assert.ok(out.includes("official"))
  assert.ok(out.includes("site 4"))
})

test("with no destination the form says nothing will be published", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("هنوز مقصدی انتخاب نشده است"))
  assert.ok(out.includes("پیامی منتشر نمی‌شود"))
})

test("changing the connection is offered as one connection per workflow", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("اتصال تلگرام"))
  assert.ok(out.includes("برای افزودن مقصد تلگرامی، اول یک اتصال را انتخاب کنید."))
})

// --- AI settings ------------------------------------------------------------

const AI_LABELS = [
  "ویرایش پیام با هوش مصنوعی",
  "پرامپت اختصاصی",
  "ارسال تصاویر به هوش مصنوعی",
  "انتشار در صورت خطای هوش مصنوعی",
]

test("the AI section carries exactly the specified labels", () => {
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  for (const label of AI_LABELS) {
    assert.ok(out.includes(label), label)
  }
})

test("the four AI options are on the page together and all default to off", () => {
  const draft = emptyWorkflowDraft()
  assert.equal(draft.aiEnabled, false, "a new workflow starts with AI off")
  assert.equal(draft.aiSendImages, false)
  assert.equal(draft.aiPublishOnFailure, false)
  assert.equal(draft.aiPrompt, "")
  // All four are rendered at the defaults, so what will be saved is visible
  // rather than hidden behind the switch that turns the stage on.
  const out = render(draft)
  for (const label of AI_LABELS) {
    assert.ok(out.includes(label), label)
  }
  assert.equal(
    out.match(/<span class="text-sm">خاموش<\/span>/g)?.length,
    3,
    "the three AI switches read خاموش"
  )
  assert.ok(
    !/<span class="text-sm">روشن<\/span>/.test(out),
    "none of them reads روشن"
  )
})

test("the default-prompt hint appears only when AI is on and the prompt is empty", () => {
  const HINT = "در صورت خالی بودن، از پرامپت پیش‌فرض ویرایش محتوا استفاده می‌شود."
  assert.ok(
    !render({ ...emptyWorkflowDraft(), aiEnabled: false, aiPrompt: "" }).includes(
      HINT
    ),
    "hidden while AI is off"
  )
  assert.ok(
    render({ ...emptyWorkflowDraft(), aiEnabled: true, aiPrompt: "" }).includes(HINT),
    "shown when AI is on and the prompt is empty"
  )
  assert.ok(
    !render({
      ...emptyWorkflowDraft(),
      aiEnabled: true,
      aiPrompt: "لحن رسمی",
    }).includes(HINT),
    "hidden once the operator wrote their own prompt"
  )
})

test("the fallback switch says it publishes the original content", () => {
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  assert.ok(out.includes("همان متن اصلی بدون ویرایش منتشر می‌شود"))
  assert.ok(out.includes("اگر خاموش باشد، پیام منتشر نمی‌شود"))
})

test("excluding images never implies excluding them from the destination", () => {
  const out = render({ ...emptyWorkflowDraft(), aiEnabled: true })
  assert.ok(out.includes("خود تصاویر همچنان در مقصد منتشر می‌شوند"))
})

// --- status and save --------------------------------------------------------

test("the status section and the save are both present", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("وضعیت"))
  assert.ok(out.includes("وضعیت ورک‌فلو"))
  assert.ok(out.includes("ورک‌فلوی غیرفعال، رویدادهای تازه را نمی‌پذیرد"))
  assert.ok(out.includes("ذخیره"))
  assert.ok(out.includes("انصراف"))
})

test("the submit label follows the screen it is on", () => {
  assert.ok(render(emptyWorkflowDraft(), { submitLabel: "ایجاد ورک‌فلو" }).includes("ایجاد ورک‌فلو"))
  assert.ok(render(emptyWorkflowDraft(), { pending: true }).includes("در حال ذخیره…"))
})

test("a server refusal is rendered inside the form", () => {
  const out = render(emptyWorkflowDraft(), {
    error: "برای این کانال ورک‌فلوی دیگری ثبت شده است.",
  })
  assert.match(out, /role="alert"/)
})

test("the form is one form", () => {
  assert.equal(render(emptyWorkflowDraft()).match(/<form/g)?.length, 1)
})

// --- the enable refusal ------------------------------------------------------

test("a workflow that publishes nowhere is not offered as enabled", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(out.includes("برای فعال‌سازی دست‌کم یک مقصد فعال لازم است"))
  // The save is still offered: a disabled workflow with no destinations is a
  // perfectly good thing to store.
  assert.ok(out.includes("ذخیره"))
})

test("the switch cannot be turned on when the backend would refuse it", () => {
  const out = render({ ...emptyWorkflowDraft(), enabled: true })
  assert.ok(out.includes("فعال‌سازی بدون مقصد فعال ممکن نیست"))
  assert.ok(!out.includes("در حال ذخیره…"))
})

test("with an enabled destination the save is offered again", () => {
  const out = render({
    ...emptyWorkflowDraft(),
    enabled: true,
    destinations: [telegram(-100999)],
  })
  assert.ok(out.includes("ذخیره"))
  assert.ok(!out.includes("فعال‌سازی بدون مقصد فعال ممکن نیست"))
})

// --- the duplicate-workflow refusal -----------------------------------------

test("a conflict names the workflow that already holds the channel and links to it", () => {
  const out = render(emptyWorkflowDraft(), {
    error: "برای این کانال ورک‌فلوی دیگری ثبت شده است.",
    conflict: { id: 12, channelLabel: "کانال خبری" },
  })
  assert.ok(out.includes("برای این کانال ورک‌فلوی دیگری ثبت شده است."))
  assert.ok(out.includes("ویرایش ورک‌فلوی «کانال خبری»"))
  assert.match(out, /href="\/automation\/workflows\/12"/)
})

test("a refusal with nowhere else to go shows the reason and no link", () => {
  const out = render(emptyWorkflowDraft(), {
    error: "شما مجوز این عمل را ندارید.",
    conflict: null,
  })
  assert.ok(out.includes("شما مجوز این عمل را ندارید."))
  assert.ok(!out.includes("ویرایش ورک‌فلوی"))
})

// --- the source is fixed after creation -------------------------------------

test("an edit says the source channel cannot be moved instead of offering it", () => {
  const out = render(
    { ...emptyWorkflowDraft(), managedChannelID: 5 },
    { sourceLocked: true }
  )
  assert.ok(out.includes("کانال مبدأ پس از ایجاد ورک‌فلو قابل تغییر نیست"))
  assert.ok(out.includes("data-disabled"), "the picker cannot be used")
})

test("a new workflow still offers every managed channel", () => {
  const out = render(emptyWorkflowDraft())
  assert.ok(!out.includes("data-disabled"))
  assert.ok(out.includes("پیام‌های تازهٔ این کانال، ورک‌فلو را اجرا می‌کنند"))
})

test("editing a workflow does not call its own source channel taken", () => {
  // The row being edited holds that channel; offering "edit the workflow that
  // has it" would point at the record already open.
  const out = render(
    { ...emptyWorkflowDraft(), managedChannelID: 5 },
    {
      sourceLocked: true,
      taken: new Map<number, TakenWorkflow>(),
    }
  )
  assert.ok(!out.includes("ورک‌فلوی دیگری ثبت شده است"))
  assert.ok(out.includes("ذخیره"))
})

// --- destinations write exactly one address ---------------------------------

const bodyOf = (draft: WorkflowDraft) =>
  workflowBody(draft) as {
    destinations: Record<string, unknown>[]
  }

test("a Telegram destination names a connection and a peer id, never a site", () => {
  const rows = bodyOf({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [telegramDestination("official", -100999)],
  }).destinations
  assert.equal(rows.length, 1)
  assert.equal(rows[0].destination_type, "telegram")
  assert.equal(rows[0].client_key, "official")
  assert.equal(rows[0].channel_id, -100999, "channel_id is the Telegram peer id")
  assert.equal(rows[0].site_id, 0, "a telegram row never names a site")
  assert.equal(rows[0].position, 0)
})

test("a WordPress destination names a site, never a client or a peer", () => {
  const rows = bodyOf({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [wordpressDestination(4)],
  }).destinations
  assert.equal(rows.length, 1)
  assert.equal(rows[0].destination_type, "wordpress")
  assert.equal(rows[0].site_id, 4, "site_id is the registered site's id")
  assert.equal(rows[0].client_key, "", "a wordpress row never names a client")
  assert.equal(rows[0].channel_id, 0, "and never a Telegram peer id")
})

test("each row of a mixed workflow keeps its own address", () => {
  const rows = bodyOf({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [
      telegramDestination("official", -100999),
      wordpressDestination(4),
      telegramDestination("sport", -100777),
    ],
  }).destinations
  assert.deepEqual(
    rows.map((row) => [row.destination_type, row.client_key, row.channel_id, row.site_id]),
    [
      ["telegram", "official", -100999, 0],
      ["wordpress", "", 0, 4],
      ["telegram", "sport", -100777, 0],
    ]
  )
  assert.deepEqual(
    rows.map((row) => row.position),
    [0, 1, 2]
  )
})

test("a managed channel id and a peer id are never swapped", () => {
  // managed_channel_id is a local row id; channel_id is a Telegram peer id.
  // The two answer different questions and the payload keeps them apart.
  const body = workflowBody({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [telegramDestination("official", -100999)],
  }) as Record<string, unknown>
  assert.equal(body.managed_channel_id, 5, "the local managed_channels.id")
  assert.equal(
    (body.destinations as Record<string, unknown>[])[0].channel_id,
    -100999,
    "the Telegram peer id"
  )
})

// --- WordPress publishing settings -----------------------------------------

const terms: SiteTerms = {
  categories: [
    { id: 7, label: "اخبار" },
    { id: 8, label: "ورزش" },
  ],
  tags: [{ id: 9, label: "فوری" }],
  loading: false,
  error: null,
}

const siteOptions = (over: Record<string, unknown> = {}) =>
  render(
    {
      ...emptyWorkflowDraft(),
      managedChannelID: 5,
      destinations: [
        { ...wordpressDestination(4), options: { ...over } },
      ],
    },
    { siteTerms: new Map([[4, terms]]) }
  )

test("a WordPress site keeps its own category, tag and thumbnail settings", () => {
  const out = siteOptions()
  assert.ok(out.includes("خبرگزاری"), "the site is named")
  assert.ok(out.includes("تصویر شاخص نوشته"))
  assert.ok(out.includes("دسته‌بندی‌های وردپرس"))
  assert.ok(out.includes("برچسب‌های وردپرس"))
  assert.ok(out.includes("از خود سایت خوانده می‌شوند"), "ids come from the site")
})

test("the terms already chosen are shown by their own names", () => {
  const out = siteOptions({ category_ids: [7], tag_ids: [9] })
  assert.ok(out.includes("اخبار"), "the category's name, not its number")
  assert.ok(out.includes("فوری"), "and the tag's")
})

test("a term id that is not on the site is kept and named, never dropped", () => {
  const out = siteOptions({ category_ids: [7, 404], tag_ids: [9] })
  assert.ok(out.includes("404"), "the id the site did not report is still there")
  assert.ok(out.includes("پیدا نشدند و بدون تغییر نگه داشته می‌شوند"))
})

test("the WordPress settings travel in the destination's own options", () => {
  const rows = bodyOf({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [
      {
        ...wordpressDestination(4),
        options: {
          category_ids: [7],
          tag_ids: [9],
          thumbnail: "omit",
        },
      },
    ],
  }).destinations
  assert.deepEqual(rows[0].options, {
    category_ids: [7],
    tag_ids: [9],
    thumbnail: "omit",
  })
})

test("a Telegram destination carries no WordPress setting", () => {
  const rows = bodyOf({
    ...emptyWorkflowDraft(),
    managedChannelID: 5,
    destinations: [{ ...telegramDestination("official", -100999), options: {} }],
  }).destinations
  const options = rows[0].options as Record<string, unknown>
  assert.ok(!("category_ids" in options))
  assert.ok(!("tag_ids" in options))
  assert.ok(!("thumbnail" in options))
})

test("the site's terms that cannot be read are reported, not invented", () => {
  const out = render(
    {
      ...emptyWorkflowDraft(),
      managedChannelID: 5,
      destinations: [wordpressDestination(4)],
    },
    {
      siteTerms: new Map([
        [4, { categories: [], tags: [], loading: false, error: "سایت در دسترس نیست." }],
      ]),
    }
  )
  assert.ok(out.includes("سایت در دسترس نیست."))
  assert.ok(out.includes("دسته‌بندی‌های وردپرس"), "the pickers are still offered")
  assert.ok(out.includes("برچسب‌های وردپرس"))
})

test("the thumbnail policy is the publisher's own two values", () => {
  assert.equal(DEFAULT_THUMBNAIL, "source", "the publisher keeps the source image")
  assert.equal(thumbnailOf({}), "source")
  assert.equal(thumbnailOf({ thumbnail: "omit" }), "omit")
  assert.equal(thumbnailOf({ thumbnail: "nonsense" }), "source", "never guessed")
  assert.deepEqual(Object.keys(thumbnailPolicyLabels), ["source", "omit"])
})

test("one site's settings are its own and are not written to another", () => {
  const changed: Record<string, unknown>[] = []
  const wrapper = renderToStaticMarkup(
    <MemoryRouter initialEntries={["/automation"]}>
      <WordPressDestinationOptions
        site={{ id: 4, label: "خبرگزاری", detail: "news.example.com" }}
        terms={terms}
        options={{ category_ids: [7], thumbnail: "source" }}
        onOptionsChange={(next) => changed.push(next)}
      />
    </MemoryRouter>
  )
  assert.ok(wrapper.includes("خبرگزاری"))
  assert.deepEqual(changed, [], "nothing is written without an interaction")
})

// --- the list --------------------------------------------------------------

const listedWorkflow = readWorkflow({
  id: 12,
  managed_channel_id: 5,
  channel: {
    id: 5,
    title: "کانال خبری",
    username: "news",
    client_key: "official",
    telegram_channel_id: -100123,
  },
  enabled: true,
  ai_enabled: true,
  ai_prompt: "",
  ai_send_images: false,
  ai_publish_on_failure: false,
  config_revision: 2,
  destinations: [
    {
      id: 1,
      destination_type: "telegram",
      client_key: "official",
      channel_id: -100999,
      site_id: 0,
      options: {},
      position: 0,
      enabled: true,
    },
  ],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  last_run: {
    id: 71,
    status: "published",
    created_at: "2026-02-01T10:00:00Z",
    completed_at: "2026-02-01T10:00:09Z",
  },
})

test("the list shows source channel, status, AI, destinations, last run and its result", () => {
  const out = html(
    <WorkflowsTable
      workflows={[listedWorkflow]}
      channelTitles={new Map()}
      canManage
      onDelete={() => {}}
      onToggle={() => {}}
    />
  )
  for (const head of [
    "کانال مبدأ",
    "وضعیت",
    "هوش مصنوعی",
    "مقاصد",
    "آخرین اجرا",
    "نتیجه",
  ]) {
    assert.ok(out.includes(head), head)
  }
  assert.ok(out.includes("کانال خبری"), "the channel is named, not numbered")
  assert.ok(out.includes("فعال"))
  assert.ok(out.includes("روشن"), "AI is reported as on or off")
  assert.ok(out.includes("تلگرام"), "and the destinations are named")
  assert.ok(out.includes("منتشر شد"), "and the last result")
})

test("the Actions column is pinned to the physical left and nothing else is", () => {
  const out = html(
    <WorkflowsTable
      workflows={[listedWorkflow]}
      channelTitles={new Map()}
      canManage
      onDelete={() => {}}
      onToggle={() => {}}
    />
  )
  assert.ok(out.includes("sticky left-0 z-20"), "the head pins with its layer")
  assert.ok(out.includes("sticky left-0 z-10"), "so do the cells")
  assert.ok(out.includes("bg-background"), "opaque, so text does not show through")
  assert.ok(!out.includes("sticky right-0"))
})

test("a workflow that is off and has no run says so rather than showing zeros", () => {
  const off = readWorkflow({
    ...{
      id: 13,
      managed_channel_id: 8,
      enabled: false,
      ai_enabled: false,
      ai_prompt: "",
      ai_send_images: false,
      ai_publish_on_failure: false,
      config_revision: 1,
      destinations: [],
      created_at: "",
      updated_at: "",
    },
  })
  const out = html(
    <WorkflowsTable
      workflows={[off]}
      channelTitles={new Map([[8, "کانال ورزشی"]])}
      canManage={false}
      onDelete={() => {}}
      onToggle={() => {}}
    />
  )
  assert.ok(out.includes("کانال ورزشی"))
  assert.ok(out.includes("غیرفعال"))
  assert.ok(out.includes("خاموش"))
})

// --- run history -----------------------------------------------------------

const runRecord = (over: Record<string, unknown> = {}) =>
  readRun({
    id: 71,
    workflow_id: 12,
    event_id: "evt-1",
    status: "published",
    source_message_id: 4242,
    original_content: "متن اصلی پیام",
    polished_content: "متن ویرایش‌شدهٔ پیام",
    ai_status: "succeeded",
    ai_fallback_used: false,
    ai_model: "model-x",
    ai_error_code: "",
    ai_error_message: "",
    ai_input_tokens: 120,
    ai_output_tokens: 80,
    ai_duration_ms: 900,
    config_revision: 3,
    attempt_count: 2,
    last_error_code: "",
    last_error_message: "",
    created_at: "2026-02-01T10:00:00Z",
    started_at: "2026-02-01T10:00:01Z",
    completed_at: "2026-02-01T10:00:09Z",
    publications: [
      {
        id: 1,
        destination_type: "telegram",
        client_key: "official",
        channel_id: -100999,
        site_id: 0,
        status: "published",
        remote_message_id: 7788,
        remote_post_id: 0,
        remote_url: "",
        attempt_count: 1,
        last_error_code: "",
        last_error_message: "",
        completed_at: "2026-02-01T10:00:08Z",
      },
      {
        id: 2,
        destination_type: "wordpress",
        client_key: "",
        channel_id: 0,
        site_id: 4,
        status: "failed",
        remote_message_id: 0,
        remote_post_id: 0,
        remote_url: "",
        attempt_count: 3,
        last_error_code: "publish_failed",
        last_error_message: "the site refused the post",
        completed_at: "",
      },
    ],
    ...over,
  })

test("the history lists the real run: source message, time, status, AI and fallback", () => {
  const out = html(<RunsTable runs={[runRecord()]} workflowID={12} />)
  for (const head of [
    "پیام مبدأ",
    "زمان دریافت",
    "وضعیت",
    "نتیجه هوش مصنوعی",
    "بازگشت به محتوای اصلی",
    "مقاصد",
    "مدت",
  ]) {
    assert.ok(out.includes(head), head)
  }
  assert.ok(out.includes("4242"), "the source message id")
  assert.ok(out.includes("موفق"), "the AI result")
  assert.ok(out.includes("استفاده نشد"), "and whether the fallback was used")
})

test("every destination carries its own result and its own remote id", () => {
  const out = html(<RunsTable runs={[runRecord()]} workflowID={12} />)
  assert.ok(out.includes("تلگرام"))
  assert.ok(out.includes("وردپرس"))
  assert.ok(out.includes("#7788"), "the Telegram message id the peer confirmed")
  assert.ok(out.includes("منتشر شد"))
  assert.ok(out.includes("ناموفق"), "and the destination that failed")
})

test("retry information and the last refusal travel with the run", () => {
  const out = html(
    <RunsTable
      runs={[
        runRecord({
          status: "failed",
          last_error_code: "publish_failed",
          last_error_message: "the site refused the post",
        }),
      ]}
      workflowID={12}
    />
  )
  assert.ok(out.includes(`تلاش ${faDigits(2)}`))
  assert.ok(out.includes("publish_failed"))
  assert.ok(out.includes("the site refused the post"))
})

test("the detail keeps the original and the polished text apart", () => {
  const out = html(
    <RunDetail run={runRecord()} workflowID={12} workflowLabel="کانال خبری" />
  )
  assert.ok(out.includes("متن اصلی"))
  assert.ok(out.includes("متن ویرایش‌شده"))
  assert.ok(out.includes("متن اصلی پیام"))
  assert.ok(out.includes("متن ویرایش‌شدهٔ پیام"))
})

test("the detail reports the AI error and the token usage", () => {
  const out = html(
    <RunDetail
      run={runRecord({
        ai_status: "failed",
        ai_error_code: "ai_unavailable",
        ai_error_message: "the model did not answer",
      })}
      workflowID={12}
      workflowLabel="کانال خبری"
    />
  )
  assert.ok(out.includes("ai_unavailable"))
  assert.ok(out.includes("the model did not answer"))
  assert.ok(out.includes("توکن ورودی"))
  assert.ok(out.includes("توکن خروجی"))
  assert.ok(out.includes(faDigits(120)))
  assert.ok(out.includes(faDigits(80)))
})

test("the detail names each destination's remote ids and its attempts", () => {
  const out = html(
    <RunDetail run={runRecord()} workflowID={12} workflowLabel="کانال خبری" />
  )
  assert.ok(out.includes("شناسه پیام دور"))
  assert.ok(out.includes("شناسه نوشته دور"))
  assert.ok(out.includes("7788"))
  assert.ok(out.includes("تعداد تلاش"))
})

test("a partially published run is never rendered as a success", () => {
  const published = html(<RunStatusBadge status="published" />)
  assert.ok(published.includes("bg-emerald-500/10"), "a published run reads green")
  assert.ok(published.includes("منتشر شد"))

  const partial = html(<RunStatusBadge status="partially_published" />)
  assert.ok(!partial.includes("bg-emerald-500/10"), "this one never does")
  assert.ok(partial.includes("bg-amber-500/10"), "it carries the warning tone")
  assert.ok(partial.includes("بخشی منتشر شد"), "and the word that says so")

  const detail = html(
    <RunDetail
      run={runRecord({ status: "partially_published" })}
      workflowID={12}
      workflowLabel="کانال خبری"
    />
  )
  assert.ok(detail.includes("در همهٔ مقاصد منتشر نشد"), "and the page says it too")
  assert.ok(detail.includes("موفق کامل نیست"))
})

test("a partially published row of the history carries the same warning tone", () => {
  const out = html(
    <RunsTable
      runs={[runRecord({ status: "partially_published" })]}
      workflowID={12}
    />
  )
  assert.ok(out.includes("بخشی منتشر شد"))
  assert.ok(
    !html(<RunStatusBadge status="partially_published" />).includes(
      "bg-emerald-500/10"
    )
  )
})

// --- the real conversation: save, enable, reload ----------------------------

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

// Exactly what the gateway renders back for one workflow.
const storedWorkflow = (over: Record<string, unknown> = {}) => ({
  id: 12,
  managed_channel_id: 5,
  channel: {
    id: 5,
    title: "کانال خبری",
    username: "news",
    client_key: "official",
    telegram_channel_id: -100123,
  },
  enabled: false,
  ai_enabled: true,
  ai_prompt: "لحن رسمی",
  ai_send_images: true,
  ai_publish_on_failure: false,
  config_revision: 1,
  destinations: [
    {
      id: 1,
      destination_type: "telegram",
      client_key: "official",
      channel_id: -100999,
      site_id: 0,
      options: {},
      position: 0,
      enabled: true,
    },
    {
      id: 2,
      destination_type: "wordpress",
      client_key: "",
      channel_id: 0,
      site_id: 4,
      options: { category_ids: [7], tag_ids: [9], thumbnail: "omit" },
      position: 1,
      enabled: true,
    },
  ],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  ...over,
})

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
      clientKey: "official",
      channelID: -100999,
      siteID: null,
      options: {},
      enabled: true,
    },
    {
      type: "wordpress",
      clientKey: "",
      channelID: null,
      siteID: 4,
      options: { category_ids: [7], tag_ids: [9], thumbnail: "omit" },
      enabled: true,
    },
  ]),
}

test("saving a new workflow posts it and lands on the stored record", async () => {
  const calls = stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.method === "POST") {
      return { status: 201, payload: { data: { workflow: storedWorkflow() }, meta: {} } }
    }
    return refuse(404, "not_found", "no such route " + call.path)
  })
  const result = (await workflowAction({
    params: { workflowId: "new" },
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as Response

  const post = calls.find((call) => call.method === "POST")
  assert.ok(post, "the create goes to the API")
  assert.equal(post?.path, "/api/v1/local/automation/workflows")
  const sent = post?.body as Record<string, unknown>
  assert.equal(sent.managed_channel_id, 5)
  assert.equal(sent.enabled, false)
  assert.equal(sent.ai_enabled, true)
  assert.equal(sent.ai_prompt, "لحن رسمی")
  assert.equal(sent.ai_send_images, true)
  assert.equal(sent.ai_publish_on_failure, false)
  // The write lands on the record's own URL, whose loader reads it back.
  assert.equal(result.status, 302)
  assert.equal(
    result.headers.get("Location"),
    "/automation/workflows/12",
    "the saved record's own URL, not the list"
  )
})

// Everything the editor's loader reads, so a "reload" is the same conversation
// the browser has: the record on its own URL, the channels, the workflow list,
// the connections and the registered sites.
function answerEditorAPI(call: Call, workflow: Record<string, unknown>) {
  if (call.path === "/api/v1/auth/me") return ok(me)
  if (call.path === "/api/v1/local/channels") {
    return ok({
      channels: [{ id: 5, title: "کانال خبری", client_key: "official" }],
    })
  }
  if (call.path === "/api/v1/local/automation/workflows/12") {
    return ok({ workflow })
  }
  if (call.path === "/api/v1/local/automation/workflows") {
    return ok({ workflows: [workflow] }, { total: 1, has_more: false })
  }
  if (call.path === "/api/v1/telegram") return ok({ servers: [] })
  if (call.path === "/api/v1/local/wordpress") {
    return ok({ sites: [{ id: 4, title: "خبرگزاری", domain: "news.example.com" }] })
  }
  return refuse(404, "not_found", "no such route " + call.path)
}

test("the saved workflow and its settings come back on a reload", async () => {
  stubApi((call) => answerEditorAPI(call, storedWorkflow()))
  const data = (await workflowEditorLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12"),
  } as never)) as { status: string; workflow?: ReturnType<typeof readWorkflow> }

  assert.equal(data.status, "ok")
  assert.ok(data.workflow)
  assert.equal(data.workflow?.managedChannelID, 5)
  assert.equal(data.workflow?.aiEnabled, true)
  assert.equal(data.workflow?.aiPrompt, "لحن رسمی")
  assert.equal(data.workflow?.aiSendImages, true)
  assert.equal(data.workflow?.aiPublishOnFailure, false)
  assert.equal(data.workflow?.enabled, false)
  assert.equal(data.workflow?.destinations.length, 2)
  // And the form is re-seeded from that answer rather than from what was typed.
  const draft = workflowDraftOf(data.workflow as never)
  assert.equal(draft.destinations[1].siteID, 4)
  assert.deepEqual(draft.destinations[1].options, {
    category_ids: [7],
    tag_ids: [9],
    thumbnail: "omit",
  })
})

test("enabling is saved and is still on after a reload", async () => {
  let patched: unknown = null
  stubApi((call) => {
    if (call.method === "PATCH") {
      patched = call.body
    }
    return answerEditorAPI(call, storedWorkflow({ enabled: true }))
  })
  const result = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)) as Response
  assert.equal(result.status, 302)
  assert.equal(result.headers.get("Location"), "/automation/workflows/12")
  assert.equal((patched as Record<string, unknown>).enabled, true)

  const data = (await workflowEditorLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12"),
  } as never)) as { workflow?: { enabled?: boolean } }
  assert.equal(data.workflow?.enabled, true, "the reload shows the saved state")
})

test("an enable the backend refuses is shown with its reason and looks off", async () => {
  const message =
    "a workflow needs at least one enabled destination before it can be enabled"
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(400, "invalid_input", message)
  })
  const result = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)) as { error?: string; revertEnabled?: boolean }
  assert.equal(result.error, message, "the server's own sentence, kept as written")
  assert.equal(result.revertEnabled, true, "and the switch is put back")

  // The reason is what the form shows; the switch never keeps looking active.
  const out = render(
    { ...emptyWorkflowDraft(), enabled: false, managedChannelID: 5 },
    { error: message }
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
    request: submitTo("/automation/workflows/12", {
      ...newWorkflowFields,
      intent: "update",
      enabled: "true",
    }),
  } as never)) as { error?: string; revertEnabled?: boolean }
  assert.equal(result.error, message)
  assert.equal(result.revertEnabled, true)
})

test("a refused write is never reported as a success", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(409, "conflict", "a workflow already exists for that source channel")
  })
  const result = (await workflowAction({
    params: { workflowId: "new" },
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as Response | { error?: string }
  assert.ok(!(result instanceof Response), "no redirect, so no success is claimed")
  assert.ok((result as { error?: string }).error)
})

test("a 409 names the workflow that holds the channel and links to it", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.method === "POST") {
      return refuse(409, "conflict", "a workflow already exists for that source channel")
    }
    return ok({ workflows: [storedWorkflow()] }, { total: 1 })
  })
  const result = (await workflowAction({
    params: { workflowId: "new" },
    request: submitTo("/automation/workflows/new", newWorkflowFields),
  } as never)) as {
    error?: string
    conflictWorkflowID?: number | null
    conflictChannelLabel?: string
  }
  assert.equal(result.conflictWorkflowID, 12)
  assert.equal(result.conflictChannelLabel, "کانال خبری")

  const out = render(emptyWorkflowDraft(), {
    error: result.error,
    conflict: {
      id: result.conflictWorkflowID as number,
      channelLabel: result.conflictChannelLabel as string,
    },
  })
  assert.match(out, /href="\/automation\/workflows\/12"/)
  assert.ok(out.includes("ویرایش ورک‌فلوی «کانال خبری»"))
})
