// The automation area's one view of the API.
//
// Every wire field name in this release lives in this file and nowhere else.
// Routes and components read the camelCase models below and post through the
// payload builders, so a contract rename is one edit here rather than a hunt
// through eight screens. Nothing else in `app/` may name a snake_case field of
// /local/automation.
//
// Two rules are encoded here rather than left to the screens:
//   - A secret is write-only. The API answers `has_secret`, never the value, and
//     no model carries a secret back into the browser.
//   - Delivery headers and bodies arrive already redacted and already bounded
//     by the server. This layer renders them; it never rewrites, expands or
//     "un-redacts" them.

import type { ApiError } from "@/lib/api"

// ---------------------------------------------------------------------------
// Panel routes. A path handed to `redirect()` must go through `strip()`; these
// are router paths, never absolute URLs.
// ---------------------------------------------------------------------------

export const automationRoute = {
  root: "/automation",
  webhooks: "/automation/webhooks",
  // Creating and editing are one form component on two routes: `new` is the
  // create page and `:id/edit` is the same form seeded from the stored row. A
  // bare `:id` is the old address of that editor and answers by redirecting to
  // it, so a bookmark lands where it always meant to.
  newWebhook: "/automation/webhooks/new",
  editWebhook: (webhookID: number) => `/automation/webhooks/${webhookID}/edit`,
  deliveries: (webhookID: number) => `/automation/webhooks/${webhookID}/deliveries`,
  delivery: (webhookID: number, deliveryID: number) =>
    `/automation/webhooks/${webhookID}/deliveries/${deliveryID}`,
  workflows: "/automation/workflows",
  newWorkflow: "/automation/workflows/new",
  editWorkflow: (workflowID: number) => `/automation/workflows/${workflowID}/edit`,
  workflow: (workflowID: number) => `/automation/workflows/${workflowID}`,
  runs: (workflowID: number) => `/automation/workflows/${workflowID}/runs`,
  run: (workflowID: number, runID: number) =>
    `/automation/workflows/${workflowID}/runs/${runID}`,
} as const

// Where a form goes back to when it is done. A link that came from the listing
// carries the view it was on — the tab, the query, the page — so a save or a
// cancel lands back in that exact view; a bookmark, a paste or a refresh has no
// such history and falls back to the collection's own tab. The listing is the
// only caller that builds `from`, and every form reads it through here, so the
// two halves cannot drift apart.
export function automationReturnTo(
  state: unknown,
  tab: "webhooks" | "workflows"
): string {
  const from = (state as { from?: unknown } | null | undefined)?.from
  return typeof from === "string" && from.startsWith(automationRoute.root)
    ? from
    : `${automationRoute.root}?tab=${tab}`
}

// The `state` half of the pair above, for a link out of the listing.
export function automationFormLinkState(from: string): { from: string } {
  return { from }
}

// ---------------------------------------------------------------------------
// API paths, relative to API_BASE.
// ---------------------------------------------------------------------------

const ROOT = "/local/automation"

export const automationApi = {
  status: `${ROOT}/status`,
  webhooks: `${ROOT}/webhooks`,
  webhook: (id: number) => `${ROOT}/webhooks/${id}`,
  deliveries: (webhookID: number) => `${ROOT}/webhooks/${webhookID}/deliveries`,
  delivery: (webhookID: number, deliveryID: number) =>
    `${ROOT}/webhooks/${webhookID}/deliveries/${deliveryID}`,
  retryDelivery: (webhookID: number, deliveryID: number) =>
    `${ROOT}/webhooks/${webhookID}/deliveries/${deliveryID}/retry`,
  testWebhook: (id: number) => `${ROOT}/webhooks/${id}/test`,
  workflows: `${ROOT}/workflows`,
  workflow: (id: number) => `${ROOT}/workflows/${id}`,
  runs: (workflowID: number) => `${ROOT}/workflows/${workflowID}/runs`,
  run: (workflowID: number, runID: number) =>
    `${ROOT}/workflows/${workflowID}/runs/${runID}`,
} as const

// ---------------------------------------------------------------------------
// Wire shapes. These mirror the gateway's JSON views exactly.
// ---------------------------------------------------------------------------

export type HttpMethod = "POST" | "PUT" | "PATCH"
export type AuthMode = "hmac" | "bearer" | "header" | "none"
export type DeliveryStatus =
  | "pending"
  | "delivering"
  | "succeeded"
  | "failed"
  | "cancelled"
export type RunStatus =
  | "queued"
  | "processing"
  | "published"
  | "published_with_fallback"
  | "partially_published"
  | "failed"
  | "cancelled"
export type AIStatus = "not_used" | "succeeded" | "failed" | "fallback_used"
export type PublicationStatus = "pending" | "published" | "failed" | "unknown"
export type DestinationType = "telegram" | "wordpress"
export type JobType = "webhook_delivery" | "workflow_run"

interface WireLastDelivery {
  status: string
  http_status: number
  at: string
  attempt_count: number
}

interface WireSummary {
  succeeded: number
  failed: number
  pending: number
}

interface WireWebhookView {
  id: number
  name: string
  description: string
  enabled: boolean
  url: string
  method: string
  auth_mode: string
  auth_header: string
  has_secret: boolean
  timeout_ms: number
  max_attempts: number
  allow_localhost: boolean
  source_channel_ids: number[]
  created_at: string
  updated_at: string
  last_delivery?: WireLastDelivery
  summary?: WireSummary
}

interface WireRequestRecord {
  method: string
  url: string
  headers: Record<string, string>
  body: string
  body_truncated: boolean
}

interface WireAttempt {
  id: number
  attempt_number: number
  request_method: string
  request_url: string
  request_headers: Record<string, string>
  request_body: string
  request_body_truncated: boolean
  response_status: number
  response_headers: Record<string, string>
  response_body: string
  response_body_truncated: boolean
  response_bytes: number
  duration_ms: number
  error_code: string
  error_message: string
  started_at: string
  finished_at: string | null
}

interface WireDeliveryView {
  id: number
  webhook_id: number
  event_id: string
  status: string
  attempt_count: number
  next_attempt_at: string | null
  last_http_status: number
  last_error: string
  created_at: string
  updated_at: string
  completed_at: string | null
  request?: WireRequestRecord
  attempts?: WireAttempt[]
}

interface WireDestination {
  id: number
  destination_type: string
  client_key: string
  channel_id: number
  site_id: number
  options: Record<string, unknown>
  position: number
  enabled: boolean
}

// The gateway's sourceChannelView. `telegram_channel_id` is the Telegram peer
// id and `id` the local managed_channels.id: the two answer different
// questions and are never interchangeable, so both are read and named apart.
interface WireChannelRef {
  id: number
  title: string
  username: string
  client_key: string
  telegram_channel_id: number
}

interface WireRunSummary {
  id: number
  status: string
  created_at: string
  completed_at: string | null
}

interface WireWorkflowView {
  id: number
  managed_channel_id: number
  channel?: WireChannelRef
  enabled: boolean
  ai_enabled: boolean
  ai_prompt: string
  ai_send_images: boolean
  ai_publish_on_failure: boolean
  config_revision: number
  destinations: WireDestination[]
  created_at: string
  updated_at: string
  last_run?: WireRunSummary
}

interface WirePublication {
  id: number
  destination_type: string
  client_key: string
  channel_id: number
  site_id: number
  status: string
  remote_message_id: number
  remote_post_id: number
  remote_url: string
  attempt_count: number
  last_error_code: string
  last_error_message: string
  completed_at: string | null
}

interface WireRunView {
  id: number
  workflow_id: number
  event_id: string
  status: string
  source_message_id: number
  original_content: string
  polished_content: string
  ai_status: string
  ai_fallback_used: boolean
  ai_model: string
  ai_error_code: string
  ai_error_message: string
  ai_input_tokens: number
  ai_output_tokens: number
  ai_duration_ms: number
  config_revision: number
  attempt_count: number
  last_error_code: string
  last_error_message: string
  created_at: string
  started_at: string | null
  completed_at: string | null
  publications: WirePublication[]
}

interface WireStatusView {
  enabled: boolean
  workers: number
  pending_jobs: number
  retry_backlog: number
  processing: number
  failed_recent: number
  recent_failures: {
    job_type: string
    target_id: number
    error_code: string
    error_message: string
    at: string
  }[]
}

// ---------------------------------------------------------------------------
// Presentation models. What the screens read.
//
// Identifier rule, normative for this whole subsystem — the UI shows these
// numbers, so a mix-up is a visible lie:
//   - `managedChannelID` is always a local managed_channels.id. It is the
//     workflow's source channel and a webhook's subscription list.
//   - `channelID` is always a Telegram peer id, meaningful only together with
//     the `clientKey` beside it.
//   - `clientKey` is always the connection key.
//   - `siteID` is always a registered WordPress site's id.
// A title is never inferred from a numeric id: the screens resolve a local id
// through the channel rows they already loaded.
// ---------------------------------------------------------------------------

export interface DeliverySummary {
  status: DeliveryStatus
  httpStatus: number
  at: string
  attemptCount: number
}

export interface DeliveryCounts {
  succeeded: number
  failed: number
  pending: number
}

export interface WebhookItem {
  id: number
  name: string
  description: string
  enabled: boolean
  url: string
  method: HttpMethod
  authMode: AuthMode
  authHeader: string
  // hasSecret only. The value is never in the browser and never in history.
  hasSecret: boolean
  timeoutMs: number
  maxAttempts: number
  // allowLocalhost is this webhook's half of the loopback exception. It only
  // narrows the deployment's own permission: with
  // TELESYNC_WEBHOOK_ALLOW_LOCALHOST off it does nothing at all.
  allowLocalhost: boolean
  // Local managed_channels.id values this webhook subscribes to. Never
  // Telegram ids: a title comes from the channel rows, never from here.
  sourceChannelIds: number[]
  createdAt: string
  updatedAt: string
  lastDelivery: DeliverySummary | null
  summary: DeliveryCounts | null
}

// One persisted request or attempt body, already redacted and already bounded
// by the server. `truncated` is shown, never worked around.
export interface RecordedBody {
  headers: Record<string, string>
  body: string
  truncated: boolean
}

export interface DeliveryAttempt {
  id: number
  attemptNumber: number
  requestMethod: string
  requestUrl: string
  request: RecordedBody
  responseStatus: number
  response: RecordedBody
  responseBytes: number
  durationMs: number
  errorCode: string
  errorMessage: string
  startedAt: string
  finishedAt: string | null
}

// The delivery's own request record carries its method and address as well as
// the headers and body the history keeps.
export interface DeliveryRequest extends RecordedBody {
  method: string
  url: string
}

export interface DeliveryItem {
  id: number
  webhookID: number
  eventID: string
  status: DeliveryStatus
  attemptCount: number
  nextAttemptAt: string | null
  lastHttpStatus: number
  lastError: string
  createdAt: string
  updatedAt: string
  completedAt: string | null
  request: DeliveryRequest | null
  attempts: DeliveryAttempt[]
}

export interface WorkflowDestinationItem {
  id: number
  type: DestinationType
  clientKey: string
  // A Telegram peer id, meaningful only together with `clientKey`.
  channelID: number
  siteID: number
  options: Record<string, unknown>
  position: number
  enabled: boolean
}

export interface WorkflowChannelRef {
  // The local managed_channels.id: what permissions are decided on.
  id: number
  title: string
  username: string
  clientKey: string
  // The Telegram peer id. Meaningful only together with `clientKey`.
  channelID: number
}

export interface WorkflowItem {
  id: number
  // The source channel, as a local managed_channels.id. Its title is resolved
  // from the channel rows, or from `channel` when the API filled that in.
  managedChannelID: number
  channel: WorkflowChannelRef | null
  enabled: boolean
  aiEnabled: boolean
  aiPrompt: string
  aiSendImages: boolean
  aiPublishOnFailure: boolean
  configRevision: number
  destinations: WorkflowDestinationItem[]
  createdAt: string
  updatedAt: string
  lastRun: {
    id: number
    status: RunStatus
    createdAt: string
    completedAt: string | null
  } | null
}

export interface RunPublication {
  id: number
  type: DestinationType
  clientKey: string
  channelID: number
  siteID: number
  status: PublicationStatus
  remoteMessageID: number
  remotePostID: number
  remoteURL: string
  attemptCount: number
  lastErrorCode: string
  lastErrorMessage: string
  completedAt: string | null
}

export interface RunItem {
  id: number
  workflowID: number
  eventID: string
  status: RunStatus
  sourceMessageID: number
  originalContent: string
  polishedContent: string
  aiStatus: AIStatus
  aiFallbackUsed: boolean
  aiModel: string
  aiErrorCode: string
  aiErrorMessage: string
  aiInputTokens: number
  aiOutputTokens: number
  aiDurationMs: number
  // The workflow configuration revision this run was prepared from. A queued
  // run keeps the configuration it was queued with, so editing the workflow
  // afterwards changes future runs and not this one.
  configRevision: number
  attemptCount: number
  lastErrorCode: string
  lastErrorMessage: string
  createdAt: string
  startedAt: string | null
  completedAt: string | null
  publications: RunPublication[]
}

export interface AutomationFailureItem {
  jobType: JobType
  targetID: number
  errorCode: string
  errorMessage: string
  at: string
}

export interface AutomationStatusItem {
  enabled: boolean
  workers: number
  pendingJobs: number
  retryBacklog: number
  processing: number
  failedRecent: number
  recentFailures: AutomationFailureItem[]
}

// ---------------------------------------------------------------------------
// Envelopes. Every automation route answers one named record, or a named page
// of them: {data:{webhook}} and {data:{webhooks:[…]}}. The keys live here and
// nowhere else, because reading `.data` where `.data.webhook` was meant is the
// kind of mismatch that renders an empty form out of a perfectly good record.
// ---------------------------------------------------------------------------

function recordOf(data: unknown, key: string): unknown {
  const envelope = (data ?? {}) as Record<string, unknown>
  return envelope[key]
}

function recordsOf(data: unknown, key: string): unknown[] {
  const rows = recordOf(data, key)
  return Array.isArray(rows) ? rows : []
}

export const readWebhookRecord = (data: unknown): WebhookItem =>
  readWebhook(recordOf(data, "webhook"))
export const readDeliveryRecord = (data: unknown): DeliveryItem =>
  readDelivery(recordOf(data, "delivery"))
export const readWorkflowRecord = (data: unknown): WorkflowItem =>
  readWorkflow(recordOf(data, "workflow"))
export const readRunRecord = (data: unknown): RunItem =>
  readRun(recordOf(data, "run"))

export const webhookRows = (data: unknown): unknown[] =>
  recordsOf(data, "webhooks")
export const deliveryRows = (data: unknown): unknown[] =>
  recordsOf(data, "deliveries")
export const workflowRows = (data: unknown): unknown[] =>
  recordsOf(data, "workflows")
export const runRows = (data: unknown): unknown[] => recordsOf(data, "runs")

// ---------------------------------------------------------------------------
// Readers. A response that omits a field is read as "not reported", never
// invented: an empty list, a null summary, an empty string.
// ---------------------------------------------------------------------------

const str = (value: unknown): string => (typeof value === "string" ? value : "")
const num = (value: unknown): number =>
  typeof value === "number" && Number.isFinite(value) ? value : 0
const bool = (value: unknown): boolean => value === true

function ids(value: unknown): number[] {
  return Array.isArray(value) ? value.filter((v): v is number => Number.isInteger(v)) : []
}

function headers(value: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  if (value && typeof value === "object") {
    for (const [key, raw] of Object.entries(value as Record<string, unknown>)) {
      out[key] = str(raw)
    }
  }
  return out
}

function recordedBody(
  source: { headers?: unknown; body?: unknown; body_truncated?: unknown } | undefined
): RecordedBody {
  return {
    headers: headers(source?.headers),
    body: str(source?.body),
    truncated: bool(source?.body_truncated),
  }
}

export function readWebhook(raw: unknown): WebhookItem {
  const wire = (raw ?? {}) as Partial<WireWebhookView>
  const last = wire.last_delivery as Partial<WireLastDelivery> | undefined
  const summary = wire.summary as Partial<WireSummary> | undefined
  return {
    id: num(wire.id),
    name: str(wire.name),
    description: str(wire.description),
    enabled: bool(wire.enabled),
    url: str(wire.url),
    method: readMethod(wire.method),
    authMode: readAuthMode(wire.auth_mode),
    authHeader: str(wire.auth_header),
    hasSecret: bool(wire.has_secret),
    timeoutMs: num(wire.timeout_ms),
    maxAttempts: num(wire.max_attempts),
    allowLocalhost: bool(wire.allow_localhost),
    sourceChannelIds: ids(wire.source_channel_ids),
    createdAt: str(wire.created_at),
    updatedAt: str(wire.updated_at),
    lastDelivery: last
      ? {
          status: readDeliveryStatus(last.status),
          httpStatus: num(last.http_status),
          at: str(last.at),
          attemptCount: num(last.attempt_count),
        }
      : null,
    summary: summary
      ? {
          succeeded: num(summary.succeeded),
          failed: num(summary.failed),
          pending: num(summary.pending),
        }
      : null,
  }
}

export function readDelivery(raw: unknown): DeliveryItem {
  const wire = (raw ?? {}) as Partial<WireDeliveryView>
  const request = wire.request as Partial<WireRequestRecord> | undefined
  const attempts = Array.isArray(wire.attempts) ? wire.attempts : []
  return {
    id: num(wire.id),
    webhookID: num(wire.webhook_id),
    eventID: str(wire.event_id),
    status: readDeliveryStatus(wire.status),
    attemptCount: num(wire.attempt_count),
    nextAttemptAt: str(wire.next_attempt_at) || null,
    lastHttpStatus: num(wire.last_http_status),
    lastError: str(wire.last_error),
    createdAt: str(wire.created_at),
    updatedAt: str(wire.updated_at),
    completedAt: str(wire.completed_at) || null,
    request: request
      ? {
          ...recordedBody(request),
          method: str(request.method),
          url: str(request.url),
        }
      : null,
    attempts: attempts.map((rawAttempt): DeliveryAttempt => {
      const attempt = (rawAttempt ?? {}) as Partial<WireAttempt>
      return {
        id: num(attempt.id),
        attemptNumber: num(attempt.attempt_number),
        requestMethod: str(attempt.request_method),
        requestUrl: str(attempt.request_url),
        request: recordedBody({
          headers: attempt.request_headers,
          body: attempt.request_body,
          body_truncated: attempt.request_body_truncated,
        }),
        responseStatus: num(attempt.response_status),
        response: recordedBody({
          headers: attempt.response_headers,
          body: attempt.response_body,
          body_truncated: attempt.response_body_truncated,
        }),
        responseBytes: num(attempt.response_bytes),
        durationMs: num(attempt.duration_ms),
        errorCode: str(attempt.error_code),
        errorMessage: str(attempt.error_message),
        startedAt: str(attempt.started_at),
        finishedAt: str(attempt.finished_at) || null,
      }
    }),
  }
}

// readChannelRef reads the gateway's sourceChannelView. `telegram_channel_id`
// is the wire name of the peer id; an older shape used `channel_id` there and
// the fallback keeps a stored response readable without inventing a number the
// server did not say. A channel the server did not report is null, never a
// zero-valued row.
export function readChannelRef(
  raw: unknown
): WorkflowChannelRef | null {
  const channel = (raw ?? {}) as Partial<WireChannelRef> & { channel_id?: unknown }
  if (!raw || typeof raw !== "object") return null
  const id = num(channel.id)
  if (id <= 0) return null
  return {
    id,
    title: str(channel.title),
    username: str(channel.username),
    clientKey: str(channel.client_key),
    channelID: num(channel.telegram_channel_id ?? channel.channel_id),
  }
}

export function readWorkflow(raw: unknown): WorkflowItem {
  const wire = (raw ?? {}) as Partial<WireWorkflowView>
  const channel = wire.channel as Partial<WireChannelRef> | undefined
  const lastRun = wire.last_run as Partial<WireRunSummary> | undefined
  const destinations = Array.isArray(wire.destinations) ? wire.destinations : []
  return {
    id: num(wire.id),
    managedChannelID: num(wire.managed_channel_id),
    channel: readChannelRef(channel),
    enabled: bool(wire.enabled),
    aiEnabled: bool(wire.ai_enabled),
    aiPrompt: str(wire.ai_prompt),
    aiSendImages: bool(wire.ai_send_images),
    aiPublishOnFailure: bool(wire.ai_publish_on_failure),
    configRevision: num(wire.config_revision),
    destinations: destinations.map((rawDestination): WorkflowDestinationItem => {
      const destination = (rawDestination ?? {}) as Partial<WireDestination>
      return {
        id: num(destination.id),
        type: readDestinationType(destination.destination_type),
        clientKey: str(destination.client_key),
        channelID: num(destination.channel_id),
        siteID: num(destination.site_id),
        options:
          destination.options && typeof destination.options === "object"
            ? (destination.options as Record<string, unknown>)
            : {},
        position: num(destination.position),
        enabled: bool(destination.enabled),
      }
    }),
    createdAt: str(wire.created_at),
    updatedAt: str(wire.updated_at),
    lastRun: lastRun
      ? {
          id: num(lastRun.id),
          status: readRunStatus(lastRun.status),
          createdAt: str(lastRun.created_at),
          completedAt: str(lastRun.completed_at) || null,
        }
      : null,
  }
}

export function readRun(raw: unknown): RunItem {
  const wire = (raw ?? {}) as Partial<WireRunView>
  const publications = Array.isArray(wire.publications) ? wire.publications : []
  return {
    id: num(wire.id),
    workflowID: num(wire.workflow_id),
    eventID: str(wire.event_id),
    status: readRunStatus(wire.status),
    sourceMessageID: num(wire.source_message_id),
    originalContent: str(wire.original_content),
    polishedContent: str(wire.polished_content),
    aiStatus: readAIStatus(wire.ai_status),
    aiFallbackUsed: bool(wire.ai_fallback_used),
    aiModel: str(wire.ai_model),
    aiErrorCode: str(wire.ai_error_code),
    aiErrorMessage: str(wire.ai_error_message),
    aiInputTokens: num(wire.ai_input_tokens),
    aiOutputTokens: num(wire.ai_output_tokens),
    aiDurationMs: num(wire.ai_duration_ms),
    configRevision: num(wire.config_revision),
    attemptCount: num(wire.attempt_count),
    lastErrorCode: str(wire.last_error_code),
    lastErrorMessage: str(wire.last_error_message),
    createdAt: str(wire.created_at),
    startedAt: str(wire.started_at) || null,
    completedAt: str(wire.completed_at) || null,
    publications: publications.map((rawPublication): RunPublication => {
      const publication = (rawPublication ?? {}) as Partial<WirePublication>
      return {
        id: num(publication.id),
        type: readDestinationType(publication.destination_type),
        clientKey: str(publication.client_key),
        channelID: num(publication.channel_id),
        siteID: num(publication.site_id),
        status: readPublicationStatus(publication.status),
        remoteMessageID: num(publication.remote_message_id),
        remotePostID: num(publication.remote_post_id),
        remoteURL: str(publication.remote_url),
        attemptCount: num(publication.attempt_count),
        lastErrorCode: str(publication.last_error_code),
        lastErrorMessage: str(publication.last_error_message),
        completedAt: str(publication.completed_at) || null,
      }
    }),
  }
}

// The status route answers {data:{status:{…}}}, one envelope deeper than a
// screen reads it. Unwrapping lives here so the wire shape is known in exactly
// one place and no screen has to know how the gateway wraps a response — the
// mistake of reading the envelope as the record is what made every detail page
// render zeros once already.
export function statusRecord(data: unknown): unknown {
  const wrapped = (data ?? {}) as { status?: unknown }
  return wrapped.status ?? {}
}

export function readStatus(raw: unknown): AutomationStatusItem {
  const wire = (raw ?? {}) as Partial<WireStatusView>
  const failures = Array.isArray(wire.recent_failures) ? wire.recent_failures : []
  return {
    enabled: bool(wire.enabled),
    workers: num(wire.workers),
    pendingJobs: num(wire.pending_jobs),
    retryBacklog: num(wire.retry_backlog),
    processing: num(wire.processing),
    failedRecent: num(wire.failed_recent),
    recentFailures: failures.map((rawFailure): AutomationFailureItem => {
      const failure = (rawFailure ?? {}) as WireStatusView["recent_failures"][number]
      return {
        jobType: failure.job_type === "workflow_run" ? "workflow_run" : "webhook_delivery",
        targetID: num(failure.target_id),
        errorCode: str(failure.error_code),
        errorMessage: str(failure.error_message),
        at: str(failure.at),
      }
    }),
  }
}

// ---------------------------------------------------------------------------
// Enum readers. An unknown value is reported as itself, never guessed into a
// status the server did not say.
// ---------------------------------------------------------------------------

const HTTP_METHODS: readonly string[] = ["POST", "PUT", "PATCH"]
const AUTH_MODES: readonly string[] = ["hmac", "bearer", "header", "none"]
const DELIVERY_STATUSES: readonly string[] = [
  "pending",
  "delivering",
  "succeeded",
  "failed",
  "cancelled",
]
const RUN_STATUSES: readonly string[] = [
  "queued",
  "processing",
  "published",
  "published_with_fallback",
  "partially_published",
  "failed",
  "cancelled",
]
const AI_STATUSES: readonly string[] = [
  "not_used",
  "succeeded",
  "failed",
  "fallback_used",
]
const PUBLICATION_STATUSES: readonly string[] = [
  "pending",
  "published",
  "failed",
  "unknown",
]
const DESTINATION_TYPES: readonly string[] = ["telegram", "wordpress"]

const known = <T extends string>(
  value: unknown,
  allowed: readonly string[],
  fallback: T
): T => (allowed.includes(str(value)) ? (str(value) as T) : fallback)

export const readMethod = (value: unknown): HttpMethod =>
  known<HttpMethod>(value, HTTP_METHODS, "POST")
export const readAuthMode = (value: unknown): AuthMode =>
  known<AuthMode>(value, AUTH_MODES, "none")
export const readDeliveryStatus = (value: unknown): DeliveryStatus =>
  known<DeliveryStatus>(value, DELIVERY_STATUSES, "pending")
export const readRunStatus = (value: unknown): RunStatus =>
  known<RunStatus>(value, RUN_STATUSES, "queued")
export const readAIStatus = (value: unknown): AIStatus =>
  known<AIStatus>(value, AI_STATUSES, "not_used")
export const readPublicationStatus = (value: unknown): PublicationStatus =>
  known<PublicationStatus>(value, PUBLICATION_STATUSES, "unknown")
export const readDestinationType = (value: unknown): DestinationType =>
  known<DestinationType>(value, DESTINATION_TYPES, "telegram")

// ---------------------------------------------------------------------------
// Labels. Persian only in the presentation layer; payloads stay ASCII.
// ---------------------------------------------------------------------------

export const methodLabels: Record<HttpMethod, string> = {
  POST: "POST",
  PUT: "PUT",
  PATCH: "PATCH",
}

export const authModeLabels: Record<AuthMode, string> = {
  hmac: "امضای HMAC",
  bearer: "توکن Bearer",
  header: "هدر اختصاصی",
  none: "بدون احراز",
}

export const deliveryStatusLabels: Record<DeliveryStatus, string> = {
  pending: "در انتظار",
  delivering: "در حال ارسال",
  succeeded: "موفق",
  failed: "ناموفق",
  cancelled: "لغو شد",
}

export const runStatusLabels: Record<RunStatus, string> = {
  queued: "در صف",
  processing: "در حال پردازش",
  published: "منتشر شد",
  published_with_fallback: "منتشر شد با محتوای اصلی",
  partially_published: "بخشی منتشر شد",
  failed: "ناموفق",
  cancelled: "لغو شد",
}

export const aiStatusLabels: Record<AIStatus, string> = {
  not_used: "استفاده نشد",
  succeeded: "موفق",
  failed: "ناموفق",
  fallback_used: "بازگشت به محتوای اصلی",
}

export const publicationStatusLabels: Record<PublicationStatus, string> = {
  pending: "در انتظار",
  published: "منتشر شد",
  failed: "ناموفق",
  unknown: "نامشخص",
}

export const destinationTypeLabels: Record<DestinationType, string> = {
  telegram: "تلگرام",
  wordpress: "وردپرس",
}

export const jobTypeLabels: Record<JobType, string> = {
  webhook_delivery: "تحویل وب‌هوک",
  workflow_run: "اجرای ورک‌فلو",
}

export type Tone = "default" | "success" | "warning" | "destructive" | "muted"

const tone = (map: Record<string, Tone>, value: string): Tone => map[value] ?? "muted"

const deliveryTones: Record<string, Tone> = {
  pending: "warning",
  delivering: "default",
  succeeded: "success",
  failed: "destructive",
  cancelled: "muted",
}
const runTones: Record<string, Tone> = {
  queued: "warning",
  processing: "default",
  published: "success",
  published_with_fallback: "success",
  partially_published: "warning",
  failed: "destructive",
  cancelled: "muted",
}
const publicationTones: Record<string, Tone> = {
  pending: "warning",
  published: "success",
  failed: "destructive",
  unknown: "muted",
}
const aiTones: Record<string, Tone> = {
  not_used: "muted",
  succeeded: "success",
  failed: "destructive",
  fallback_used: "warning",
}

export const deliveryStatusTone = (status: DeliveryStatus): Tone =>
  tone(deliveryTones, status)
export const runStatusTone = (status: RunStatus): Tone => tone(runTones, status)
export const publicationStatusTone = (status: PublicationStatus): Tone =>
  tone(publicationTones, status)
export const aiStatusTone = (status: AIStatus): Tone => tone(aiTones, status)

// ---------------------------------------------------------------------------
// Behaviour rules.
// ---------------------------------------------------------------------------

// A manual retry is offered only for a terminal failure. Anything still in
// flight, still queued, already delivered or cancelled is left alone: a retry
// on those would either duplicate work or undo an operator's cancellation.
export function canRetryDelivery(delivery: Pick<DeliveryItem, "status">): boolean {
  return delivery.status === "failed"
}

// A duplicate workflow is the one refusal a user can act on by going somewhere
// else, so it is named rather than reported raw.
export function isDuplicateWorkflow(error: unknown): boolean {
  return errorCodeOf(error) === "conflict"
}

// isEnableRefusal reports a refusal of switching a workflow on specifically, so
// a screen can say "this will not run" rather than "the save failed".
export function isEnableRefusal(error: unknown): boolean {
  return enableRefusalReasonOf(error) !== null
}

const enableRefusalPhrases = [
  "at least one enabled destination",
  "needs an authenticated account",
]

// enableRefusalReasonOf is the server's own sentence when it refused to switch a
// workflow on, and null for any other failure. The message is kept verbatim:
// it names what is missing, and paraphrasing it would hide that.
export function enableRefusalReasonOf(error: unknown): string | null {
  const code = errorCodeOf(error)
  if (code !== "invalid_input") return null
  const message = messageOf(error)
  return enableRefusalPhrases.some((phrase) =>
    message.toLowerCase().includes(phrase)
  )
    ? message
    : null
}

// A workflow with no enabled destination publishes nowhere, and the backend
// refuses to switch one on ("a workflow needs at least one enabled destination
// before it can be enabled"). This is that same rule read before the request
// goes out, so the switch never looks active for a workflow the server will not
// accept. It refuses; it never silently enables anything.
export function workflowEnableBlocker(
  draft: Pick<WorkflowDraft, "destinations">
): string | null {
  return draft.destinations.some((destination) => destination.enabled)
    ? null
    : "برای فعال‌سازی دست‌کم یک مقصد فعال لازم است؛ ورک‌فلوی بدون مقصد فعال چیزی منتشر نمی‌کند."
}

export function errorCodeOf(error: unknown): string {
  return error && typeof error === "object" && "code" in error
    ? String((error as ApiError).code)
    : ""
}

// messageOf is the sentence the server wrote for this refusal. It is kept as it
// was written: it is where the field or the setting at fault is named.
export function messageOf(error: unknown): string {
  return error && typeof error === "object" && "message" in error
    ? String((error as { message: unknown }).message)
    : ""
}

// The specific refusals of this area, in the operator's language. Anything not
// named here keeps the server's own sentence rather than being paraphrased.
//
// Only codes whose server text is a fixed, generic one are named here. A code
// whose server text names the reason — `invalid_input` and `store_error` — is
// deliberately absent: replacing "enabling a workflow needs an authenticated
// account…" with "invalid input" would hide the one thing the operator needs.
const errorExplanations: Record<string, string> = {
  // `conflict` is not one refusal: a workflow answers it for a channel that
  // already has a workflow, a delivery for one that cannot be retried. The
  // sentence below is the workflow reading; the webhook screens keep their own
  // in `components/automation/webhook-errors.ts`, where the reason is known.
  conflict:
    "برای این کانال ورک‌فلوی دیگری ثبت شده است. هر کانال فقط یک ورک‌فلو دارد؛ همان را ویرایش کنید.",
  public_base_url_required:
    "برای استفاده از وب‌هوک باید نشانی عمومی سرویس (api.public_base_url) در پیکربندی تنظیم شود؛ بدون آن نشانی رسانه‌ها برای گیرنده قابل دسترسی نیست.",
  automation_source_forbidden:
    "مجوز دسترسی به کانال مبدأ را ندارید.",
  automation_destination_forbidden:
    "مجوز انتشار در یکی از مقاصد انتخاب‌شده را ندارید.",
  automation_unavailable:
    "اتوماسیون در پیکربندی این سرویس روشن نیست. در فایل پیکربندی «automation.enabled» را true کنید و سرویس را دوباره راه بیندازید.",
  not_found: "موردی با این شناسه پیدا نشد.",
  forbidden: "شما مجوز این عمل را ندارید.",
  method_not_allowed: "این عملیات روی این رکورد مجاز نیست.",
}

// Codes the server refuses with a reason of its own: the message names the
// field, the range or the setting at fault. Those are never replaced by a
// generic sentence here — "enabling a workflow needs an authenticated account"
// is the whole answer, and "invalid input" would hide it.
const reasonedRefusals = new Set(["invalid_input", "store_error", "aborted"])

export function explainAutomationError(error: unknown): string {
  const code = errorCodeOf(error)
  const message = messageOf(error)
  if (message && reasonedRefusals.has(code)) return message
  const explained = errorExplanations[code]
  if (explained) return explained
  // Anything else the service said in its own words is kept as it was written,
  // whether it arrived as an Error or as a plain payload.
  return message || "عملیات انجام نشد."
}

// How long one run took, from the moment it was queued to the moment it
// finished. A run still going has no duration yet, and a clock that runs
// backwards is reported as unknown rather than as a negative number.
export function runDurationMs(run: Pick<RunItem, "createdAt" | "completedAt">): number | null {
  if (!run.completedAt) return null
  const end = new Date(run.completedAt).getTime()
  const start = new Date(run.createdAt).getTime()
  if (!Number.isFinite(end) || !Number.isFinite(start) || end < start) return null
  return end - start
}

// ---------------------------------------------------------------------------
// Bodies. Write-only secrets: `secret` is posted only when the operator typed
// one, so an edit that leaves the field alone never rewrites the stored value.
// ---------------------------------------------------------------------------

export interface WebhookDraft {
  name: string
  description: string
  enabled: boolean
  url: string
  method: HttpMethod
  authMode: AuthMode
  authHeader: string
  // null means "leave the stored secret as it is".
  secret: string | null
  timeoutMs: number
  maxAttempts: number
  allowLocalhost: boolean
  sourceChannelIds: number[]
}

export function emptyWebhookDraft(): WebhookDraft {
  return {
    name: "",
    description: "",
    enabled: true,
    url: "",
    method: "POST",
    authMode: "none",
    authHeader: "",
    secret: null,
    timeoutMs: 5_000,
    maxAttempts: 3,
    allowLocalhost: false,
    sourceChannelIds: [],
  }
}

export function webhookDraftOf(webhook: WebhookItem): WebhookDraft {
  return {
    name: webhook.name,
    description: webhook.description,
    enabled: webhook.enabled,
    url: webhook.url,
    method: webhook.method,
    authMode: webhook.authMode,
    authHeader: webhook.authHeader,
    // Never seeded from the server: the plaintext is not in the response.
    secret: null,
    timeoutMs: webhook.timeoutMs,
    maxAttempts: webhook.maxAttempts,
    allowLocalhost: webhook.allowLocalhost,
    sourceChannelIds: [...webhook.sourceChannelIds],
  }
}

export function webhookBody(draft: WebhookDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    description: draft.description.trim(),
    enabled: draft.enabled,
    url: draft.url.trim(),
    method: draft.method,
    auth_mode: draft.authMode,
    auth_header: draft.authMode === "header" ? draft.authHeader.trim() : "",
    // Omitted entirely when the operator typed nothing: `null` clears a stored
    // secret, an absent key leaves it alone.
    ...(draft.secret === null ? {} : { secret: draft.secret }),
    timeout_ms: draft.timeoutMs,
    max_attempts: draft.maxAttempts,
    allow_localhost: draft.allowLocalhost,
    source_channel_ids: [...draft.sourceChannelIds],
  }
}

export interface DestinationDraft {
  type: DestinationType
  clientKey: string
  channelID: number | null
  siteID: number | null
  // Options is carried through untouched. The WordPress editor writes only
  // `category_ids`, `tag_ids` and `thumbnail`; anything else another client
  // stored survives a save here rather than being erased by a posted `{}`.
  options: Record<string, unknown>
  enabled: boolean
}

export interface WorkflowDraft {
  managedChannelID: number | null
  enabled: boolean
  aiEnabled: boolean
  aiPrompt: string
  aiSendImages: boolean
  aiPublishOnFailure: boolean
  destinations: DestinationDraft[]
}

export function emptyWorkflowDraft(): WorkflowDraft {
  return {
    managedChannelID: null,
    // A new workflow starts off. Switching one on is a separate, deliberate
    // step — and one the backend refuses unless the workflow has an enabled
    // destination to publish to and an account to publish under.
    enabled: false,
    aiEnabled: false,
    aiPrompt: "",
    aiSendImages: false,
    aiPublishOnFailure: false,
    destinations: [],
  }
}

// One destination row, built the same way wherever the UI adds one. A
// destination is addressed exactly one way: `clientKey` + `channelID` for
// Telegram, `siteID` for WordPress; the other address is left null so the
// payload builder can write the zero the schema expects.
export function telegramDestination(
  clientKey: string,
  channelID: number,
  enabled = true
): DestinationDraft {
  return {
    type: "telegram",
    clientKey,
    channelID,
    siteID: null,
    options: {},
    enabled,
  }
}

export function wordpressDestination(
  siteID: number,
  enabled = true
): DestinationDraft {
  return {
    type: "wordpress",
    clientKey: "",
    channelID: null,
    siteID,
    options: {},
    enabled,
  }
}

export function workflowDraftOf(workflow: WorkflowItem): WorkflowDraft {
  return {
    managedChannelID: workflow.managedChannelID,
    enabled: workflow.enabled,
    aiEnabled: workflow.aiEnabled,
    aiPrompt: workflow.aiPrompt,
    aiSendImages: workflow.aiSendImages,
    aiPublishOnFailure: workflow.aiPublishOnFailure,
    destinations: workflow.destinations.map((destination) => ({
      type: destination.type,
      clientKey: destination.clientKey,
      channelID: destination.type === "telegram" ? destination.channelID : null,
      siteID: destination.type === "wordpress" ? destination.siteID : null,
      options: { ...destination.options },
      enabled: destination.enabled,
    })),
  }
}

// The schema refuses a telegram row that names a site and a wordpress row that
// names a connection, so each destination is written with exactly one address.
// `client_key` + `channel_id` address a Telegram destination the way the manual
// publishing route does; `site_id` addresses a WordPress one.
export function workflowBody(draft: WorkflowDraft): Record<string, unknown> {
  return {
    managed_channel_id: draft.managedChannelID,
    enabled: draft.enabled,
    ai_enabled: draft.aiEnabled,
    ai_prompt: draft.aiPrompt.trim(),
    ai_send_images: draft.aiSendImages,
    ai_publish_on_failure: draft.aiPublishOnFailure,
    destinations: draft.destinations.map((destination, index) => ({
      destination_type: destination.type,
      ...(destination.type === "telegram"
        ? {
            client_key: destination.clientKey,
            channel_id: destination.channelID ?? 0,
            site_id: 0,
          }
        : {
            client_key: "",
            channel_id: 0,
            site_id: destination.siteID ?? 0,
          }),
      options: destination.options ?? {},
      position: index,
      enabled: destination.enabled,
    })),
  }
}

// ---------------------------------------------------------------------------
// Recorded bodies. Rendered as readable JSON when they are JSON, verbatim when
// they are not. A body the server redacted or cut short is shown exactly so.
// ---------------------------------------------------------------------------

export interface BodyView {
  text: string
  json: boolean
  truncated: boolean
  empty: boolean
}

export function bodyView(body: RecordedBody): BodyView {
  return bodyViewOf(body.body, body.truncated)
}

export function bodyViewOf(raw: string, truncated: boolean): BodyView {
  const text = raw ?? ""
  if (text.trim() === "") return { text: "", json: false, truncated, empty: true }
  try {
    return {
      text: JSON.stringify(JSON.parse(text), null, 2),
      json: true,
      truncated,
      empty: false,
    }
  } catch {
    return { text, json: false, truncated, empty: false }
  }
}

// headerLines turns a header map into the order-preserving lines a code block
// shows. A redacted value is rendered as it was stored, including its marker.
export function headerLines(headers: Record<string, string>): string[] {
  return Object.entries(visibleHeaders(headers)).map(
    ([name, value]) => `${name}: ${value}`
  )
}

// Credential-shaped headers are never rendered and never copied out of this
// panel. The server already replaces their values with `[redacted]` before
// storing, and this is the second lock on the same door: a header that carries
// or may carry a credential is withheld by name, whatever value it holds. The
// count is reported so a record never reads as "there were no headers".
const credentialHeaderNames = new Set([
  "authorization",
  "proxy-authorization",
  "cookie",
  "set-cookie",
  "x-api-key",
  "api-key",
  "x-auth-token",
  "x-access-token",
  "x-csrf-token",
])
const credentialNameShape =
  /(authorization|token|secret|password|passwd|credential|signature|session|cookie|api[-_]?key)/i

export function isCredentialHeader(name: string): boolean {
  const lower = name.trim().toLowerCase()
  return credentialHeaderNames.has(lower) || credentialNameShape.test(lower)
}

export function visibleHeaders(
  headers: Record<string, string>
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, value] of Object.entries(headers)) {
    if (!isCredentialHeader(name)) out[name] = value
  }
  return out
}

export function hiddenHeaderCount(headers: Record<string, string>): number {
  return Object.keys(headers).filter((name) => isCredentialHeader(name)).length
}
