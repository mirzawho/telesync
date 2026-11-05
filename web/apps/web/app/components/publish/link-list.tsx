import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { PlusIcon, Trash2Icon } from "lucide-react"

import { faDigits } from "@/components/persian-digits"
import { cn } from "@workspace/ui/lib/utils"
import type { LinkItem } from "@/lib/publish-draft"

/**
 * LinkList is the message's links as a list: the anchor text drawn in the body
 * beside the target it points at, each one editable and removable.
 *
 * It is the third of the message's managed fields, after the hashtags and the
 * mentions, and it differs from them in one way that the layout has to say:
 * a link is already *in* the message. Its anchor text is a word of a sentence,
 * so it is edited in place and never pulled to the end. Only a link added here
 * is appended to the message.
 *
 * A row commits when a field loses focus rather than on every keystroke, which
 * is what keeps the rewrite of the body from happening under a half-typed word.
 */
export function LinkList({
  links,
  onAdd,
  onEdit,
  onDelete,
  className,
}: {
  links: readonly LinkItem[]
  onAdd: (link: { anchorText: string; url: string }) => void
  onEdit: (id: string, patch: { anchorText?: string; url?: string }) => void
  onDelete: (id: string) => void
  className?: string
}) {
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <ul className="flex flex-col gap-2" dir="ltr">
        {links.map((link, i) => (
          <li key={link.id}>
            <LinkRow
              link={link}
              index={i}
              onEdit={(patch) => onEdit(link.id, patch)}
              onDelete={() => onDelete(link.id)}
            />
          </li>
        ))}
      </ul>

      {links.length === 0 && (
        <p className="text-xs text-muted-foreground">لینکی در این پیام نیست.</p>
      )}

      <AddLink onAdd={onAdd} />
    </div>
  )
}

function LinkRow({
  link,
  index,
  onEdit,
  onDelete,
}: {
  link: LinkItem
  index: number
  onEdit: (patch: { anchorText?: string; url?: string }) => void
  onDelete: () => void
}) {
  const n = faDigits(String(index + 1))
  const [anchorText, setAnchorText] = React.useState(link.anchorText)
  const [url, setUrl] = React.useState(link.url)

  // A row that is re-rendered from the model after an external change (the
  // message text was edited, say) must show the model, not the last keystroke.
  React.useEffect(() => {
    setAnchorText(link.anchorText)
    setUrl(link.url)
  }, [link.anchorText, link.url])

  const commitAnchor = () => {
    const next = anchorText.trim()
    // An emptied anchor text is not a link of anything; the row keeps the words
    // it had rather than rewriting the message to have none.
    if (!next || next === link.anchorText) {
      setAnchorText(link.anchorText)
      return
    }
    onEdit({ anchorText: next })
  }

  const commitUrl = () => {
    const next = url.trim()
    if (!next || next === link.url) {
      setUrl(link.url)
      return
    }
    onEdit({ url: next })
  }

  return (
    <div className="flex items-center gap-2 rounded-lg border border-input p-2">
      <Input
        value={anchorText}
        dir="auto"
        aria-label={`متن پیوند ${n}`}
        placeholder="متن پیوند"
        className="min-w-0 flex-1"
        onChange={(e) => setAnchorText(e.target.value)}
        onBlur={commitAnchor}
      />
      <Input
        value={url}
        dir="ltr"
        type="text"
        aria-label={`نشانی پیوند ${n}`}
        placeholder="https://example.com"
        className="min-w-0 flex-[2]"
        onChange={(e) => setUrl(e.target.value)}
        onBlur={commitUrl}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        onClick={onDelete}
        aria-label={`حذف لینک ${n}`}
        title={`حذف لینک ${n}`}
        className="text-muted-foreground hover:text-destructive"
      >
        <Trash2Icon className="size-4" />
      </Button>
    </div>
  )
}

/**
 * AddLink is the row that appends a link to the message.
 *
 * It is the only link control that changes where the text goes: a link added
 * here is written at the end of the message, and never spliced into the middle
 * of a sentence it was not part of.
 */
function AddLink({ onAdd }: { onAdd: (link: { anchorText: string; url: string }) => void }) {
  const [anchorText, setAnchorText] = React.useState("")
  const [url, setUrl] = React.useState("")

  const ready = anchorText.trim().length > 0 && url.trim().length > 0

  const add = () => {
    if (!ready) return
    onAdd({ anchorText: anchorText.trim(), url: url.trim() })
    setAnchorText("")
    setUrl("")
  }

  return (
    <div className="flex items-center gap-2 border-t border-input pt-2">
      <Input
        value={anchorText}
        dir="auto"
        aria-label="متن پیوند تازه"
        placeholder="متن پیوند"
        className="min-w-0 flex-1"
        onChange={(e) => setAnchorText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") add()
        }}
      />
      <Input
        value={url}
        dir="ltr"
        aria-label="نشانی پیوند تازه"
        placeholder="https://example.com"
        className="min-w-0 flex-[2]"
        onChange={(e) => setUrl(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") add()
        }}
      />
      <Button type="button" variant="outline" size="sm" onClick={add} disabled={!ready}>
        <PlusIcon className="size-4" />
        افزودن
      </Button>
    </div>
  )
}
