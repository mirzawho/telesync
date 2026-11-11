import { type RouteConfig, layout, route } from "@react-router/dev/routes"

// The panel's routes. The layout route owns the sidebar, header and the
// authentication guard; each page is a child, so direct navigation to any of
// them renders the same chrome.
export default [
  route("/", "routes/_panel/home.tsx"),
  route("login", "routes/login.tsx"),
  route("logout", "routes/logout.tsx"),
  layout("routes/_panel/layout.tsx", [
    route("dashboard", "routes/_panel/dashboard.tsx"),
    route("categories", "routes/_panel/categories.tsx"),
    // The channel workspace and the open channel's timeline are one screen:
    // the explorer renders the channel list, and the message history is its
    // child so that selecting a channel never unmounts the list.
    route("channels", "routes/_panel/channels.tsx", [
      route(":id/messages", "routes/_panel/channel-messages.tsx"),
    ]),
    route("channels/new", "routes/_panel/channel-create-legacy.tsx"),
    route("wordpress", "routes/_panel/wordpress.tsx"),
    route("administrators", "routes/_panel/administrators.tsx"),
    // The automation area: a status front page and the two collections it
    // manages, each with its own history beside it. A create screen is the same
    // screen as its editor: `new` is the "no record yet" value of the id
    // segment, so there is never a second copy of a form.
    route("automation", "routes/_panel/automation.tsx"),
    // Creating and editing are one form component on two routes: `new` is the
    // create page and `:id/edit` is the same form seeded from the stored row.
    // `new` is a literal and outranks `:webhookId` below, so it is never taken
    // for a record id.
    route("automation/webhooks/new", "routes/_panel/automation-webhook-form.tsx"),
    route(
      "automation/webhooks/:webhookId/edit",
      "routes/_panel/automation-webhook-form.tsx",
      { id: "routes/_panel/automation-webhook-edit" }
    ),
    // The two addresses these screens had before: the bare collection and a
    // bare record id. Both answer by pointing at where the content lives now,
    // so a bookmark or a link in a message keeps working. An id that was never
    // a record is reported, never turned into an editor.
    route(
      "automation/webhooks/:webhookId",
      "routes/_panel/automation-legacy-redirect.tsx",
      { id: "routes/_panel/automation-webhook-legacy" }
    ),
    route(
      "automation/webhooks",
      "routes/_panel/automation-legacy-redirect.tsx",
      { id: "routes/_panel/automation-webhooks-legacy" }
    ),
    route(
      "automation/webhooks/:webhookId/deliveries",
      "routes/_panel/automation-deliveries.tsx"
    ),
    route(
      "automation/webhooks/:webhookId/deliveries/:deliveryId",
      "routes/_panel/automation-delivery.tsx"
    ),
    route("automation/workflows/new", "routes/_panel/automation-workflow-form.tsx"),
    route(
      "automation/workflows/:workflowId/edit",
      "routes/_panel/automation-workflow-form.tsx",
      { id: "routes/_panel/automation-workflow-edit" }
    ),
    route(
      "automation/workflows/:workflowId",
      "routes/_panel/automation-legacy-redirect.tsx",
      { id: "routes/_panel/automation-workflow-legacy" }
    ),
    route(
      "automation/workflows",
      "routes/_panel/automation-legacy-redirect.tsx",
      { id: "routes/_panel/automation-workflows-legacy" }
    ),
    route(
      "automation/workflows/:workflowId/runs",
      "routes/_panel/automation-runs.tsx"
    ),
    route(
      "automation/workflows/:workflowId/runs/:runId",
      "routes/_panel/automation-run.tsx"
    ),
  ]),
] satisfies RouteConfig
