// What a publishing failure says, in Persian, by category.
//
// The backend's own sentence is English and may change; the panel must not pour
// it into a Persian interface. What a reader needs is which stage failed and
// what to do about it, and the API's stable `code` is what says that — a code is
// a contract, a message is not.
//
// The split that matters most is the one between the two halves of a media
// transfer. An attachment the source could not be read is a source problem with
// a source remedy (try again, or publish without it). An attachment the
// destination would not take is the opposite stage and the opposite remedy.
// Neither is ever described as the other.

const MESSAGES: Record<string, string> = {
  publish_source_media_unavailable:
    "رسانهٔ پیام مبدأ خوانده نشد. دوباره تلاش کنید یا پیوست را حذف کنید.",
  publish_source_unavailable: "پیام مبدأ در دسترس نیست؛ ممکن است حذف شده باشد.",
  publish_source_forbidden: "اجازهٔ خواندن پیام مبدأ را ندارید.",
  publish_media_upload_failed: "مقصد این رسانه را نپذیرفت؛ چیزی منتشر نشد.",
  publish_media_unsupported: "این رسانه در مقصد انتخاب‌شده پشتیبانی نمی‌شود.",
  publish_unsupported: "این نوع محتوا در مقصد انتخاب‌شده پشتیبانی نمی‌شود.",
  publish_media_too_large: "حجم رسانه بیش از حد مجاز است.",
  publish_permission_denied: "اجازهٔ ارسال پیام در این مقصد را ندارید.",
  publish_destination_unavailable: "مقصد انتخاب‌شده در دسترس نیست.",
  publish_destination_forbidden: "این مقصد را نمی‌توان برای ارسال انتخاب کرد.",
  publish_forbidden: "اجازهٔ انتشار محتوا را ندارید.",
  publish_outcome_unknown:
    "نتیجهٔ ارسال مشخص نشد؛ برای جلوگیری از ارسال دوباره، به‌صورت خودکار تلاش نمی‌شود.",
  publish_idempotency_conflict: "این درخواست پیش‌تر با محتوای دیگری ثبت شده است.",
  publish_failed: "انتشار ناموفق بود.",
  publish_partial_failure: "بخشی از انتشار انجام نشد؛ موارد موفق دوباره ارسال نمی‌شوند.",
  publish_wordpress_auth_failed: "شناسنامهٔ وردپرس پذیرفته نشد.",
  publish_wordpress_forbidden: "کاربر وردپرس اجازهٔ انتشار این محتوا را ندارد.",
  publish_wordpress_unreachable: "ارتباط با سایت وردپرسی برقرار نشد.",
  publish_wordpress_timeout: "سایت وردپرسی به‌موقع پاسخ نداد؛ نتیجه مشخص نیست.",
  publish_wordpress_incompatible: "سایت انتخاب‌شده رابط برنامه‌نویسی درستی ندارد.",
  rate_limited: "محدودیت نرخ اعمال شد؛ کمی بعد دوباره تلاش کنید.",
  worker_unavailable: "ارتباط با سرور تلگرام برقرار نیست.",
  not_connected: "ارتباط با سرور تلگرام برقرار نیست.",
  worker_timeout: "پاسخ سرور تلگرام دیر رسید؛ دوباره تلاش کنید.",
  unauthorized: "نشست شما معتبر نیست؛ دوباره وارد شوید.",
  forbidden: "اجازهٔ انجام این کار را ندارید.",
  unknown_client: "سرور تلگرام انتخاب‌شده در دسترس نیست.",
  invalid_input: "اطلاعات ارسال‌شده معتبر نیست.",
  store_error: "ذخیرهٔ محلی با مشکل روبه‌رو شد؛ دوباره تلاش کنید.",
}

const TITLES: Record<string, string> = {
  publish_source_media_unavailable: "رسانهٔ مبدأ خوانده نشد",
  publish_media_upload_failed: "رسانه در مقصد پذیرفته نشد",
  publish_media_unsupported: "رسانه در مقصد پشتیبانی نمی‌شود",
  publish_source_unavailable: "پیام مبدأ در دسترس نیست",
  publish_permission_denied: "اجازهٔ ارسال نیست",
  publish_outcome_unknown: "نتیجهٔ ارسال مشخص نیست",
  rate_limited: "محدودیت نرخ",
}

const FALLBACK = "انتشار ناموفق بود؛ جزئیات در گزارش ثبت شده است."

const FALLBACK_TITLE = "انتشار ناموفق"

/**
 * publishErrorText is the sentence a reader sees for one failed attempt.
 *
 * A code this build does not know still gets a Persian sentence: an
 * unrecognised failure is a failure in this interface, not an excuse to show
 * the backend's own words. The request id travels beside it separately, so a
 * reader can report a problem without the message carrying anything internal.
 */
export function publishErrorText(code: string, _backendMessage?: string): string {
  return MESSAGES[code] ?? FALLBACK
}

/**
 * publishErrorTitle is the short heading for the same failure, for a place
 * that names the stage rather than explaining it.
 */
export function publishErrorTitle(code: string): string {
  return TITLES[code] ?? FALLBACK_TITLE
}
