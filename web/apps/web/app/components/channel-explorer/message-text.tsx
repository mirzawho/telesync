import * as React from "react"

import { buildTextNodes, type InlineNode } from "@/lib/message-entities"
import type { Entity } from "@/lib/types"

// One message's text, drawn from Telegram's own formatting spans.
//
// The spans come from the API, so a hashtag is a hashtag because Telegram said
// so and not because a regular expression guessed. The source text is turned
// into React elements and never into HTML: a message is untrusted content from
// a channel, and nothing here may execute it. Newlines, emoji, mixed Persian
// and English and long unbroken URLs are kept exactly as sent.

export function MessageText({
  text,
  entities,
}: {
  text: string
  entities?: Entity[] | null
}) {
  const nodes = React.useMemo(
    () => buildTextNodes(text, entities),
    [text, entities]
  )
  return (
    <div className="whitespace-pre-wrap [overflow-wrap:anywhere]">
      {nodes.map((node, index) => (
        <Inline key={index} node={node} />
      ))}
    </div>
  )
}

function Inline({ node }: { node: InlineNode }) {
  const children = node.children?.length ? (
    node.children.map((child, index) => <Inline key={index} node={child} />)
  ) : (
    node.text
  )

  switch (node.kind) {
    case "bold":
      return <strong>{children}</strong>
    case "italic":
      return <em>{children}</em>
    case "underline":
      return <u>{children}</u>
    case "strike":
      return <s>{children}</s>
    case "spoiler":
      return (
        <span className="bg-foreground/15 rounded px-0.5" data-spoiler="">
          {children}
        </span>
      )
    case "blockquote":
      return (
        <blockquote
          className="border-inline-start-2 border-border ps-2 text-muted-foreground"
        >
          {children}
        </blockquote>
      )
    case "code":
      return (
        <code
          dir="ltr"
          className="rounded bg-background/60 px-1 py-0.5 font-mono text-[0.85em]"
        >
          {children}
        </code>
      )
    case "pre":
      return (
        <pre
          dir="ltr"
          className="overflow-x-auto rounded-lg bg-background/60 px-3 py-2 font-mono text-[0.85em]"
        >
          {children}
        </pre>
      )
    case "link":
      return <Outbound href={node.href}>{children}</Outbound>
    case "email":
      return <Outbound href={`mailto:${node.text}`}>{children}</Outbound>
    case "phone":
      return <Outbound href={`tel:${node.text}`}>{children}</Outbound>
    case "mention":
      // A Telegram username resolves to its public profile; a mention_name has
      // no username to link to and is left as the name Telegram sent.
      return /^@[\w]+$/.test(node.text) ? (
        <Outbound href={`https://t.me/${node.text.slice(1)}`}>{children}</Outbound>
      ) : (
        <span className="text-primary">{children}</span>
      )
    case "hashtag":
    case "cashtag":
    case "bot_command":
      return <span className="text-primary">{children}</span>
    default:
      return <>{children}</>
  }
}

// Outbound is a link out of the panel. It opens in its own tab, is told it is
// not ours to vouch for, and is isolated so a right-to-left message cannot
// reorder a left-to-right URL into something unreadable.
function Outbound({
  href,
  children,
}: {
  href?: string
  children: React.ReactNode
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer nofollow"
      dir="ltr"
      className="inline text-primary underline decoration-primary/40 underline-offset-2 [unicode-bidi:isolate] hover:decoration-primary"
    >
      {children}
    </a>
  )
}
