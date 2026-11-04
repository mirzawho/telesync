package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// GET /api/v1/local/stats/dashboard answers one number per collection for the
// panel's dashboard, in one call instead of four list requests.
//
// Two rules hold on every field. A collection the caller may not see at all is
// null, never 0: "you are not allowed to know" and "there is nothing there"
// must not be told apart. A number that is returned counts exactly the rows the
// corresponding list route would paginate through for that caller — the same
// visible() rule every list uses.

const statsPath = "/api/v1/local/stats/dashboard"

// dashboardStats is the response's data.stats; a null field decodes as nil.
type dashboardStats struct {
	Categories     *int `json:"categories"`
	Channels       *int `json:"channels"`
	WordPressSites *int `json:"wordpress_sites"`
	Administrators *int `json:"administrators"`
}

func ptrInt(v int) *int { return &v }

func num(v *int) string {
	if v == nil {
		return "null"
	}
	return strconv.Itoa(*v)
}

func (s dashboardStats) String() string {
	return "categories=" + num(s.Categories) + " channels=" + num(s.Channels) +
		" wordpress_sites=" + num(s.WordPressSites) + " administrators=" + num(s.Administrators)
}

// statsOf reads data.stats from the dashboard answer.
func statsOf(t *testing.T, resp *http.Response) dashboardStats {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", statsPath, resp.StatusCode, bodyOf(resp))
	}
	var decoded struct {
		Data struct {
			Stats dashboardStats `json:"stats"`
		} `json:"data"`
		Meta json.RawMessage `json:"meta"`
	}
	raw := bodyOf(resp)
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode dashboard stats: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(decoded.Meta, &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if _, ok := meta["request_id"]; !ok {
		t.Errorf("meta has no request_id: %s", decoded.Meta)
	}
	// The envelope must carry every field: an omitted key and a null look the
	// same when the field is read loosely, so the shape is asserted here.
	var shape map[string]any
	if err := json.Unmarshal([]byte(raw), &shape); err == nil {
		if data, ok := shape["data"].(map[string]any); ok {
			if got, ok := data["stats"].(map[string]any); !ok {
				t.Errorf("data has no stats object: %s", raw)
			} else {
				for _, key := range []string{"categories", "channels", "wordpress_sites", "administrators"} {
					if _, ok := got[key]; !ok {
						t.Errorf("data.stats has no %q key: %s", key, raw)
					}
				}
			}
		}
	}
	return decoded.Data.Stats
}

// wantStats compares each field, null standing for "must not be shown".
func wantStats(t *testing.T, what string, got, want dashboardStats) {
	t.Helper()
	check := func(name string, g, w *int) {
		if (g == nil) != (w == nil) {
			t.Errorf("%s: %s = %s, want %s", what, name, num(g), num(w))
			return
		}
		if g != nil && *g != *w {
			t.Errorf("%s: %s = %s, want %s", what, name, num(g), num(w))
		}
	}
	check("categories", got.Categories, want.Categories)
	check("channels", got.Channels, want.Channels)
	check("wordpress_sites", got.WordPressSites, want.WordPressSites)
	check("administrators", got.Administrators, want.Administrators)
}

// assertListTotals checks each list route's meta.total — the number of rows the
// caller may view — against what the dashboard reported.
func assertListTotals(t *testing.T, env *accountEnv, token string, want map[string]int) {
	t.Helper()
	for path, wantTotal := range want {
		resp := env.send(t, token, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, bodyOf(resp))
		}
		var decoded struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal([]byte(bodyOf(resp)), &decoded); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if decoded.Meta.Total != wantTotal {
			t.Errorf("%s total = %d, want %d", path, decoded.Meta.Total, wantTotal)
		}
	}
}

// reportUserCount is how the tests count accounts: whatever an unfiltered
// user listing reports as its total.
func reportUserCount(t *testing.T, store *channelstore.Store) int {
	t.Helper()
	_, total, err := store.ListUsers(context.Background(), channelstore.UserFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	return total
}

// testSecretBox stands in for the deployment's encrypting box in tests. The
// key is a placeholder, never a credential for anything.
type testSecretBox struct{}

func (testSecretBox) Seal(plaintext string) (secrets.Box, error) {
	enc, err := secrets.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		return secrets.Box{}, err
	}
	return enc.Seal(plaintext)
}

func (testSecretBox) Open(box secrets.Box) (string, error) {
	enc, err := secrets.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		return "", err
	}
	return enc.Open(box)
}

// stubWPClient satisfies the client field so the WordPress side of the config
// is complete; the dashboard only lists sites and never calls it.
type stubWPClient struct{}

func (stubWPClient) Probe(context.Context, string, string, string) (wordpress.SiteInfo, error) {
	return wordpress.SiteInfo{}, nil
}

func (stubWPClient) CreatePost(context.Context, string, string, string, wordpress.PostInput) (wordpress.PostResult, error) {
	return wordpress.PostResult{}, nil
}

func (stubWPClient) UploadMedia(context.Context, string, string, string, string, string, []byte) (wordpress.MediaRef, error) {
	return wordpress.MediaRef{}, nil
}

func (stubWPClient) FindTag(context.Context, string, string, string, string) (wordpress.TagRef, bool, error) {
	return wordpress.TagRef{}, false, nil
}

func (stubWPClient) CreateTag(context.Context, string, string, string, string) (wordpress.TagRef, error) {
	return wordpress.TagRef{}, nil
}

func (stubWPClient) ListTerms(context.Context, string, string, string, string, string, int, int) (wordpress.TermPage, error) {
	return wordpress.TermPage{Terms: []wordpress.TermRef{}}, nil
}

// withWordPressConfig rebuilds the handler with the WordPress side configured
// the way the daemon wires it: the site store, a working seal and a client to
// talk to the site with. Without those three the WordPress routes answer 503,
// and the dashboard must answer the same way.
func (e *accountEnv) withWordPressConfig(t *testing.T) {
	t.Helper()
	cfg := e.handlerConfig()
	cfg.WordPress = e.store
	cfg.Secrets = testSecretBox{}
	cfg.WordPressClient = stubWPClient{}
	e.useConfig(cfg)
}

// --- the counts, scoped to what each caller may see -------------------------

func TestDashboardStatsCountOnlyWhatEachCallerMaySee(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	ctx := context.Background()
	catA, err := env.store.CreateCategory(ctx, "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateCategory(ctx, "Beta", ""); err != nil {
		t.Fatal(err)
	}
	chA := env.channelRow(t, "official", 4001)
	env.channelRow(t, "official", 4002)
	// Two accounts besides the default administrator.
	limited := env.account(t, "dash-limited")
	env.account(t, "dash-reader")
	env.grantCategories(t, limited, allow(catA.ID))
	env.grantChannels(t, limited, allow(chA.ID))

	wantAccounts := reportUserCount(t, env.store)

	// The administrator holds every global view and no deny, so each count is
	// the whole collection.
	got := statsOf(t, env.send(t, env.defaults.token, http.MethodGet, statsPath, ""))
	wantStats(t, "administrator with every view", got, dashboardStats{
		Categories: ptrInt(2), Channels: ptrInt(2), Administrators: ptrInt(wantAccounts),
	})

	// The limited account holds exactly one allow per collection: the number is
	// 1, not 2, and administrators is null without a users permission.
	got = statsOf(t, env.send(t, limited.token, http.MethodGet, statsPath, ""))
	wantStats(t, "caller with one allow per collection", got, dashboardStats{
		Categories: ptrInt(1), Channels: ptrInt(1), Administrators: nil,
	})

	// Each count equals what the matching list route paginates through for the
	// same caller. The site list needs the WordPress side to be configured, so
	// it is asserted in the WordPress test below.
	assertListTotals(t, env, limited.token, map[string]int{
		"/api/v1/local/categories": 1,
		"/api/v1/local/channels":   1,
	})
	assertListTotals(t, env, env.defaults.token, map[string]int{
		"/api/v1/local/categories": 2,
		"/api/v1/local/channels":   2,
	})
}

// A caller with the global view and an explicit deny is counted minus the
// denied rows — the deny overrides the global view here exactly as it does in
// a list, and the number must never exceed what the list would return.
func TestDashboardStatsSubtractExplicitDeniesFromAGlobalView(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	ctx := context.Background()
	catA, err := env.store.CreateCategory(ctx, "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	catB, err := env.store.CreateCategory(ctx, "Beta", "")
	if err != nil {
		t.Fatal(err)
	}
	chA := env.channelRow(t, "official", 4101)
	chB := env.channelRow(t, "official", 4102)

	// The default caller holds every global view; deny one record of each kind.
	perms, err := env.store.PermissionsOf(ctx, env.defaults.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	perms.Categories = append(perms.Categories,
		channelstore.ResourcePermissions{ResourceID: catB.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
		}})
	perms.Channels = append(perms.Channels,
		channelstore.ResourcePermissions{ResourceID: chB.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
		}})
	if err := env.store.ReplacePermissions(ctx, env.defaults.user.ID, perms); err != nil {
		t.Fatal(err)
	}

	got := statsOf(t, env.send(t, env.defaults.token, http.MethodGet, statsPath, ""))
	wantAccounts := reportUserCount(t, env.store)
	wantStats(t, "global view with one deny per kind", got, dashboardStats{
		Categories: ptrInt(1), Channels: ptrInt(1), Administrators: ptrInt(wantAccounts),
	})
	assertListTotals(t, env, env.defaults.token, map[string]int{
		"/api/v1/local/categories": 1,
		"/api/v1/local/channels":   1,
	})
	_ = catA
	_ = chA
}

// --- what the caller may not see at all ------------------------------------

func TestDashboardStatsAreNullForCollectionsTheCallerCannotSee(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	ctx := context.Background()
	if _, err := env.store.CreateCategory(ctx, "Alpha", ""); err != nil {
		t.Fatal(err)
	}
	env.channelRow(t, "official", 4011)
	who := env.account(t, "dash-none",
		channelstore.PermissionCategoriesCreate, channelstore.PermissionChannelsCreate)

	// No global view and no per-row allow for any collection: everything this
	// caller isn't allowed to see is null rather than 0.
	got := statsOf(t, env.send(t, who.token, http.MethodGet, statsPath, ""))
	if got.Categories != nil || got.Channels != nil || got.Administrators != nil {
		t.Errorf("caller who may see nothing: categories=%s channels=%s administrators=%s, want all null",
			num(got.Categories), num(got.Channels), num(got.Administrators))
	}
	// A create right alone admits nothing: the list is still empty for them,
	// and the dashboard must not show a number either.
	assertListTotals(t, env, who.token, map[string]int{"/api/v1/local/categories": 0})
}

// --- administrators ---------------------------------------------------------

func TestDashboardAdministratorsCountNeedsAGlobalUserRight(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	for i, tc := range []struct {
		name    string
		globals []channelstore.GlobalPermission
		want    *int
	}{
		{"users.view", []channelstore.GlobalPermission{channelstore.PermissionUsersView}, nil},
		{"users.manage", []channelstore.GlobalPermission{channelstore.PermissionUsersManage}, nil},
		{"a non-user right", []channelstore.GlobalPermission{channelstore.PermissionChannelsView}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			who := env.account(t, "dash-admin-"+strconv.Itoa(i), tc.globals...)
			// Counted after this account exists, so the number includes it.
			want := ptrInt(reportUserCount(t, env.store))
			if tc.globals[0] == channelstore.PermissionChannelsView {
				want = nil
			}
			got := statsOf(t, env.send(t, who.token, http.MethodGet, statsPath, ""))
			if (got.Administrators == nil) != (want == nil) {
				t.Fatalf("administrators = %s, want %s", num(got.Administrators), num(want))
			}
			if want != nil && *got.Administrators != *want {
				t.Errorf("administrators = %s, want %s", num(got.Administrators), num(want))
			}
		})
	}
}

// --- authentication, methods, unconfigured stores ---------------------------

func TestDashboardStatsRequireAuthAndGetOnly(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	if resp := env.sendAnon(t, http.MethodGet, statsPath); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401", resp.StatusCode)
	}
	if resp := env.sendAnon(t, http.MethodPost, statsPath); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous POST = %d, want 401: a credential is checked first", resp.StatusCode)
	}
	if resp := env.send(t, env.defaults.token, http.MethodPost, statsPath, "{}"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
	// A path below the collection is not the collection: only GET is served.
	if resp := env.send(t, env.defaults.token, http.MethodGet, statsPath+"/x", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET %s/x = %d, want 405", statsPath+"/x", resp.StatusCode)
	}
}

func TestDashboardStatsAre503WhenAStoreIsNotConfigured(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	cfg := env.handlerConfig()
	cfg.Users = nil
	cfg.Channels = nil
	cfg.Categories = nil
	cfg.WordPress = nil
	env.useConfig(cfg)

	resp := env.send(t, env.defaults.token, http.MethodGet, statsPath, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503: %s", statsPath, resp.StatusCode, bodyOf(resp))
	}
	var decoded struct {
		Error gateway.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != gateway.CodeWorkerUnavail {
		t.Errorf("error.code = %q, want %q", decoded.Error.Code, gateway.CodeWorkerUnavail)
	}
}

// --- a store failure --------------------------------------------------------

func TestDashboardStatsReportAStoreFailureAs500(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	cfg := env.handlerConfig()
	cfg.Categories = brokenCategoryStore{}
	env.useConfig(cfg)

	resp := env.send(t, env.defaults.token, http.MethodGet, statsPath, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET %s = %d, want 500: %s", statsPath, resp.StatusCode, bodyOf(resp))
	}
	var decoded struct {
		Error gateway.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != gateway.CodeStoreError {
		t.Errorf("error.code = %q, want %q: a broken store is not a count of 0",
			decoded.Error.Code, gateway.CodeStoreError)
	}
}

// brokenCategoryStore fails every listing; the embed satisfies the rest of the
// interface, which no dashboard request reaches.
type brokenCategoryStore struct{ gateway.CategoryStore }

func (brokenCategoryStore) ListCategories(context.Context, channelstore.ListQuery) ([]channelstore.Category, int, error) {
	return nil, 0, context.Canceled
}

func (brokenCategoryStore) GetCategory(context.Context, int64) (channelstore.Category, bool, error) {
	return channelstore.Category{}, false, nil
}

func (brokenCategoryStore) ListCategoriesByIDs(context.Context, []int64) ([]channelstore.Category, error) {
	return nil, context.Canceled
}

// --- WordPress sites counted like the site list -----------------------------

func TestDashboardStatsCountVisibleWordPressSites(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	env.withWordPressConfig(t)
	box := testSecretBox{}
	for _, url := range []string{"https://stats-one.example.com", "https://stats-two.example.com"} {
		seal, err := box.Seal("app-password")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.store.CreateWordPressSite(context.Background(), channelstore.WordPressSite{
			URL: url, Title: "Site", Username: "editor", Credential: seal,
		}); err != nil {
			t.Fatal(err)
		}
	}
	sites, err := env.store.ListWordPressSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("seeded %d sites, want 2", len(sites))
	}

	// The administrator may list every site, so the number is all of them.
	got := statsOf(t, env.send(t, env.defaults.token, http.MethodGet, statsPath, ""))
	if num(got.WordPressSites) != "2" {
		t.Errorf("wordpress_sites (global view) = %s, want 2", num(got.WordPressSites))
	}

	// One grant: exactly the sites that caller may see, and only those. A grant
	// names the site and allows the view; a global "wordpress.view" would admit
	// every site instead, which is the case asserted just above.
	visitor := env.account(t, "dash-site-visitor")
	if err := env.store.ReplacePermissions(context.Background(), visitor.user.ID, channelstore.PermissionSet{
		Wordsites: []channelstore.ResourcePermissions{allow(sites[0].ID)},
	}); err != nil {
		t.Fatal(err)
	}
	got = statsOf(t, env.send(t, visitor.token, http.MethodGet, statsPath, ""))
	if num(got.WordPressSites) != "1" {
		t.Errorf("wordpress_sites (one site grant) = %s, want 1", num(got.WordPressSites))
	}
	assertListTotals(t, env, visitor.token, map[string]int{"/api/v1/local/wordpress": 1})

	// No site right at all: null, not an empty-set 0.
	none := env.account(t, "dash-no-site")
	if err := env.store.ReplacePermissions(context.Background(), none.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{},
	}); err != nil {
		t.Fatal(err)
	}
	got = statsOf(t, env.send(t, none.token, http.MethodGet, statsPath, ""))
	if got.WordPressSites != nil {
		t.Errorf("wordpress_sites (no site right) = %s, want null", num(got.WordPressSites))
	}

	// The WordPress side of the configuration is missing here: the same 503
	// the site list answers, never a count over a store it cannot reach.
	cfg := env.handlerConfig()
	cfg.WordPressClient = nil
	cfg.Secrets = nil
	env.useConfig(cfg)
	resp := env.send(t, env.defaults.token, http.MethodGet, statsPath, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET %s without the WordPress side = %d, want 503: %s",
			statsPath, resp.StatusCode, bodyOf(resp))
	}
}
