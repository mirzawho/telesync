/* eslint-disable react-refresh/only-export-components */
import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@workspace/ui/lib/utils"

const badgeVariants = cva(
  "inline-flex w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-full border border-transparent px-2 py-0.5 text-xs font-medium whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground",
        secondary: "bg-secondary text-secondary-foreground",
        destructive: "bg-destructive/10 text-destructive dark:bg-destructive/20",
        outline: "border-border text-foreground dark:border-input",
        success:
          "bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-400",
        warning: "bg-amber-500/10 text-amber-700 dark:bg-amber-500/15 dark:text-amber-400",
        muted: "bg-muted text-muted-foreground",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

type BadgeProps = useRender.ComponentProps<"span"> & VariantProps<typeof badgeVariants>

function Badge({ className, variant, render, ...props }: BadgeProps) {
  return useRender({
    defaultTagName: "span",
    render,
    props: mergeProps<"span">(
      {
        ...{ "data-slot": "badge" },
        className: cn(badgeVariants({ variant }), className),
      },
      props
    ),
  })
}

export { Badge, badgeVariants }
