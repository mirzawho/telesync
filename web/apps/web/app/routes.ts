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
    route("wordpress", "routes/_panel/wordpress.tsx"),
    route("administrators", "routes/_panel/administrators.tsx"),
    // The automation area: a status front page and the two collections it
    // manages, each with its own history beside it. A create screen is the same
    // screen as its editor: `new` is the "no record yet" value of the id
    // segment, so there is never a second copy of a form.
    route("automation", "routes/_panel/automation.tsx"),
    route("automation/webhooks", "routes/_panel/automation-webhooks.tsx"),
    // Creating and editing a webhook are one screen, exactly as they are for
    // workflows below: `new` is the one non-numeric value of the id segment and
    // the page reads it as "no webhook yet", so a direct navigation and a
    // refresh land on the same form. `new` is never taken for a record id, and
    // any other non-numeric segment is not a record either — the page reports
    // it as missing rather than inventing one. There is no separate create
    // route, which would have no id to answer with. Both
    //   /admin/automation/webhooks/new   (create)
    //   /admin/automation/webhooks/7     (edit)
    // resolve to this one entry.
    route(
      "automation/webhooks/:webhookId",
      "routes/_panel/automation-webhooks.tsx",
      { id: "routes/_panel/automation-webhook-editor" }
    ),
    route(
      "automation/webhooks/:webhookId/deliveries",
      "routes/_panel/automation-deliveries.tsx"
    ),
    route(
      "automation/webhooks/:webhookId/deliveries/:deliveryId",
      "routes/_panel/automation-delivery.tsx"
    ),
    route("automation/workflows", "routes/_panel/automation-workflows.tsx"),
    // Creating and editing are one screen: `new` is the "no id yet" value of
    // the same segment, so the editor never has two copies of its own form.
    route(
      "automation/workflows/:workflowId",
      "routes/_panel/automation-workflow-editor.tsx"
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
