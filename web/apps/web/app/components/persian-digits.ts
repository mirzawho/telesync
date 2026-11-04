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

// A clock reading on its own, for a message footer that already sits under its
// day's separator: "۲۲:۳۰".
const timeFormat = new Intl.DateTimeFormat("fa-IR-u-ca-persian", {
  timeStyle: "short",
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

// faTime renders only the clock reading of an API timestamp, or an em dash when
// the API sent none.
export function faTime(value: string | null | undefined): string {
  if (!value) {
    return "—"
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return "—"
  }
  return timeFormat.format(date)
}
