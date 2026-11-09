import { Link, redirect } from "react-router"

import { EmptyState } from "@/components/states"
import { WebhookIcon, WorkflowIcon } from "lucide-react"

import { automationRoute } from "@/lib/automation"
import { pageMeta, section, PageShell } from "@/lib/page"
import { requestURL, strip } from "@/path"

import type { Route } from "./+types/automation-legacy-redirect"

// The addresses these screens had before create and edit became pages of their
// own. Every one of them answers with where that content lives now, so a
// bookmark, a link in a message or a browser's back button keeps working. Only
// an id that was never a record is treated as unknown: it is reported, never
// quietly turned into an editor for a record that does not exist.

export const meta = () => pageMeta("اتوماسیون")

export const handle = section("اتوماسیون")

const RECORD = /^\/automation\/(webhooks|workflows)\/([0-9]+)$/
const COLLECTION = /^\/automation\/(webhooks|workflows)(?:\/|$)/

type LoaderData = { status: "missing"; collection: "webhooks" | "workflows" }

export async function clientLoader({
  request,
}: Route.LoaderArgs): Promise<Response | LoaderData> {
  const path = strip(requestURL(request).pathname)
  const collection = COLLECTION.exec(path)?.[1] === "workflows" ? "workflows" : "webhooks"

  const record = RECORD.exec(path)
  if (record) {
    const [, kind, id] = record
    return redirect(
      kind === "webhooks"
        ? automationRoute.editWebhook(Number(id))
        : automationRoute.editWorkflow(Number(id))
    )
  }
  // The collection's own view is where a bare address belongs.
  if (path === automationRoute.webhooks || path === automationRoute.workflows) {
    return redirect(`${automationRoute.root}?tab=${collection}`)
  }
  // `new` is a real route of its own and resolves without our help; anything
  // else was never a record. It is reported rather than quietly turned into an
  // editor for one that does not exist.
  return { status: "missing", collection }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="اتوماسیون">
      <EmptyState
        icon={WebhookIcon}
        title="صفحه پیدا نشد"
        description={error instanceof Error ? error.message : ""}
      />
    </PageShell>
  )
}

export default function AutomationLegacyRedirect({
  loaderData,
}: Route.ComponentProps) {
  const webhook = loaderData.collection === "webhooks"
  return (
    <PageShell title="اتوماسیون">
      <EmptyState
        icon={webhook ? WebhookIcon : WorkflowIcon}
        title={webhook ? "وب‌هوک پیدا نشد" : "ورک‌فلو پیدا نشد"}
        description="شناسه در نشانی معتبر نیست، یا این رکورد حذف شده است."
        action={
          <Link
            to={`${automationRoute.root}?tab=${loaderData.collection}`}
            className="text-sm underline-offset-4 hover:underline"
          >
            بازگشت به اتوماسیون
          </Link>
        }
      />
    </PageShell>
  )
}
