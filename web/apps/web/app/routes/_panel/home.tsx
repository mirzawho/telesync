import { redirect } from "react-router"

import { ErrorState } from "@/components/states"

import type { Route } from "./+types/home"

// The panel opens on the dashboard; the redirect runs in the browser, once.
export async function clientLoader() {
  return redirect("/dashboard")
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <ErrorState
        message={error instanceof Error ? error.message : "این مسیر باز نشد."}
      />
    </div>
  )
}
