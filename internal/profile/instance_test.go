package profile_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"
	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/profile"
)

// Test API hashes: obviously fake, never real credentials.
const (
	officialHash = "aaaaaaaabbbbbbbbccccccccdddddddd"
	customHash   = "11111111222222223333333344444444"
)

func customInstance(t *testing.T) appconfig.Client {
	t.Helper()
	dir := t.TempDir()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "cat.pem")
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&k.PublicKey),
	})
	if err := os.WriteFile(keyPath, pemData, 0o600); err != nil {
		t.Fatal(err)
	}
	return appconfig.Client{
		Key:           "the-cat",
		Type:          appconfig.Custom,
		Enabled:       true,
		AppID:         22222,
		APIHash:       customHash,
		Phone:         "+15559990000",
		SessionPath:   filepath.Join(dir, "sessions", "the-cat.json"),
		RPCSocketPath: filepath.Join(dir, "run", "the-cat.sock"),
		Server: &appconfig.Server{
			Host:             "mt.backend.test",
			Port:             8443,
			DCID:             2,
			RSAPublicKeyPath: keyPath,
			Transport:        "intermediate",
			Routing:          "single-dc",
		},
	}
}

func TestFromClientOfficialUsesLibraryDefaults(t *testing.T) {
	dir := t.TempDir()
	cl := appconfig.Client{
		Key:           "official",
		Type:          appconfig.Official,
		AppID:         11111,
		APIHash:       officialHash,
		SessionPath:   filepath.Join(dir, "sessions", "official.json"),
		RPCSocketPath: filepath.Join(dir, "run", "official.sock"),
	}
	spec, err := profile.FromClient(cl)
	if err != nil {
		t.Fatalf("FromClient: %v", err)
	}
	if spec.Kind != profile.Official {
		t.Errorf("kind = %q", spec.Kind)
	}
	if spec.AppID != 11111 || spec.AppHash != officialHash {
		t.Errorf("credentials = %d", spec.AppID)
	}
	if spec.SessionPath != cl.SessionPath {
		t.Errorf("session = %q", spec.SessionPath)
	}

	opt := spec.Options(nil, nil)
	if opt.DC != 0 || len(opt.DCList.Options) != 0 || opt.Resolver != nil || len(opt.PublicKeys) != 0 {
		t.Fatalf("official must leave gotd's defaults in place: DC=%d list=%d resolver=%v keys=%d",
			opt.DC, len(opt.DCList.Options), opt.Resolver, len(opt.PublicKeys))
	}
}

func TestFromClientCustomSetsEverythingExplicitly(t *testing.T) {
	cl := customInstance(t)
	spec, err := profile.FromClient(cl)
	if err != nil {
		t.Fatalf("FromClient: %v", err)
	}
	if spec.Kind != profile.Custom {
		t.Fatalf("kind = %q", spec.Kind)
	}
	if spec.DC != 2 || spec.Endpoint != "mt.backend.test:8443" {
		t.Errorf("dc/endpoint = %d/%q", spec.DC, spec.Endpoint)
	}
	opt := spec.Options(nil, nil)
	if opt.DC != 2 {
		t.Errorf("options DC = %d", opt.DC)
	}
	if len(opt.DCList.Options) != 1 || opt.DCList.Options[0].IPAddress != "mt.backend.test" {
		t.Errorf("dc list = %+v", opt.DCList.Options)
	}
	if opt.Resolver == nil || len(opt.PublicKeys) != 1 {
		t.Errorf("resolver=%v keys=%d", opt.Resolver, len(opt.PublicKeys))
	}
	if opt.DCList.Options[0].IPAddress == "149.154.167.51" {
		t.Error("the custom profile must never resolve to official Telegram")
	}
}

func TestFromClientCustomRejectsASessionFromAnotherDC(t *testing.T) {
	cl := customInstance(t)
	if err := os.MkdirAll(filepath.Dir(cl.SessionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		Version int
		Data    session.Data
	}{Version: 1, Data: session.Data{DC: 4, AuthKey: []byte{1}, AuthKeyID: []byte{2}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cl.SessionPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = profile.FromClient(cl)
	if err == nil {
		t.Fatal("a session saved for another DC must be rejected")
	}
	if !strings.Contains(err.Error(), "DC 4") {
		t.Errorf("error should name the session's DC: %v", err)
	}
}

func TestFromClientValidatesItsInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*appconfig.Client)
		want   string
	}{
		{"no key", func(c *appconfig.Client) { c.Key = "" }, "key"},
		{"no app id", func(c *appconfig.Client) { c.AppID = 0 }, "app_id"},
		{"no hash", func(c *appconfig.Client) { c.APIHash = "" }, "app_hash"},
		{"no session path", func(c *appconfig.Client) { c.SessionPath = "" }, "session"},
		{"unknown type", func(c *appconfig.Client) { c.Type = "proxy" }, "type"},
		{"custom without server", func(c *appconfig.Client) { c.Server = nil }, "server"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := customInstance(t)
			tc.mutate(&cl)
			_, err := profile.FromClient(cl)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does less than mention %q", err, tc.want)
			}
		})
	}
}

func TestFromClientInstancesAreIsolated(t *testing.T) {
	a := customInstance(t)
	b := customInstance(t)
	b.Key = "second-cat"
	b.Server.Host = "other.backend.test"

	specA, err := profile.FromClient(a)
	if err != nil {
		t.Fatal(err)
	}
	specB, err := profile.FromClient(b)
	if err != nil {
		t.Fatal(err)
	}
	if specA.SessionPath == specB.SessionPath {
		t.Fatal("two instances must not share a session path")
	}
	if specA.Endpoint == specB.Endpoint {
		t.Fatal("two instances must not share an endpoint")
	}
	if specA.NewClient(nil, nil) == specB.NewClient(nil, nil) {
		t.Fatal("clients must be separate instances")
	}
}
