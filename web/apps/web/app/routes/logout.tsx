import { redirect } from "react-router"

import { setToken } from "@/lib/session"

// Logout forgets the token the browser holds. The API's tokens are stateless
// and expire on their own, so there is no server-side session to revoke.
export async function clientAction() {
  setToken(null)
  return redirect("/login")
}

export function clientLoader() {
  return redirect("/login")
}
