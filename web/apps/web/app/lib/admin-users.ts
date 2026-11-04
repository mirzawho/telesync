// The administrator forms' outbound bodies, kept out of the components so the
// one rule that matters most can be read and tested on its own: a profile save
// never carries a password, and a new account is always created active.
//
// The API has no password route of its own (internal/gateway/users.go):
// PUT /local/users/{id} with a `password` and nothing else is the password
// change, and a nil field there is left unchanged. So the password dialog sends
// `password` alone and the profile dialog sends no password at all — an empty
// password field in a profile body would be a different, silent call.

export interface CreateAdministratorValues {
  fullname: string
  username: string
  password: string
}

export interface UpdateAdministratorValues {
  fullname: string
  username: string
  is_active: boolean
}

export function createUserBody(
  values: CreateAdministratorValues
): Record<string, unknown> {
  return {
    fullname: values.fullname,
    username: values.username,
    password: values.password,
    // The creation dialog offers no status choice: a new account starts active.
    is_active: true,
  }
}

export function updateUserBody(
  values: UpdateAdministratorValues
): Record<string, unknown> {
  return {
    fullname: values.fullname,
    username: values.username,
    is_active: values.is_active,
  }
}

export function passwordBody(password: string): Record<string, unknown> {
  return { password }
}

// accountStatusLabel is the wording beside the status switch, which follows the
// switch rather than the stored row until the save is confirmed.
export function accountStatusLabel(active: boolean): string {
  return active ? "فعال" : "غیرفعال"
}

export interface AdministratorFieldErrors {
  fullname?: string
  username?: string
  password?: string
}

// The API answers a taken username with 409 and this exact message
// (internal/gateway/users.go, userWriteError). The wire carries no code that
// separates it from the other 409 this page can meet — the last-administrator
// refusal — so the message is how the panel tells the two apart. That also
// decides where the error goes: a taken username belongs on the username field,
// not in a box above the form.
const duplicateUsernameMessage = "That username is already in use."
// Two refusals share this opening and differ only in what they ask for next
// (the account update names another account, the permission update names a
// permission), so the opening is what identifies them.
const lastAdministratorPrefix =
  "This is the last active account that can manage users"

// describeWriteFailure turns a refused write into what the dialog shows: one
// sentence and, where the refusal names a field, the field it belongs to. The
// two refusals this page can cause are phrased in Persian; anything else keeps
// the server's own sentence rather than inventing one for a refusal the panel
// does not recognise.
export function describeWriteFailure(
  intent: string,
  message: string
): { error: string; fieldErrors?: AdministratorFieldErrors } {
  if (message === duplicateUsernameMessage) {
    const fieldErrors = { username: "این نام کاربری قبلاً ثبت شده است." }
    return {
      error:
        intent === "create"
          ? "این نام کاربری قبلاً ثبت شده است."
          : "این نام کاربری متعلق به مدیر دیگری است.",
      fieldErrors,
    }
  }
  if (message.startsWith(lastAdministratorPrefix)) {
    return {
      error:
        "این آخرین حساب فعالی است که می‌تواند مدیرها را مدیریت کند؛ ابتدا حساب دیگری را فعال کنید.",
    }
  }
  return { error: message }
}

// channelstore.MinPasswordLen is 8 (internal/channelstore/users.go). The form
// only asserts the length so it fails before a round trip; the API stays the
// authority on the policy.
export const minPasswordLength = 8

export function passwordError(password: string): string | null {
  return password.length < minPasswordLength
    ? "رمز عبور باید دست‌کم ۸ نویسه باشد."
    : null
}
