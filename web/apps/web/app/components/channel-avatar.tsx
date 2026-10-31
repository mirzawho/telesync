import * as React from "react"

import { getToken } from "@/lib/session"
import { cn } from "@workspace/ui/lib/utils"

// ChannelAvatar shows a managed channel's picture.
//
// The API's avatar route needs the bearer token, and a browser sends none when
// it loads an <img>; the panel therefore asks the API itself, with the token it
// holds, and shows the bytes through a blob URL. One fetch per picture: the
// blob is shared through a module cache, and freed when the page goes away.
const loaded = new Map<number, Promise<string | null>>()

function loadAvatar(id: number): Promise<string | null> {
  const cached = loaded.get(id)
  if (cached) return cached
  const token = getToken()
  if (!token || !Number.isInteger(id) || id <= 0) return Promise.resolve(null)
  const request = fetch(
    `${import.meta.env.BASE_URL}api/v1/local/channels/${id}/avatar`,
    {
      headers: { Authorization: `Bearer ${token}`, Accept: "image/*" },
    }
  )
    .then(async (response) => {
      if (!response.ok) return null
      return URL.createObjectURL(await response.blob())
    })
    .catch(() => null)
  loaded.set(id, request)
  return request
}

export function ChannelAvatar({
  id,
  title,
  className,
}: {
  id: number
  title: string
  className?: string
}) {
  const [url, setUrl] = React.useState<string | null>(null)

  React.useEffect(() => {
    let live = true
    loadAvatar(id).then((value) => {
      if (live) setUrl(value)
    })
    return () => {
      live = false
    }
  }, [id])

  // title is whatever the caller has: a record read mid-write, or an API
  // answer whose title the panel has not checked, must not crash the page.
  const initial = String(title ?? "")
    .trim()
    .charAt(0)

  return (
    <span
      className={cn(
        "flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-full bg-muted text-sm font-medium text-muted-foreground",
        className
      )}
      aria-hidden="true"
    >
      {url ? (
        <img src={url} alt="" className="size-full object-cover" />
      ) : (
        initial
      )}
    </span>
  )
}
