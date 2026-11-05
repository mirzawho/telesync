import { redirect, useLocation, useNavigate, useParams } from "react-router"
import { ArrowRightIcon } from "lucide-react"

import { ChannelMessagesPane } from "@/components/channel-explorer/channel-messages-pane"
import { Button } from "@workspace/ui/components/button"
import { EmptyState } from "@/components/states"
import { hiddenNavSection, pageMeta } from "@/lib/page"
import { ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type { ChannelDetail } from "@/lib/types"

import type { Route } from "./+types/channel-messages"

export const meta = () => pageMeta("پیام‌های کانال")

// The message timeline is a chat screen: the floating navigation would cover
// the newest messages, so the shell tucks it away here.
export const handle = hiddenNavSection("کانال‌ها")

type ChannelLookup =
  // clientKey is the record's Telegram client key; empty when the answer did
  // not carry it, in which case reaction images fall back to their icon.
  | { state: "ok"; channel: ChannelDetail; clientKey: string }
  | { state: "missing" }
  | { state: "unavailable"; message: string; code: string }

// loadChannel reads the local record the route's id names. The record is the
// authority on the Telegram channel id and the client the messages come from;
// the route never accepts a Telegram id of its own. The service answers the
// canonical channel object, whose `id` is the channel's Telegram id — the page
// URL and the messages route keep the local id.
async function loadChannel(
  token: string,
  id: number | null,
  url: URL
): Promise<ChannelLookup> {
  if (id === null) return { state: "missing" }
  try {
    // The record route wraps it: data.channel, not the record itself. The
    // record's client_key names the Telegram client the channel lives in, which
    // the public channel object does not carry; it is read here for the
    // reaction images.
    const { data } = await apiRequest<{
      channel?: ChannelDetail
      client_key?: string
    }>("GET", `/local/channels/${id}`, { token })
    if (!data?.channel || typeof data.channel.id !== "string") {
      return {
        state: "unavailable",
        message: "پاسخ سرویس کانال را نداشت.",
        code: "invalid_response",
      }
    }
    return {
      state: "ok",
      channel: data.channel,
      clientKey: data.client_key ?? "",
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.isAuthLost) throw redirect(authRedirect(url))
      if (error.status === 404) return { state: "missing" }
      if (error.isForbidden) return { state: "missing" }
      return { state: "unavailable", message: error.message, code: error.code }
    }
    return {
      state: "unavailable",
      message: "بارگذاری کانال ناموفق بود.",
      code: "network_error",
    }
  }
}

function authRedirect(url: URL) {
  return `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
}

export async function clientLoader({ params, request }: Route.LoaderArgs) {
  const { token } = await requireSession(request)
  const url = requestURL(request)
  const raw = params.id ?? ""
  const id = /^[0-9]+$/.test(raw) ? Number(raw) : NaN
  return {
    lookup: await loadChannel(
      token,
      Number.isSafeInteger(id) && id > 0 ? id : null,
      url
    ),
    token,
  }
}

// This route is the workspace's left pane: it renders into the channel
// explorer's outlet, beside the channel list, and never into a page of its own.
export default function ChannelMessages({
  loaderData,
  params,
}: Route.ComponentProps) {
  const { lookup } = loaderData
  const navigate = useNavigate()
  const location = useLocation()
  // The URL's id is the local record id: it addresses the record and its
  // messages. The channel object's id is Telegram's and is never used as one.
  const localId = Number(params.id)
  // Going back keeps the search and the category chips the reader had set up,
  // so returning to the list shows the list they left.
  const back = () =>
    navigate({ pathname: "/channels", search: location.search })

  if (lookup.state === "missing") {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <EmptyState
          title="کانال پیدا نشد"
          description="کد کانال در نشانی معتبر نیست، یا این کانال در فهرست محلی نیست، یا مجوز خواندن آن را ندارید."
          action={
            <Button variant="outline" onClick={back}>
              <ArrowRightIcon />
              بازگشت به کانال‌ها
            </Button>
          }
        />
      </div>
    )
  }

  if (lookup.state === "unavailable") {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <EmptyState
          title="وضعیت کانال در دسترس نیست"
          description={`${lookup.message} (کد: ${lookup.code})`}
          action={
            <Button variant="outline" onClick={() => window.location.reload()}>
              تلاش دوباره
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <ChannelMessagesPane
      channel={lookup.channel}
      channelId={localId}
      client={lookup.clientKey}
      onBack={back}
    />
  )
}
