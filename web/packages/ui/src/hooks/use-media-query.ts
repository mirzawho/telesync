import * as React from "react"

// SSR-safe: the first render uses `defaultMatches` on the server and the real
// query on the client, then follows it.
function useMediaQuery(query: string, defaultMatches = false) {
  const [matches, setMatches] = React.useState(defaultMatches)

  React.useEffect(() => {
    const list = window.matchMedia(query)
    const update = () => setMatches(list.matches)
    update()
    list.addEventListener("change", update)
    return () => list.removeEventListener("change", update)
  }, [query])

  return matches
}

export function useIsMobile() {
  return useMediaQuery("(max-width: 767px)")
}

export { useMediaQuery }
