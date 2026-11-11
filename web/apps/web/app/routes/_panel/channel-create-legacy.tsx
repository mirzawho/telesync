import { Navigate, useLocation } from "react-router"

export function legacyCreateChannelDestination(search: string): string {
  const query = new URLSearchParams(search)
  query.set("create", "1")
  return `/channels?${query.toString()}`
}

// Preserve bookmarks to the former create page while hosting the actual form
// in the explorer, where the channel list and message viewer remain mounted.
export default function LegacyChannelCreateRoute() {
  const location = useLocation()
  return <Navigate to={legacyCreateChannelDestination(location.search)} replace />
}
