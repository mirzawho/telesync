import { XIcon } from "lucide-react"
import * as React from "react"
import { createPortal } from "react-dom"

import { cn } from "@workspace/ui/lib/utils"

export type ToastVariant = "default" | "success" | "error" | "warning"

export interface ToastOptions {
  title?: string
  description?: string
  variant?: ToastVariant
  /** Auto-dismiss delay; default 4000ms. */
  durationMs?: number
}

interface ToastRecord extends ToastOptions {
  id: number
}

interface ToastContextValue {
  toast: (opts: ToastOptions) => void
  dismiss: () => void
}

const ToastContext = React.createContext<ToastContextValue | null>(null)

// useToast gives a component the toast()/dismiss() pair. Outside a provider it
// throws rather than silently dropping the notification.
export function useToast(): ToastContextValue {
  const ctx = React.useContext(ToastContext)
  if (!ctx) {
    throw new Error("useToast must be used inside <ToastProvider>")
  }
  return ctx
}

const variantClasses: Record<ToastVariant, string> = {
  default: "bg-popover text-popover-foreground ring-foreground/10",
  success: "bg-primary/10 text-primary ring-primary/30",
  error: "bg-destructive/10 text-destructive ring-destructive/30",
  warning:
    "bg-amber-500/10 text-amber-700 ring-amber-500/40 dark:text-amber-400",
}

const DEFAULT_DURATION_MS = 4000

// ToastProvider mounts one top-center, polite live region for the whole app.
// Duplicate messages inside a short window replace the existing toast instead
// of stacking.
export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = React.useState<ToastRecord[]>([])
  const nextId = React.useRef(1)
  const timers = React.useRef(new Map<number, ReturnType<typeof setTimeout>>())
  const recent = React.useRef(new Map<string, number>())

  const remove = React.useCallback((id: number) => {
    const timer = timers.current.get(id)
    if (timer !== undefined) {
      clearTimeout(timer)
      timers.current.delete(id)
    }
    setItems((prev) => prev.filter((item) => item.id !== id))
  }, [])

  const toast = React.useCallback(
    (opts: ToastOptions) => {
      const text = `${opts.title ?? ""} ${opts.description ?? ""}`.trim()
      const now = Date.now()
      // Dedupe: a message already shown in the last 2.5s is dropped; a message
      // still on screen is refreshed and its timer restarted.
      if (text) {
        const seenAt = recent.current.get(text)
        if (seenAt !== undefined && now - seenAt < 2500) {
          return
        }
        if (seenAt !== undefined) {
          const live = items.find(
            (item) =>
              `${item.title ?? ""} ${item.description ?? ""}`.trim() === text
          )
          if (live) {
            const timer = timers.current.get(live.id)
            if (timer !== undefined) clearTimeout(timer)
            const delay = opts.durationMs ?? DEFAULT_DURATION_MS
            timers.current.set(
              live.id,
              setTimeout(() => remove(live.id), delay)
            )
            return
          }
        }
        recent.current.set(text, now)
      }
      const id = nextId.current++
      setItems((prev) => [...prev, { ...opts, id }])
      const delay = opts.durationMs ?? DEFAULT_DURATION_MS
      timers.current.set(id, setTimeout(() => remove(id), delay))
    },
    [items, remove]
  )

  const dismiss = React.useCallback(() => {
    timers.current.forEach((timer) => clearTimeout(timer))
    timers.current.clear()
    setItems([])
  }, [])

  React.useEffect(() => {
    const map = timers.current
    return () => {
      map.forEach((timer) => clearTimeout(timer))
      map.clear()
    }
  }, [])

  const value = React.useMemo(() => ({ toast, dismiss }), [toast, dismiss])

  if (typeof document === "undefined") {
    return <>{children}</>
  }

  return (
    <ToastContext.Provider value={value}>
      {children}
      {createPortal(
        <div
          role="status"
          aria-live="polite"
          aria-atomic="false"
          className="pointer-events-none fixed top-4 left-1/2 z-[100] flex w-full max-w-sm -translate-x-1/2 flex-col items-center gap-2 px-4"
        >
          {items.map((item) => (
            <ToastCard
              key={item.id}
              toast={item}
              onClose={() => remove(item.id)}
            />
          ))}
        </div>,
        document.body
      )}
    </ToastContext.Provider>
  )
}

function ToastCard({
  toast: item,
  onClose,
}: {
  toast: ToastRecord
  onClose: () => void
}) {
  const variant = item.variant ?? "default"
  return (
    <div
      data-slot="toast"
      data-variant={variant}
      className={cn(
        "pointer-events-auto flex w-full items-start gap-3 rounded-lg px-4 py-3 text-sm shadow-lg ring-1 backdrop-blur",
        variantClasses[variant]
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        {item.title && <p className="font-medium">{item.title}</p>}
        {item.description && (
          <p className="text-sm opacity-90">{item.description}</p>
        )}
      </div>
      <button
        type="button"
        onClick={onClose}
        aria-label="بستن"
        className="-mt-0.5 -me-1 rounded-sm p-1 text-current opacity-60 transition-opacity outline-none hover:opacity-100 focus-visible:ring-2 focus-visible:ring-current"
      >
        <XIcon aria-hidden="true" className="size-3.5" />
      </button>
    </div>
  )
}