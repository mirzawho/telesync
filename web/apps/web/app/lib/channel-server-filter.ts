/** Parse repeated `server` query params as configured connection keys. */
export function parseServerSelection(
  params: URLSearchParams,
  availableKeys: readonly string[]
): string[] {
  const available = new Set(availableKeys)
  return [...new Set(params.getAll("server").map((key) => key.trim()).filter((key) => key && available.has(key)))]
}

/** Empty selection means all; unchecking its last key returns to all. */
export function toggleServerSelection(
  selected: readonly string[],
  key: string,
  availableKeys: readonly string[]
): string[] {
  const allKeys = [...new Set(availableKeys)]
  if (selected.length === 0) return allKeys.length === 1 && allKeys[0] === key ? [] : [key]
  const next = selected.includes(key)
    ? selected.filter((value) => value !== key)
    : [...selected, key]
  const normalized = [...new Set(next)]
  return allKeys.length > 0 && allKeys.every((value) => normalized.includes(value))
    ? []
    : normalized
}

/** Write selection while leaving every unrelated query parameter untouched. */
export function writeServerSelection(
  current: URLSearchParams,
  selected: readonly string[]
): URLSearchParams {
  const next = new URLSearchParams(current)
  next.delete("server")
  for (const key of new Set(selected)) next.append("server", key)
  return next
}
