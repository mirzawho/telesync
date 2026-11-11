import type { ChannelMessage } from "@/lib/types"
import { ChannelMessageMedia } from "@/components/channel-explorer/message-media"
import { MessageText } from "@/components/channel-explorer/message-text"
import { Bubble, BubbleContent } from "@workspace/ui/components/bubble"
import { Message, MessageContent } from "@workspace/ui/components/message"
import { Button } from "@workspace/ui/components/button"

/** One Telegram album bubble. Captions are taken from the members that
 * actually carry them; no caption is copied onto every gallery item. */
export function ChannelMessageAlbum({ messages, onPublish }: { messages: ChannelMessage[]; onPublish?: () => void }) {
  const captioned = messages.filter((message) => typeof message.text === "string" && message.text.trim())
  return (
    <Message align="end" className="min-w-75">
      <MessageContent className="min-w-75">
        <Bubble variant="muted" className="min-w-75">
          <BubbleContent>
            <div data-slot="message-album-gallery" className="grid grid-cols-2 gap-1.5">
              {messages.map((message) => message.media ? (
                <div key={message.id} data-album-message-id={message.id} className="min-w-0 overflow-hidden rounded-lg">
                  <ChannelMessageMedia media={message.media} />
                </div>
              ) : null)}
            </div>
            {captioned.map((message) => (
              <div key={`caption:${message.id}`} data-album-caption-id={message.id} className="mt-2">
                <MessageText text={message.text!} entities={message.entities} />
              </div>
            ))}
            {onPublish && <Button type="button" variant="ghost" size="sm" className="mt-2" onClick={onPublish}>انتشار آلبوم</Button>}
          </BubbleContent>
        </Bubble>
      </MessageContent>
    </Message>
  )
}
