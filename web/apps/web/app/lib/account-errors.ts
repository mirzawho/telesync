// What an account or login failure says, in Persian, by category.
//
// Same contract as `lib/publish-errors.ts`: the backend's own sentence is
// English and may change, so it never reaches this interface. The API's stable
// `code` is what says which failure this is — a code is a contract, a message
// is not — and every code gets a Persian sentence, including one this build has
// never seen.
//
// The codes here are exactly the ones the account contract adds
// (`V005-ACCOUNT-CONTRACTS.md` §6.3) plus the shared transport codes that can
// reach an account call. Nothing is invented: a code the API does not define
// has no entry here and falls through to the fallback sentence.

const MESSAGES: Record<string, string> = {
  // The contract's account family.
  account_selection_required:
    "چند حساب روی این سرور دارید؛ حسابی که می‌خواهید با آن کار کنید را انتخاب کنید.",
  account_not_owned: "این حساب متعلق به شما نیست.",
  account_not_connected: "این حساب متصل نیست؛ نخست وارد حساب شوید.",
  login_attempt_expired: "مهلت این تلاش ورود تمام شد؛ دوباره از نخست شروع کنید.",
  login_attempt_invalid: "این تلاش ورود دیگر معتبر نیست؛ دوباره شروع کنید.",
  login_rate_limited:
    "درخواست ورود بیش از حد مجاز تکرار شد؛ کمی بعد دوباره تلاش کنید.",
  unsupported_login_method: "این روش ورود روی این سرور پشتیبانی نمی‌شود.",
  session_encryption_unavailable:
    "ذخیرهٔ امن نشست در دسترس نیست؛ ورود انجام نمی‌شود.",
  // Login step failures the state machine reports.
  login_code_invalid: "کد واردشده درست نیست.",
  login_code_expired: "کد منقضی شده است؛ کد تازه‌ای درخواست کنید.",
  login_password_invalid: "رمز عبور دومرحله‌ای درست نیست.",
  login_phone_invalid: "شمارهٔ تلفن معتبر نیست.",
  // Shared transport and authorization codes.
  rate_limited: "محدودیت نرخ اعمال شد؛ کمی بعد دوباره تلاش کنید.",
  worker_unavailable: "ارتباط با سرور تلگرام برقرار نیست.",
  not_connected: "ارتباط با سرور تلگرام برقرار نیست.",
  worker_timeout: "پاسخ سرور تلگرام دیر رسید؛ دوباره تلاش کنید.",
  unauthorized: "نشست شما معتبر نیست؛ دوباره وارد شوید.",
  forbidden: "اجازهٔ انجام این کار را ندارید.",
  not_found: "حساب موردنظر پیدا نشد.",
  unknown_client: "سرور تلگرام انتخاب‌شده در دسترس نیست.",
  invalid_input: "اطلاعات ارسال‌شده معتبر نیست.",
  store_error: "ذخیرهٔ محلی با مشکل روبه‌رو شد؛ دوباره تلاش کنید.",
  network_error: "ارتباط با سرویس برقرار نشد.",
  conflict: "این درخواست با وضعیت کنونی حساب سازگار نیست.",
}

const TITLES: Record<string, string> = {
  account_selection_required: "انتخاب حساب لازم است",
  account_not_owned: "حساب در دسترس نیست",
  account_not_connected: "حساب متصل نیست",
  login_attempt_expired: "تلاش ورود منقضی شد",
  login_rate_limited: "محدودیت نرخ",
  unsupported_login_method: "روش ورود پشتیبانی نمی‌شود",
  session_encryption_unavailable: "ذخیرهٔ امن نشست در دسترس نیست",
  login_code_invalid: "کد نادرست",
  login_code_expired: "کد منقضی شده",
  login_password_invalid: "رمز عبور نادرست",
  rate_limited: "محدودیت نرخ",
  worker_unavailable: "ارتباط برقرار نیست",
}

const FALLBACK = "حساب متصل نشد؛ جزئیات در گزارش ثبت شده است."

const FALLBACK_TITLE = "حساب متصل نشد"

/**
 * accountErrorText is the sentence a reader sees for one failed account step.
 *
 * A code this build does not know still gets a Persian sentence: an
 * unrecognised failure is a failure in this interface, not an excuse to pour
 * the backend's own words into it. The request id travels beside it separately
 * so a reader can report a problem without the message carrying anything
 * internal.
 */
export function accountErrorText(
  code: string,
  _backendMessage?: string
): string {
  return MESSAGES[code] ?? FALLBACK
}

/**
 * accountErrorTitle is the short heading for the same failure.
 */
export function accountErrorTitle(code: string): string {
  return TITLES[code] ?? FALLBACK_TITLE
}

// isAccountErrorKnown is how a caller tells "the API named a failure this build
// understands" from "something else happened". A code that is not known still
// renders — it just renders as the fallback, and the caller may want to attach
// the request id to it.
export function isAccountErrorKnown(code: string): boolean {
  return code in MESSAGES
}

// ACCOUNT_ERROR_CODES is the contract's own list, exported so a test can hold
// the panel to it: every one of these must reach a reader as a real sentence,
// never as the backend's.
export const ACCOUNT_ERROR_CODES = [
  "account_selection_required",
  "account_not_owned",
  "account_not_connected",
  "login_attempt_expired",
  "login_attempt_invalid",
  "login_rate_limited",
  "unsupported_login_method",
  "session_encryption_unavailable",
] as const
