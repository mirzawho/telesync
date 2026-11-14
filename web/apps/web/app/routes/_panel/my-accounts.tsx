import * as React from "react"
import {
  redirect,
  useNavigate,
  useNavigation,
  useRevalidator,
  useSubmit,
  type ShouldRevalidateFunctionArgs,
} from "react-router"
import { PlusIcon } from "lucide-react"

import {
  AccountDetailsDialog,
  AccountList,
  NoAccountsState,
} from "@/components/accounts/account-list"
import { AddAccountWizard } from "@/components/accounts/account-login-wizard"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { ErrorState, LoadingState } from "@/components/states"
import { accountErrorText } from "@/lib/account-errors"
import {
  disconnectAccount,
  listMyAccounts,
  logoutAccount,
  removeAccount,
} from "@/lib/account-api"
import { wizardServersOf, type AccountAction } from "@/lib/telegram-account"
import type { TelegramAccount, TelegramServer } from "@/lib/types"
import { ApiError, apiRequest } from "@/lib/api"
import { PageShell, pageMeta, section } from "@/lib/page"
import { PageHeader } from "@/components/page-header"
import { LOGIN_REDIRECT_TO, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import { Button } from "@workspace/ui/components/button"
import { useToast } from "@workspace/ui/components/toast"

import type { Route } from "./+types/my-accounts"

// حساب‌های من — the one screen that owns the signed-in user's Telegram accounts.
//
// Everything here is scoped to one reader by construction: the loader asks for
// `GET /me/telegram-accounts`, which the server answers with the caller's own
// accounts and nothing else. There is no id in this page's URL that could name
// another user's account, and nothing in it reads `owner_user_id` — the client
// never decides who owns what.
//
// The screen never shows a session, a credential or a full phone number; see
// `account-list.tsx` for what a row is allowed to carry.

export const meta = () => pageMeta("حساب‌های من")

export const handle = section("حساب‌های من")

export async function clientLoader({ request }: Route.LoaderArgs) {
  const { token } = await requireSession(request)
  const url = requestURL(request)
  try {
    const { accounts } = await listMyAccounts(token)
    // The wizard's step one needs the server definitions. They are the
    // admin-managed backends every account sits on — never anybody's session.
    let servers: TelegramServer[] = []
    try {
      const { data } = await apiRequest<{ servers?: TelegramServer[] }>(
        "GET",
        "/telegram",
        { token }
      )
      servers = data?.servers ?? []
    } catch {
      // Without a server listing the list still shows and the wizard reports
      // the refusal on its own first step. A missing listing is not a page
      // failure.
      servers = []
    }
    return { token, accounts, servers, loadError: null as string | null }
  } catch (error) {
    if (error instanceof ApiError && error.isAuthLost) {
      throw redirect(
        `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
      )
    }
    return {
      token,
      accounts: [] as TelegramAccount[],
      servers: [] as TelegramServer[],
      loadError:
        error instanceof ApiError
          ? accountErrorText(error.code, error.message)
          : "بارگذاری حساب‌ها ناموفق بود.",
    }
  }
}

// Only a submission revalidates: moving between this page and back must not
// throw the list away and refetch it.
export function shouldRevalidate({
  formMethod,
}: ShouldRevalidateFunctionArgs): boolean {
  return Boolean(formMethod)
}

interface AccountActionData {
  ok?: true
  error?: string
  intent?: string
}

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<AccountActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const accountID = String(form.get("account_id") ?? "").trim()
  const url = requestURL(request)

  if (!accountID) {
    return { error: "حساب انتخاب‌شده معتبر نیست.", intent }
  }

  try {
    if (intent === "disconnect") {
      await disconnectAccount(token, accountID)
    } else if (intent === "logout") {
      await logoutAccount(token, accountID)
    } else if (intent === "remove") {
      await removeAccount(token, accountID)
    } else {
      return { error: "درخواست نامعتبر است.", intent }
    }
    return redirect(strip(url.pathname))
  } catch (error) {
    if (error instanceof ApiError && error.isAuthLost) {
      throw redirect(
        `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(url.pathname))}`
      )
    }
    // The API's own sentence never reaches the reader: the stable code says
    // what happened and this build phrases it.
    return {
      error:
        error instanceof ApiError
          ? accountErrorText(error.code, error.message)
          : "عملیات انجام نشد.",
      intent,
    }
  }
}

// The confirmations a destructive action needs. Each names the effect and
// nothing beyond it: leaving a Telegram session and deleting a record are
// different consequences and are never described as one.
export const ACCOUNT_CONFIRMATIONS: Record<
  "logout" | "remove",
  { title: string; description: string; confirmLabel: string }
> = {
  logout: {
    title: "خروج از حساب تلگرام",
    description:
      "نشست تلگرام این حساب پایان می‌یابد. برای استفادهٔ دوباره باید دوباره وارد شوید؛ کانال‌ها و دسته‌بندی‌های ثبت‌شده باقی می‌مانند.",
    confirmLabel: "خروج از حساب",
  },
  remove: {
    title: "حذف حساب",
    description:
      "این حساب و نشست آن برای همیشه از پنل شما حذف می‌شود. کانال‌ها و عملیات‌هایی که از طریق این حساب انجام می‌شدند دیگر در دسترس نیستند و این کار قابل بازگشت نیست.",
    confirmLabel: "حذف حساب",
  },
}

export function confirmationForAccountAction(
  action: AccountAction,
  account: TelegramAccount
): { kind: "logout" | "remove"; account: TelegramAccount } | null {
  return action === "logout" || action === "remove"
    ? { kind: action, account }
    : null
}

export function MyAccountsHeader({ onAdd }: { onAdd: () => void }) {
  return (
    <PageHeader
      title="حساب‌های من"
      description="حساب‌های تلگرامی که خودتان به پنل متصل کرده‌اید. هیچ‌کدام از این حساب‌ها در اختیار کاربران دیگر قرار نمی‌گیرد."
      actions={
        <Button className="ms-auto" onClick={onAdd}>
          <PlusIcon data-icon="inline-start" />
          افزودن حساب
        </Button>
      }
    />
  )
}

export default function MyAccounts({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const { token, accounts, servers, loadError } = loaderData
  const navigate = useNavigate()
  const submit = useSubmit()
  const revalidator = useRevalidator()
  const navigation = useNavigation()
  const { toast } = useToast()

  const [wizardOpen, setWizardOpen] = React.useState(false)
  const [details, setDetails] = React.useState<TelegramAccount | null>(null)
  const [confirming, setConfirming] = React.useState<{
    kind: "logout" | "remove"
    account: TelegramAccount
  } | null>(null)

  const pending = navigation.state === "submitting"
  const loadingAccounts =
    revalidator.state === "loading" ||
    (navigation.state === "loading" &&
      navigation.location?.pathname.endsWith("/accounts"))

  // A submission that fails reopens the dialog it came from, so the refusal is
  // visible beside the thing that was refused rather than in a toast the
  // reader has to connect back to it.
  React.useEffect(() => {
    if (!actionData?.error) return
    toast({
      variant: "error",
      title: "انجام نشد.",
      description: actionData.error,
    })
  }, [actionData, toast])

  const act = (account: TelegramAccount, action: AccountAction) => {
    switch (action) {
      case "connect":
        setWizardOpen(true)
        return
      case "details":
        setDetails(account)
        return
      case "channels":
        void navigate(`/channels?account=${encodeURIComponent(account.id)}`)
        return
      case "disconnect":
        submit(
          { intent: "disconnect", account_id: account.id },
          { method: "post" }
        )
        return
      case "logout":
      case "remove": {
        const confirmation = confirmationForAccountAction(action, account)
        if (confirmation) setConfirming(confirmation)
        return
      }
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <MyAccountsHeader onAdd={() => setWizardOpen(true)} />
      {loadingAccounts ? (
        <LoadingState label="در حال بارگذاری حساب‌های شما…" />
      ) : loadError ? (
        <ErrorState
          title="حساب‌ها بارگذاری نشد"
          message={loadError}
          onRetry={() => window.location.reload()}
        />
      ) : accounts.length === 0 ? (
        // A reader with no account is told to add one. It is never shown a
        // permission refusal, which would send them to an administrator who
        // cannot help them.
        <NoAccountsState onAdd={() => setWizardOpen(true)} compact />
      ) : (
        <AccountList accounts={accounts} onAction={act} />
      )}

      <AddAccountWizard
        open={wizardOpen}
        onOpenChange={setWizardOpen}
        servers={wizardServersOf(servers)}
        token={token}
        onConnected={() => {
          setWizardOpen(false)
          revalidator.revalidate()
        }}
      />

      <AccountDetailsDialog
        account={details}
        open={details !== null}
        onOpenChange={(open) => !open && setDetails(null)}
      />

      {confirming && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setConfirming(null)}
          title={ACCOUNT_CONFIRMATIONS[confirming.kind].title}
          description={ACCOUNT_CONFIRMATIONS[confirming.kind].description}
          confirmLabel={ACCOUNT_CONFIRMATIONS[confirming.kind].confirmLabel}
          pending={pending}
          error={
            actionData?.error && actionData.intent === confirming.kind
              ? actionData.error
              : null
          }
          onConfirm={() => {
            submit(
              { intent: confirming.kind, account_id: confirming.account.id },
              { method: "post" }
            )
            setConfirming(null)
          }}
        />
      )}
    </div>
  )
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="حساب‌های من">
      <ErrorState
        title="این صفحه بارگذاری نشد"
        message={
          error instanceof ApiError
            ? accountErrorText(error.code, error.message)
            : "بارگذاری حساب‌ها ناموفق بود."
        }
        onRetry={() => window.location.reload()}
      />
    </PageShell>
  )
}
