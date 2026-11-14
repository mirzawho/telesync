package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// categoriesEnv is the gateway backed by a real SQLite store in a temporary
// directory, so the HTTP contract is exercised end to end through the database.
//
// Everything in a category test happens through the API, as the operator: the
// caller holds every permission, and each category the test creates through the
// API gets a full grant as part of creation (grantCategoryOnCreate). The
// authorization rules themselves are tested in perms_test.go, users_test.go and
// auth_test.go.
func categoriesEnv(t *testing.T) (*accountEnv, *channelstore.Store) {
	t.Helper()
	env := newAccountEnv(t, registry{"official": readyWorker()})
	env.signedIn = true
	if err := env.store.ReplacePermissions(t.Context(), env.defaults.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionCategoriesCreate},
	}); err != nil {
		t.Fatal(err)
	}
	env.grantCategoryOnCreate()
	return env, env.store
}

type categoryJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type categoryPage struct {
	Data struct {
		Categories []categoryJSON `json:"categories"`
	} `json:"data"`
	Meta struct {
		Page       int  `json:"page"`
		PerPage    int  `json:"per_page"`
		Total      int  `json:"total"`
		TotalPages int  `json:"total_pages"`
		HasMore    bool `json:"has_more"`
	} `json:"meta"`
}

type categoryOne struct {
	Data struct {
		Category categoryJSON `json:"category"`
	} `json:"data"`
}

const categoriesPath = "/api/v1/local/categories"

func TestListCategoriesDefaultsToPageOneOfTwenty(t *testing.T) {
	env, _ := categoriesEnv(t)
	resp := env.get(t, categoriesPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[categoryPage](t, resp)
	if body.Meta.Page != 1 || body.Meta.PerPage != 20 || body.Meta.Total != 0 || body.Meta.TotalPages != 0 {
		t.Fatalf("meta = %+v", body.Meta)
	}
	if body.Data.Categories == nil || len(body.Data.Categories) != 0 {
		t.Fatalf("categories = %v, want an empty array", body.Data.Categories)
	}
}

func TestListCategoriesPaginates(t *testing.T) {
	env, store := categoriesEnv(t)
	for i := 1; i <= 5; i++ {
		if _, err := store.CreateCategory(t.Context(), fmt.Sprintf("Cat %d", i), ""); err != nil {
			t.Fatal(err)
		}
	}

	resp := env.get(t, categoriesPath+"?page=2&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[categoryPage](t, resp)
	if body.Meta.Page != 2 || body.Meta.PerPage != 2 || body.Meta.Total != 5 || body.Meta.TotalPages != 3 {
		t.Fatalf("meta = %+v", body.Meta)
	}
	if len(body.Data.Categories) != 2 || body.Data.Categories[0].Name != "Cat 3" || body.Data.Categories[1].Name != "Cat 2" {
		t.Fatalf("page 2 = %+v, want Cat 3 then Cat 2", body.Data.Categories)
	}
	for _, cat := range body.Data.Categories {
		if cat.ID <= 0 || cat.CreatedAt == "" || cat.UpdatedAt == "" {
			t.Fatalf("category %+v is missing id or timestamps", cat)
		}
	}
}

func TestListCategoriesValidatesPagination(t *testing.T) {
	env, _ := categoriesEnv(t)
	for _, query := range []string{
		"?page=0", "?page=abc", "?per_page=0", "?per_page=101", "?per_page=x", "?page=1&page=2", "?nope=1",
	} {
		resp := env.get(t, categoriesPath+query)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", query, resp.StatusCode)
			continue
		}
		if code := decode[errorEnvelope](t, resp).Error.Code; code != gateway.CodeInvalidInput {
			t.Errorf("%s code = %q", query, code)
		}
	}
}

func TestListCategoriesFiltersByQuery(t *testing.T) {
	env, store := categoriesEnv(t)
	for _, c := range []struct{ name, desc string }{
		{"Technology", "gadgets"},
		{"News", "technology desk"},
		{"Music", ""},
	} {
		if _, err := store.CreateCategory(t.Context(), c.name, c.desc); err != nil {
			t.Fatal(err)
		}
	}

	body := decode[categoryPage](t, env.get(t, categoriesPath+"?q=techn"))
	if body.Meta.Total != 2 || len(body.Data.Categories) != 2 {
		t.Fatalf("total = %d, categories = %+v", body.Meta.Total, body.Data.Categories)
	}
}

func TestGetCategory(t *testing.T) {
	env, store := categoriesEnv(t)
	created, err := store.CreateCategory(t.Context(), "Technology", "about")
	if err != nil {
		t.Fatal(err)
	}

	resp := env.get(t, fmt.Sprintf("%s/%d", categoriesPath, created.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[categoryOne](t, resp)
	if body.Data.Category.ID != created.ID || body.Data.Category.Name != "Technology" ||
		body.Data.Category.Description != "about" {
		t.Fatalf("category = %+v", body.Data.Category)
	}
}

func TestGetCategoryNotFoundAndInvalidID(t *testing.T) {
	env, _ := categoriesEnv(t)

	resp := env.get(t, categoriesPath+"/999")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := decode[errorEnvelope](t, resp).Error.Code; code != gateway.CodeNotFound {
		t.Fatalf("code = %q", code)
	}

	for _, id := range []string{"abc", "0", "-1", "1.5"} {
		resp := env.get(t, categoriesPath+"/"+id)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("id %q = %d, want 400", id, resp.StatusCode)
		}
	}
}

func TestCreateCategory(t *testing.T) {
	env, store := categoriesEnv(t)
	resp := env.send(t, defaultCaller, http.MethodPost, categoriesPath, `{"name":"  Technology ","description":" Technology channels "}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[categoryOne](t, resp)
	cat := body.Data.Category
	if cat.ID <= 0 || cat.Name != "Technology" || cat.Description != "Technology channels" {
		t.Fatalf("category = %+v", cat)
	}
	if cat.CreatedAt == "" || cat.UpdatedAt == "" {
		t.Fatalf("timestamps missing: %+v", cat)
	}

	// No assignments exist after a create.
	stored, ok, err := store.GetCategory(t.Context(), cat.ID)
	if err != nil || !ok || stored.Name != "Technology" {
		t.Fatalf("stored = %+v ok=%v err=%v", stored, ok, err)
	}
}

func TestCreateCategoryAcceptsAnEmptyBody(t *testing.T) {
	env, _ := categoriesEnv(t)
	resp := env.send(t, defaultCaller, http.MethodPost, categoriesPath, `{"name":"News"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if cat := decode[categoryOne](t, resp).Data.Category; cat.Description != "" {
		t.Fatalf("description = %q, want empty", cat.Description)
	}
}

func TestCreateCategoryValidation(t *testing.T) {
	env, _ := categoriesEnv(t)
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"missing name", `{"description":"x"}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"empty name", `{"name":""}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"blank name", `{"name":"   "}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"malformed json", `{"name":`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"unknown field", `{"name":"x","colour":"red"}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"trailing data", `{"name":"x"}{}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"wrong type", `{"name":42}`, http.StatusBadRequest, gateway.CodeInvalidInput},
	}
	for _, tc := range cases {
		resp := env.send(t, defaultCaller, http.MethodPost, categoriesPath, tc.body)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
			continue
		}
		if code := decode[errorEnvelope](t, resp).Error.Code; code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, code, tc.code)
		}
	}
}

func TestCreateCategoryRequiresJSONContentType(t *testing.T) {
	env, _ := categoriesEnv(t)
	req, _ := http.NewRequest(http.MethodPost, categoriesPath, strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+env.session)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestCreateCategoryRejectsADuplicateName(t *testing.T) {
	env, _ := categoriesEnv(t)
	if resp := env.send(t, defaultCaller, http.MethodPost, categoriesPath, `{"name":"Technology"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create = %d", resp.StatusCode)
	}
	resp := env.send(t, defaultCaller, http.MethodPost, categoriesPath, `{"name":" technology "}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if code := decode[errorEnvelope](t, resp).Error.Code; code != gateway.CodeConflict {
		t.Fatalf("code = %q", code)
	}
}

func (e *accountEnv) grantedCategory(t *testing.T, store *channelstore.Store, name string) channelstore.Category {
	t.Helper()
	cat, err := store.CreateCategory(t.Context(), name, "")
	if err != nil {
		t.Fatal(err)
	}
	e.grantCategory(t, cat.ID)
	return cat
}

func TestUpdateCategory(t *testing.T) {
	env, store := categoriesEnv(t)
	created := env.grantedCategory(t, store, "Technology")

	resp := env.send(t, defaultCaller, http.MethodPut, fmt.Sprintf("%s/%d", categoriesPath, created.ID),
		`{"name":"Gadgets","description":"new"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	cat := decode[categoryOne](t, resp).Data.Category
	if cat.ID != created.ID || cat.Name != "Gadgets" || cat.Description != "new" {
		t.Fatalf("category = %+v", cat)
	}
	stored, _, _ := store.GetCategory(t.Context(), created.ID)
	if stored.Name != "Gadgets" {
		t.Fatalf("store still has %q", stored.Name)
	}
}

func TestUpdateCategoryErrors(t *testing.T) {
	env, store := categoriesEnv(t)
	created := env.grantedCategory(t, store, "Technology")
	other := env.grantedCategory(t, store, "News")

	cases := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{"missing category", fmt.Sprintf("%s/%d", categoriesPath, created.ID+100), `{"name":"x","description":""}`, http.StatusNotFound, gateway.CodeNotFound},
		{"invalid id", categoriesPath + "/abc", `{"name":"x"}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"missing name", fmt.Sprintf("%s/%d", categoriesPath, other.ID), `{"description":"x"}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"blank name", fmt.Sprintf("%s/%d", categoriesPath, other.ID), `{"name":" "}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"missing description", fmt.Sprintf("%s/%d", categoriesPath, other.ID), `{"name":"x"}`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"malformed json", fmt.Sprintf("%s/%d", categoriesPath, other.ID), `{`, http.StatusBadRequest, gateway.CodeInvalidInput},
		{"duplicate name", fmt.Sprintf("%s/%d", categoriesPath, other.ID), `{"name":"technology","description":""}`, http.StatusConflict, gateway.CodeConflict},
	}
	for _, tc := range cases {
		resp := env.send(t, defaultCaller, http.MethodPut, tc.path, tc.body)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
			continue
		}
		if code := decode[errorEnvelope](t, resp).Error.Code; code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, code, tc.code)
		}
	}
}

func TestDeleteCategory(t *testing.T) {
	env, store := categoriesEnv(t)
	created := env.grantedCategory(t, store, "Technology")

	resp := env.send(t, defaultCaller, http.MethodDelete, fmt.Sprintf("%s/%d", categoriesPath, created.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Deleted bool  `json:"deleted"`
			ID      int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !body.Data.Deleted || body.Data.ID != created.ID {
		t.Fatalf("data = %+v", body.Data)
	}

	if _, ok, err := store.GetCategory(t.Context(), created.ID); err != nil || ok {
		t.Fatalf("category survived: ok=%v err=%v", ok, err)
	}

	again := env.send(t, defaultCaller, http.MethodDelete, fmt.Sprintf("%s/%d", categoriesPath, created.ID), "")
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", again.StatusCode)
	}
	invalid := env.send(t, defaultCaller, http.MethodDelete, categoriesPath+"/abc", "")
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid id = %d, want 400", invalid.StatusCode)
	}
}

func TestDeleteCategoryKeepsChannelAssignmentsOfOtherCategories(t *testing.T) {
	env, store := categoriesEnv(t)
	ctx := t.Context()
	keep := env.grantedCategory(t, store, "Keep")
	drop := env.grantedCategory(t, store, "Drop")
	accountID := env.testEnv.account["official"].ID
	for _, id := range []int64{keep.ID, drop.ID} {
		if _, err := store.AssignChannel(ctx, accountID, "500", id); err != nil {
			t.Fatal(err)
		}
	}

	if resp := env.send(t, defaultCaller, http.MethodDelete, fmt.Sprintf("%s/%d", categoriesPath, drop.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	counts, err := store.AssignmentCounts(ctx, accountID, "500")
	if err != nil {
		t.Fatal(err)
	}
	if counts[drop.ID] != 0 || counts[keep.ID] != 1 {
		t.Fatalf("assignments = %v", counts)
	}
}

func TestCategoryRoutesRejectUnsupportedMethods(t *testing.T) {
	env, store := categoriesEnv(t)
	created, _ := store.CreateCategory(t.Context(), "Technology", "")
	one := fmt.Sprintf("%s/%d", categoriesPath, created.ID)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, categoriesPath},
		{http.MethodDelete, categoriesPath},
		{http.MethodPost, one},
	} {
		resp := env.send(t, defaultCaller, tc.method, tc.path, "")
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	if _, ok, _ := store.GetCategory(t.Context(), created.ID); !ok {
		t.Fatal("an unsupported method deleted the category")
	}

	// A path deeper than one id is not a category route at all.
	if resp := env.send(t, defaultCaller, http.MethodGet, categoriesPath+"/1/extra", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deeper path = %d, want 404", resp.StatusCode)
	}
}

func TestCategoryRoutesRequireAValidSession(t *testing.T) {
	env, _ := categoriesEnv(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, categoriesPath, ""},
		{http.MethodPost, categoriesPath, `{"name":"x"}`},
		{http.MethodDelete, categoriesPath + "/1", ""},
	} {
		for name, token := range map[string]string{"anonymous": "", "garbage": "not-a-token"} {
			resp := env.send(t, token, tc.method, tc.path, tc.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s %s = %d, want 401", name, tc.method, tc.path, resp.StatusCode)
			}
		}
	}
}

func TestCategoryRoutesNeedNoWorker(t *testing.T) {
	// The local category API must work while every Telegram client is down: it
	// answers from SQLite and never calls a worker.
	worker := readyWorker()
	env := newAccountEnv(t, registry{"official": worker})
	admin := env.account(t, "admin", channelstore.PermissionCategoriesCreate)

	if resp := env.send(t, admin.token, http.MethodPost, categoriesPath, `{"name":"Technology"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	if worker.callCount() != 0 {
		t.Fatalf("worker calls = %d, want none", worker.callCount())
	}
}
