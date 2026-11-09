package webhooktest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/webhooktest"
)

// Saving is opt-in and every saved file is evidence about one request, so it is
// written once, never overwritten, and kept where nobody will commit it.

var storeNow = time.Date(2026, 11, 8, 10, 30, 15, 123000000, time.UTC)

func sampleRecord(auth webhooktest.AuthType, method string, valid bool) webhooktest.Record {
	return webhooktest.Record{
		Route:    "/webhook/" + string(auth),
		AuthType: auth,
		Received: webhooktest.RequestRecord{
			Method: method,
			Path:   "/webhook/" + string(auth),
			URI:    "/webhook/" + string(auth) + "?source=telegram",
			Query:  map[string][]string{"source": {"telegram"}},
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			BodySize:     2,
			BodyEncoding: webhooktest.EncodingUTF8,
			BodyText:     "{}",
		},
		Authentication: webhooktest.Result{Type: auth, Valid: valid},
		Response: webhooktest.ResponseRecord{
			Status:     200,
			StatusText: "OK",
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			BodyText:   `{"success":true}`,
			BodySize:   17,
		},
	}
}

func newStore(t *testing.T, dir string) *webhooktest.Store {
	t.Helper()
	store, err := webhooktest.NewStore(dir, func() time.Time { return storeNow })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestSaveWritesOneFilePerRequestUnderTheOutputDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "temp", "webhook-responses")
	store := newStore(t, dir)

	path, err := store.Save(sampleRecord(webhooktest.AuthHMAC, "POST", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("path %q is not under %q", path, dir)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("path %q is not absolute", path)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("path %q is not a json file", path)
	}
}

func TestSaveNamesAFileAfterTheMomentModeAndMethod(t *testing.T) {
	store := newStore(t, t.TempDir())
	path, err := store.Save(sampleRecord(webhooktest.AuthBearer, "PATCH", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	name := filepath.Base(path)
	pattern := `^2026-11-08T10-30-15\.123Z_bearer_PATCH_[0-9a-f]{4}\.json$`
	if !regexp.MustCompile(pattern).MatchString(name) {
		t.Fatalf("name %q does not match %s", name, pattern)
	}
}

func TestSaveSanitizesEverythingItTakesFromTheRequest(t *testing.T) {
	store := newStore(t, t.TempDir())
	rec := sampleRecord(webhooktest.AuthHMAC, "POST", true)
	rec.AuthType = "../../etc/passwd"
	rec.Received.Method = "GET /../../evil"

	path, err := store.Save(rec)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	name := filepath.Base(path)
	if strings.ContainsAny(name, `/\`) {
		t.Fatalf("name %q kept a path separator", name)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Fatalf("path %q is not a json file", path)
	}
}

func TestSaveNeverOverwritesAPreviousRecord(t *testing.T) {
	store := newStore(t, t.TempDir())

	first, err := store.Save(sampleRecord(webhooktest.AuthHMAC, "POST", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := store.Save(sampleRecord(webhooktest.AuthHMAC, "POST", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if first == second {
		t.Fatalf("two records were written to %q", first)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

func TestSaveWritesARecordThatMatchesTheResponseTheHandlerGenerated(t *testing.T) {
	store := newStore(t, t.TempDir())
	rec := sampleRecord(webhooktest.AuthHeader, "PUT", false)
	rec.Authentication.Code = webhooktest.CodeInvalidHeader
	rec.Authentication.Message = "the X-API-Key header is not the configured secret"
	rec.Response.Status = 401
	rec.Response.StatusText = "Unauthorized"
	rec.Response.BodyText = `{"success":false}`
	rec.Response.BodySize = 17

	path, err := store.Save(rec)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got webhooktest.Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the saved record is not valid JSON: %v\n%s", err, raw)
	}
	if got.Response.BodyText != rec.Response.BodyText {
		t.Fatalf("saved response body = %q, want %q", got.Response.BodyText, rec.Response.BodyText)
	}
	if got.Response.Status != 401 || got.Response.StatusText != "Unauthorized" {
		t.Fatalf("saved response = %d %s", got.Response.Status, got.Response.StatusText)
	}
	if got.AuthType != webhooktest.AuthHeader || got.Authentication.Valid {
		t.Fatalf("saved authentication = %+v", got.Authentication)
	}
	if got.Authentication.Code != webhooktest.CodeInvalidHeader {
		t.Fatalf("saved error code = %q", got.Authentication.Code)
	}
	if got.Route != "/webhook/header" {
		t.Fatalf("saved route = %q", got.Route)
	}
	if got.SavedAt.IsZero() {
		t.Fatal("the saved record carries no timestamp")
	}
}

func TestSaveMarksAHeadResponseAsHavingNoBody(t *testing.T) {
	store := newStore(t, t.TempDir())
	rec := sampleRecord(webhooktest.AuthNone, "HEAD", true)
	rec.Response.BodyText = ""
	rec.Response.BodySize = 0
	rec.Response.BodyOmitted = true

	path, err := store.Save(rec)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got webhooktest.Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the saved record is not valid JSON: %v", err)
	}
	if !got.Response.BodyOmitted {
		t.Fatal("a HEAD response does not record that its body was omitted")
	}
	if got.Response.BodyText != "" {
		t.Fatalf("BodyText = %q, want it empty for HEAD", got.Response.BodyText)
	}
}

func TestSaveKeepsTheFileAndDirectoryToTheOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "responses")
	store := newStore(t, dir)

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("the output directory is %o, want nothing for group or other", perm)
	}

	path, err := store.Save(sampleRecord(webhooktest.AuthNone, "GET", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("the saved file is %o, want nothing for group or other", perm)
	}
}

func TestSaveFailsLoudlyWhenTheDirectoryCannotBeWritten(t *testing.T) {
	// A file where a directory would have to go: the caller must be told a
	// record was not kept, never left believing it was.
	root := t.TempDir()
	blocker := filepath.Join(root, "responses")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := webhooktest.NewStore(filepath.Join(blocker, "nested"),
		func() time.Time { return storeNow })
	if err == nil {
		t.Fatal("NewStore accepted a directory that cannot be created")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Fatalf("NewStore error %q does not name the path it refused", err)
	}
}

func TestStoreKeepsTheDirectoryItWasGiven(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "saved")
	store := newStore(t, dir)
	if store.Dir() != dir {
		t.Fatalf("Dir() = %q, want %q", store.Dir(), dir)
	}
}
