// React's side of the publishing draft.
//
// The rules live in lib/publish-draft and are proved there. This is only the
// state holder around them: one draft per source message, edits applied through
// a shallow copy so React sees the change, and no second copy of any rule.

import * as React from "react"

import {
  initDraftState,
  validate,
  type DraftValidation,
  type PublishDraftState,
  type PublishSource,
  type ValidateOptions,
} from "@/lib/publish-draft"

export interface PublishMessageDraft {
  state: PublishDraftState
  // update applies one mutator to a copy of the draft, which is what makes a
  // change visible to the renderer. The mutators themselves edit in place.
  update: (recipe: (state: PublishDraftState) => PublishDraftState) => void
  // validation reports every rule; display narrows it to the fields the user
  // has reached, so an untouched form is not a wall of errors.
  validation: DraftValidation
  display: DraftValidation
  // dirty is whether anything at all has been changed since the dialog opened.
  dirty: boolean
}

export function usePublishMessageDraft(source: PublishSource): PublishMessageDraft {
  const [state, setState] = React.useState<PublishDraftState>(() => initDraftState(source))
  const [dirty, setDirty] = React.useState(false)

  // A draft is seeded once per source message. A new message starts a new
  // draft on purpose; a rerender never does.
  const sourceKey = `${source.localChannelId}:${source.messageId}`
  const seeded = React.useRef(sourceKey)
  React.useEffect(() => {
    if (seeded.current === sourceKey) return
    seeded.current = sourceKey
    setState(initDraftState(source))
    setDirty(false)
  }, [sourceKey, source])

  const update = React.useCallback(
    (recipe: (current: PublishDraftState) => PublishDraftState) => {
      setState((prev) => {
        const next: PublishDraftState = {
          ...prev,
          telegram: { ...prev.telegram },
          wordpress: { ...prev.wordpress },
          touched: { ...prev.touched },
        }
        return recipe(next) ?? next
      })
      setDirty(true)
    },
    [],
  )

  const validation = React.useMemo(() => validate(state), [state])
  const display = React.useMemo(
    () => validate(state, { touched: state.touched } as ValidateOptions),
    [state],
  )

  return { state, update, validation, display, dirty }
}
