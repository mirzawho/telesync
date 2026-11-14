import { apiRequest } from "@/lib/api"
import type { Entity } from "@/lib/types"

export type ChannelMessageMediaKind =
  | "photo"
  | "video"
  | "audio"
  | "voice"
  | "document"

export interface ChannelMessagePayload {
  text: string
  entities: Entity[]
  media?: { kind: ChannelMessageMediaKind }
  keep_media?: true
  options: {
    silent?: boolean
    disable_web_page_preview?: boolean
  }
}

export interface ChannelMessageEditPayload {
  text: string
  entities: Entity[]
  media?: { kind: ChannelMessageMediaKind }
  keep_media?: true
  options: { disable_web_page_preview?: boolean }
}

export type SendOperationStatus =
  | "queued"
  | "processing"
  | "retrying"
  | "sent"
  | "failed"
  | "unknown"
  | (string & {})

export type EditOperationStatus =
  | "queued"
  | "processing"
  | "retrying"
  | "edited"
  | "failed"
  | "unknown"
  | (string & {})

export interface OperationError {
  code: string
  message: string
}

export interface SendAccepted {
  operation_id: string
  status: SendOperationStatus
  accepted_at: string
  destination: { account_id: string; server_key?: string; channel_id: string }
  status_url: string
}

export interface SendOperation {
  operation_id: string
  status: SendOperationStatus
  stage?: string
  attempt_count: number
  destination: { account_id: string; server_key?: string; channel_id: string }
  accepted_at: string
  result?: { message_id: number }
  error?: OperationError
}

export interface EditAccepted {
  operation_id: string
  status: EditOperationStatus
  accepted_at: string
  target: { account_id: string; server_key?: string; channel_id: string; message_id: number }
  status_url: string
}

export interface EditOperation {
  operation_id: string
  status: EditOperationStatus
  stage?: string
  attempt_count: number
  target: { account_id: string; server_key?: string; channel_id: string; message_id: number }
  accepted_at: string
  result?: { message_id: number; media_kind?: string }
  error?: OperationError
}

export interface DeleteMessageResult {
  deleted: boolean
  connection: { client_key: string }
  channel_id: string
  message_id: number
}

function targetPath(accountID: string, channelID: string | number): string {
  const remoteID = String(channelID)
  if (!/^acc_[0-9a-hjkmnp-tv-z]{26}$/.test(accountID) || !/^[1-9]\d*$/.test(remoteID)) {
    throw new Error("شناسهٔ حساب یا کانال تلگرام معتبر نیست.")
  }
  return `/telegram/accounts/${encodeURIComponent(accountID)}/channels/${remoteID}/messages`
}

function withMessage(path: string, messageID: number): string {
  if (!Number.isSafeInteger(messageID) || messageID <= 0) {
    throw new Error("شناسهٔ پیام معتبر نیست.")
  }
  return `${path}/${messageID}`
}

function requestBody(payload: ChannelMessagePayload | ChannelMessageEditPayload, file?: File): object | FormData {
  if (!file) return payload
  const form = new FormData()
  form.append("payload", JSON.stringify(payload))
  form.append("file", file, file.name)
  return form
}

export async function sendChannelMessage(
  accountID: string,
  channelID: string | number,
  payload: ChannelMessagePayload,
  idempotencyKey: string,
  options: { token?: string | null; signal?: AbortSignal; file?: File } = {}
) {
  const path = `${targetPath(accountID, channelID)}/send`
  const { data } = await apiRequest<SendAccepted>("POST", path, {
    token: options.token,
    signal: options.signal,
    headers: { "Idempotency-Key": idempotencyKey },
    body: requestBody(payload, options.file),
  })
  return data
}

export async function getSendOperation(
  accountID: string,
  channelID: string | number,
  operationID: string,
  options: { token?: string | null; signal?: AbortSignal } = {}
) {
  const path = `${targetPath(accountID, channelID)}/send/${encodeURIComponent(operationID)}`
  const { data } = await apiRequest<SendOperation>("GET", path, options)
  return data
}

export async function editChannelMessage(
  accountID: string,
  channelID: string | number,
  messageID: number,
  payload: ChannelMessageEditPayload,
  idempotencyKey: string,
  options: { token?: string | null; signal?: AbortSignal; file?: File } = {}
) {
  const path = withMessage(targetPath(accountID, channelID), messageID)
  const { data } = await apiRequest<EditAccepted>("PUT", path, {
    token: options.token,
    signal: options.signal,
    headers: { "Idempotency-Key": idempotencyKey },
    body: requestBody(payload, options.file),
  })
  return data
}

export async function getEditOperation(
  accountID: string,
  channelID: string | number,
  messageID: number,
  operationID: string,
  options: { token?: string | null; signal?: AbortSignal } = {}
) {
  const path = `${withMessage(targetPath(accountID, channelID), messageID)}/edit/${encodeURIComponent(operationID)}`
  const { data } = await apiRequest<EditOperation>("GET", path, options)
  return data
}

export async function deleteChannelMessage(
  accountID: string,
  channelID: string | number,
  messageID: number,
  options: { token?: string | null; signal?: AbortSignal } = {}
) {
  const path = withMessage(targetPath(accountID, channelID), messageID)
  const { data } = await apiRequest<DeleteMessageResult>("DELETE", path, options)
  return data
}
