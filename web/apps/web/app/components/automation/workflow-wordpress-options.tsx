import * as React from "react"

import { OptionCombobox } from "@/components/automation/option-combobox"
import { MultiPicker } from "@/components/multi-picker"
import { LoadingState } from "@/components/states"
import {
  Field,
  FieldDescription,
  FieldLabel,
} from "@workspace/ui/components/field"

import type { NamedOption } from "@/components/automation/workflow-form"

// One WordPress destination's own publishing settings, kept in the destination's
// `options` exactly as the publisher reads them.
//
// The keys and the values are the backend's vocabulary and nothing else:
// `category_ids` and `tag_ids` are this site's WordPress term ids, `post_status`
// is the status the post is created with and `thumbnail` is the destination's
// media policy ("source" publishes the source attachment along, "omit" publishes
// text alone). A term id is only ever one this site reported — this panel never
// types, guesses or generates one, because a workflow must never assign a
// WordPress term the operator did not make.

export const OPTION_CATEGORY_IDS = "category_ids"
export const OPTION_TAG_IDS = "tag_ids"
export const OPTION_POST_STATUS = "post_status"
export const OPTION_THUMBNAIL = "thumbnail"

export type ThumbnailPolicy = "source" | "omit"

export const thumbnailPolicyLabels: Record<ThumbnailPolicy, string> = {
  source: "انتشار تصویر همراه متن",
  omit: "انتشار بدون تصویر",
}

// The publication statuses the WordPress route itself understands. The value
// travels to the site as it is written here, so nothing else is offered: a
// status the site does not know would be a failure at publish time, not a
// choice.
export type PostStatus = "publish" | "draft" | "pending" | "private"

export const postStatusLabels: Record<PostStatus, string> = {
  publish: "انتشار فوری",
  draft: "پیش‌نویس",
  pending: "در انتظار بازبینی",
  private: "خصوصی",
}

export const postStatusValues = Object.keys(postStatusLabels) as PostStatus[]

// The publisher's own default is "source": a destination that names no policy
// keeps the source attachment. The editor writes the key explicitly anyway, so
// what is stored is what was chosen rather than what was assumed.
export const DEFAULT_THUMBNAIL: ThumbnailPolicy = "source"

// Likewise a destination that names no status is published by the publisher's
// own default; the editor writes the key explicitly either way.
export const DEFAULT_POST_STATUS: PostStatus = "publish"

export interface SiteTerms {
  categories: NamedOption[]
  tags: NamedOption[]
  loading: boolean
  error: string | null
}

export const emptySiteTerms = (): SiteTerms => ({
  categories: [],
  tags: [],
  loading: true,
  error: null,
})

const intList = (value: unknown): number[] =>
  Array.isArray(value)
    ? value.filter(
        (entry): entry is number => Number.isInteger(entry) && entry > 0
      )
    : []

export const categoryIDsOf = (options: Record<string, unknown>): number[] =>
  intList(options[OPTION_CATEGORY_IDS])
export const tagIDsOf = (options: Record<string, unknown>): number[] =>
  intList(options[OPTION_TAG_IDS])

export function thumbnailOf(options: Record<string, unknown>): ThumbnailPolicy {
  return options[OPTION_THUMBNAIL] === "omit" ? "omit" : DEFAULT_THUMBNAIL
}

// A status this editor does not offer is kept as it is stored rather than
// replaced: another client may have written one, and silently swapping it for
// the default would republish the site's own choice.
export function postStatusOf(options: Record<string, unknown>): string {
  const raw = options[OPTION_POST_STATUS]
  return typeof raw === "string" && raw !== "" ? raw : DEFAULT_POST_STATUS
}

export function isKnownPostStatus(value: string): value is PostStatus {
  return (postStatusValues as string[]).includes(value)
}

// The picker answers with the ids it is showing. Ids that are stored but that
// this page could not resolve — the site refused the read, or a term was
// deleted — are kept rather than dropped, so a save never silently unassigns a
// term the operator cannot see.
function keptUnresolved(
  chosen: number[],
  known: NamedOption[],
  previous: number[]
) {
  const seen = new Set(known.map((term) => term.id))
  return [...new Set([...chosen, ...previous.filter((id) => !seen.has(id))])]
}

export function WordPressDestinationOptions({
  site,
  terms,
  options,
  onOptionsChange,
  pending,
}: {
  site: NamedOption
  terms: SiteTerms
  options: Record<string, unknown>
  onOptionsChange: (next: Record<string, unknown>) => void
  pending?: boolean
}) {
  const categories = categoryIDsOf(options)
  const tags = tagIDsOf(options)
  const unknownCategories = categories.filter(
    (id) => !terms.categories.some((term) => term.id === id)
  )
  const unknownTags = tags.filter(
    (id) => !terms.tags.some((term) => term.id === id)
  )

  return (
    // No border of its own: this is the body of one destination block in the
    // workflow form, and a bordered box inside a bordered box reads as two
    // unrelated panels.
    <section
      className="flex flex-col gap-3"
      aria-labelledby={`wp-site-${site.id}`}
    >
      <h4 id={`wp-site-${site.id}`} className="text-sm font-medium">
        {site.label}
        {site.detail ? (
          <span dir="ltr" className="ms-2 text-xs text-muted-foreground">
            {site.detail}
          </span>
        ) : null}
      </h4>

      <Field>
        <FieldLabel htmlFor={`wp-status-${site.id}`}>وضعیت انتشار</FieldLabel>
        <OptionCombobox
          id={`wp-status-${site.id}`}
          label="وضعیت انتشار"
          options={[
            ...postStatusValues.map((value) => ({
              value,
              label: postStatusLabels[value],
            })),
            // A status this editor does not offer is shown as it is stored, so
            // the field never claims a choice the record does not hold.
            ...(isKnownPostStatus(postStatusOf(options))
              ? []
              : [
                  {
                    value: postStatusOf(options),
                    label: postStatusOf(options),
                  },
                ]),
          ]}
          value={postStatusOf(options)}
          onChange={(value) =>
            onOptionsChange({ ...options, [OPTION_POST_STATUS]: value })
          }
          placeholder="وضعیت انتشار را انتخاب کنید"
          disabled={pending}
        />
        <FieldDescription>
          نوشته در سایت مقصد با همین وضعیت ساخته می‌شود.
        </FieldDescription>
      </Field>

      <Field>
        <FieldLabel htmlFor={`wp-thumbnail-${site.id}`}>
          تصویر شاخص نوشته
        </FieldLabel>
        <OptionCombobox
          id={`wp-thumbnail-${site.id}`}
          label="تصویر شاخص نوشته"
          options={(["source", "omit"] as ThumbnailPolicy[]).map((value) => ({
            value,
            label: thumbnailPolicyLabels[value],
          }))}
          value={thumbnailOf(options)}
          onChange={(value) =>
            onOptionsChange({
              ...options,
              [OPTION_THUMBNAIL]: value === "omit" ? "omit" : "source",
            })
          }
          placeholder="سیاست تصویر را انتخاب کنید"
          disabled={pending}
        />
        <FieldDescription>
          «انتشار بدون تصویر» فقط متن را می‌فرستد؛ تصویر پیام مبدأ در این مقصد
          منتشر نمی‌شود.
        </FieldDescription>
      </Field>

      {terms.loading ? (
        <LoadingState label="در حال بارگذاری دسته‌بندی‌ها و برچسب‌ها…" />
      ) : (
        <>
          <MultiPicker
            id={`wp-categories-${site.id}`}
            title="دسته‌بندی‌های وردپرس"
            description="دسته‌بندی‌های همین سایت؛ شناسه‌ها از خود سایت خوانده می‌شوند."
            placeholder="دسته‌بندی را انتخاب کنید"
            emptyText={
              terms.error
                ? "دسته‌بندی‌های این سایت خوانده نشد."
                : "دسته‌بندی‌ای در این سایت نیست."
            }
            items={terms.categories}
            value={categories}
            onValueChange={(ids) =>
              onOptionsChange({
                ...options,
                [OPTION_CATEGORY_IDS]: keptUnresolved(
                  ids,
                  terms.categories,
                  categories
                ),
              })
            }
            labelOf={(term) => term.label}
            disabled={pending}
          />
          <MultiPicker
            id={`wp-tags-${site.id}`}
            title="برچسب‌های وردپرس"
            description="برچسب‌های همین سایت؛ شناسه‌ها از خود سایت خوانده می‌شوند."
            placeholder="برچسب را انتخاب کنید"
            emptyText={
              terms.error
                ? "برچسب‌های این سایت خوانده نشد."
                : "برچسبی در این سایت نیست."
            }
            items={terms.tags}
            value={tags}
            onValueChange={(ids) =>
              onOptionsChange({
                ...options,
                [OPTION_TAG_IDS]: keptUnresolved(ids, terms.tags, tags),
              })
            }
            labelOf={(term) => term.label}
            disabled={pending}
          />
        </>
      )}

      {terms.error && (
        <p role="alert" className="text-xs text-destructive">
          {terms.error}
        </p>
      )}

      {(unknownCategories.length > 0 || unknownTags.length > 0) && (
        <p className="text-xs text-muted-foreground">
          شناسه‌های زیر در این سایت پیدا نشدند و بدون تغییر نگه داشته می‌شوند:{" "}
          <span dir="ltr">
            {[...unknownCategories, ...unknownTags].join("، ")}
          </span>
        </p>
      )}
    </section>
  )
}
