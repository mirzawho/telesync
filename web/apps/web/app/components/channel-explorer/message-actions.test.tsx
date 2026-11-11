import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  MessageActions,
  MessageDeleteConfirmation,
} from "@/components/channel-explorer/message-actions"
import { AlertDialog } from "@workspace/ui/components/alert-dialog"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)
const props = {
  canEdit: true,
  canDelete: true,
  deleting: false,
  deleteError: null,
  onEdit: () => undefined,
  onDelete: () => undefined,
}

test("message actions expose a compact accessible trigger when an action is allowed", () => {
  const out = html(<MessageActions {...props} />)
  assert.match(out, /aria-label="عملیات پیام"/)
  assert.match(out, /data-slot="dropdown-menu-trigger"/)
  assert.doesNotMatch(out, /ویرایش پیام|حذف پیام/)
})

test("message actions render no trigger when neither action is allowed", () => {
  const out = html(<MessageActions {...props} canEdit={false} canDelete={false} />)
  assert.equal(out, "")
})

test("edit and delete permissions are independently reflected in the menu", () => {
  const editOnly = html(<MessageActions {...props} canDelete={false} />)
  const deleteOnly = html(<MessageActions {...props} canEdit={false} />)
  assert.match(editOnly, /aria-label="عملیات پیام"/)
  assert.match(deleteOnly, /aria-label="عملیات پیام"/)
})

test("delete confirmation describes the destructive action and exposes its failure", () => {
  const out = html(
    <AlertDialog open>
      <MessageDeleteConfirmation
        deleting={false}
        deleteError="حذف انجام نشد"
        onCancel={() => undefined}
        onDelete={() => undefined}
      />
    </AlertDialog>
  )
  assert.match(out, /حذف پیام/)
  assert.match(
    out,
    /آیا مطمئن هستید که می‌خواهید این پیام را حذف کنید؟ این عملیات ممکن است قابل بازگشت نباشد\./
  )
  assert.match(out, /انصراف/)
  assert.match(out, /role="alert"/)
  assert.match(out, /حذف انجام نشد/)
})

test("deleting disables confirmation and prevents repeated submits", () => {
  const out = html(
    <AlertDialog open>
      <MessageDeleteConfirmation
        deleting
        deleteError={null}
        onCancel={() => undefined}
        onDelete={() => undefined}
      />
    </AlertDialog>
  )
  assert.match(out, /در حال حذف…/)
  assert.match(out, /disabled/)
})
