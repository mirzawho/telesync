package gateway

import (
	"errors"
	"strconv"
	"strings"
)

// The list routes share one way of reading their filters off the query string:
// `page`, `per_page`, `q` and `category_ids`.
//
// A category filter always means OR semantics — a row filed under at least one
// of the selected categories is listed — and an empty selection is no filter at
// all. The filter and the search text are applied before pagination and inside
// the caller's own visibility.

// maxCategoryFilterIDs bounds a category filter. It is far above the number of
// categories any operator actually has, and keeps a query string from growing
// the binder's argument list.
const maxCategoryFilterIDs = 100

// parseCategoryFilterIDs reads `category_ids`, either repeated
// (`?category_ids=1&category_ids=2`) or comma-separated (`?category_ids=1,2`).
// Every id must be a positive integer.
func parseCategoryFilterIDs(q map[string][]string) ([]int64, error) {
	raw := q["category_ids"]
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]int64, 0, len(raw))
	seen := map[int64]bool{}
	for _, part := range raw {
		text := part
		for text != "" {
			var token string
			if i := strings.IndexByte(text, ','); i >= 0 {
				token, text = text[:i], text[i+1:]
			} else {
				token, text = text, ""
			}
			if strings.TrimSpace(token) == "" {
				return nil, errors.New("category_ids must be a comma-separated list of positive integers.")
			}
			id, err := strconv.ParseInt(strings.TrimSpace(token), 10, 64)
			if err != nil || id < 1 {
				return nil, errors.New("category_ids must be a comma-separated list of positive integers.")
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
			if len(out) > maxCategoryFilterIDs {
				return nil, errors.New("category_ids may name at most " +
					strconv.Itoa(maxCategoryFilterIDs) + " categories.")
			}
		}
	}
	return out, nil
}

// hasCategoryFilter reports whether the query names a category filter at all.
// It is how the callers tell "no filter" from "filter, applied", without
// counting an empty string as a selection.
func hasCategoryFilter(q map[string][]string) bool {
	for _, value := range q["category_ids"] {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// placeholders renders n SQL parameters for an IN list.
func placeholders(n int) string {
	return strings.Repeat("?,", n-1) + "?"
}

// toAny hands the driver ids as parameters.
func toAny(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}
