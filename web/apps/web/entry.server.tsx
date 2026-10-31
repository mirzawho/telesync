import { createReadableStreamFromReadable } from "@react-router/node"
import { renderToPipeableStream } from "react-dom/server"
import { ServerRouter, type EntryContext } from "react-router"
import { PassThrough } from "node:stream"

// The panel is static: its one page is rendered once, at build time, and every
// route after that is drawn in the browser by the router. This entry renders
// that page — the shell and the root route — with no data in it, because the
// panel's data comes from the API once the browser is running.
//
// React Router dispatches no loaders for a static build (`ssr: false`), so this
// is reached only for the page itself.
export default function handleRequest(
  request: Request,
  responseStatusCode: number,
  responseHeaders: Headers,
  routerContext: EntryContext
) {
  return new Promise<Response>((resolve, reject) => {
    const { pipe } = renderToPipeableStream(
      <ServerRouter context={routerContext} url={request.url} />,
      {
        onAllReady() {
          const body = new PassThrough()
          responseHeaders.set("Content-Type", "text/html; charset=utf-8")
          resolve(
            new Response(createReadableStreamFromReadable(body), {
              headers: responseHeaders,
              status: responseStatusCode,
            })
          )
          pipe(body)
        },
        onShellError: reject,
        onError(error) {
          reject(error)
        },
      }
    )
  })
}
