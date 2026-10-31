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
    route("channels", "routes/_panel/channels.tsx"),
    route("channels/:id/messages", "routes/_panel/channel-messages.tsx"),
    route("wordpress", "routes/_panel/wordpress.tsx"),
    route("administrators", "routes/_panel/administrators.tsx"),
  ]),
] satisfies RouteConfig
