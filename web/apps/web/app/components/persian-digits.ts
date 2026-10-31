const persianDigits = "۰۱۲۳۴۵۶۷۸۹"

// faDigits keeps numbers in the Persian reading direction's own numerals, in
// rendered output only: API values and request payloads stay ASCII.
export function faDigits(value: number | string | null | undefined): string {
  if (value === null || value === undefined || value === "") {
    return "—"
  }
  return String(value).replace(/[0-9]/g, (d) => persianDigits[Number(d)])
}

const dateFormat = new Intl.DateTimeFormat("fa-IR-u-ca-persian", {
  dateStyle: "medium",
  timeStyle: "short",
})

const dateOnlyFormat = new Intl.DateTimeFormat("fa-IR-u-ca-persian", {
  dateStyle: "medium",
})

// faDateTime renders an API timestamp (RFC 3339) in the Jalali calendar, or an
// em dash when the API sent none.
export function faDateTime(
  value: string | null | undefined,
  withTime = true
): string {
  if (!value) {
    return "—"
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return "—"
  }
  return (withTime ? dateFormat : dateOnlyFormat).format(date)
}
