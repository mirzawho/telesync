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
  ]),
] satisfies RouteConfig
