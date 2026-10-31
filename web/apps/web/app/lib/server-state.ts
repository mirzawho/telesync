import type { TelegramServer } from "./types"

// A server's connection, as the dashboard says it: three connection states,
// and "needs login" only for a connection that is up but has no account.
export type ConnectionState =
  | "connecting"
  | "connected"
  | "error"
  | "needs_login"

// connectionOf reads one state from the API's report:
//
//   - the worker still starting, or its transport connecting: در حال اتصال.
//     A server that has not connected yet is not "needs login".
//   - connected with transport ready: متصل — or نیازمند ورود when the account
//     is not authorized.
//   - an error or a dropped connection: خطا در اتصال.
//   - no worker at all, or one that did not answer: also خطا در اتصال, because
//     the panel cannot claim a connection it has not seen.
export function connectionOf(server: TelegramServer): ConnectionState {
  if (server.state === "starting" || server.transport === "connecting") {
    return "connecting"
  }
  if (server.state === "connected" && server.transport === "ready") {
    return server.authorized === false ? "needs_login" : "connected"
  }
  return "error"
}

export const connectionLabels: Record<ConnectionState, string> = {
  connecting: "در حال اتصال",
  connected: "متصل",
  error: "خطا در اتصال",
  needs_login: "نیازمند ورود",
}
