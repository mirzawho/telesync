// Package appconfig loads the multi-instance configuration file.
//
// The file names clients, secrets and paths; it never holds secret values
// itself. Secrets come from environment variables named in the file, and
// diagnostics always print the variable name, never the value.
package appconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/secrets"
)

// Type identifies how a client connects.
type Type string

const (
	Official Type = "official"
	Custom   Type = "custom"
)

// Defaults for optional settings.
const (
	DefaultListenAddr      = "127.0.0.1:8080"
	DefaultRequestTimeout  = 30 * time.Second
	DefaultRestartInitial  = time.Second
	DefaultRestartMax      = 30 * time.Second
	DefaultStartupTimeout  = 60 * time.Second
	DefaultShutdownTimeout = 15 * time.Second
	DefaultMaxRestarts     = 10
	DefaultCustomPort      = 443
	DefaultCustomDC        = 2
	// DefaultReconnectSeconds is the retry interval when the configuration does
	// not set runtime.reconnect.
	DefaultReconnectSeconds = 10
	// maxReconnectSeconds bounds the retry interval: anything longer would look
	// like a dead client rather than a patient one.
	maxReconnectSeconds = 3600
	DefaultTransport    = "intermediate"
	DefaultRouting      = "single-dc"
)

// DefaultTokenTTL is how long an issued access token stays valid when the
// configuration does not say otherwise.
const DefaultTokenTTL = time.Hour

// MinJWTSecretLen is the shortest HS256 key accepted (the hash length).
const MinJWTSecretLen = 32

// MaxTokenTTL bounds a token's life.
const MaxTokenTTL = 24 * time.Hour

// JWTConfig is how access tokens are signed.
type JWTConfig struct {
	// SecretEnv is the name of the environment variable holding the HS256 key.
	// Empty disables user authentication, which is only allowed on loopback.
	SecretEnv string
	// Secret is the resolved key. Never printed, never marshalled.
	Secret string `json:"-"`
	// SecretInFile is true when the key came from api.jwt.secret rather than
	// from a variable.
	SecretInFile bool
	// TTL bounds a token's life.
	TTL time.Duration
}

// EmbeddedDocs is the DocsDir that means "the documentation built into the
// binary": the copy of docs/api the generator embedded. It is never a path,
// so no file can be named this.
const EmbeddedDocs = "(embedded)"

// APIConfig is the public HTTP gateway configuration.
type APIConfig struct {
	ListenAddr     string
	RequestTimeout time.Duration
	// AuthTokenEnv and AuthToken are the optional fixed operator token. It is
	// accepted only on a loopback listener.
	AuthTokenEnv string
	AuthToken    string `json:"-"`
	// ListenHost is the interface address from listen_addr.
	ListenHost string
	// PublicBaseURL is the externally reachable origin of this installation,
	// used to turn relative API paths (the media endpoint, above all) into
	// absolute URLs for webhook receivers. It is empty when the operator has
	// not declared one, and no route invents an address in that case.
	PublicBaseURL string
	// JWT is the user login configuration.
	JWT JWTConfig
	// WordPress is the WordPress site management configuration.
	WordPress WordPressConfig
	// DocsDir is the directory GET /docs renders. It is EmbeddedDocs unless
	// the configuration names another tree or one sits in the directory or a
	// parent (findDocsUpwards). A named directory must exist.
	DocsDir string
}

// WordPressConfig is how registered WordPress sites keep their credentials.
type WordPressConfig struct {
	// KeyEnv is the name of the variable holding the AES-256 key.
	KeyEnv string
	// KeyFile is the file holding the key, resolved against the
	// configuration's directory. It is the form a panel deployment uses: the
	// daemon and a re-run of `config validate` read the same key.
	KeyFile string
	// Key is the resolved 32-byte key. It is never printed or marshalled.
	Key []byte `json:"-"`
	// KeyInFile is true when the key came from api.wordpress.encryption_key.
	KeyInFile bool
}

// keyFromFile reads the WordPress key from path.
//
// The file holds the key as ParseKey accepts it (base64 or 32 raw bytes) and
// nothing else but whitespace; it is never printed. It must be a regular file,
// so a path that names a directory or a device is refused rather than read.
func keyFromFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return nil, fmt.Errorf("%w: encryption_key_path %q does not exist", secrets.ErrKey, path)
	case err != nil:
		return nil, fmt.Errorf("%w: encryption_key_path %q: %v", secrets.ErrKey, path, err)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%w: encryption_key_path %q is not a regular file", secrets.ErrKey, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: encryption_key_path %q: %v", secrets.ErrKey, path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("%w: encryption_key_path %q is an empty key", secrets.ErrKey, path)
	}
	key, err := secrets.ParseKey(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("encryption_key_path %q: %w", path, err)
	}
	return key, nil
}

// Encrypter is the credential seal for registered WordPress sites.
//
// It fails when no key is configured, so a deployment that registers a site
// without one gets an error rather than a credential stored in the clear.
func (w WordPressConfig) Encrypter() (*secrets.Encrypter, error) {
	if len(w.Key) == 0 {
		return nil, fmt.Errorf("%w: set api.wordpress.encryption_key_env (or api.wordpress.encryption_key) in the configuration",
			secrets.ErrKeyNotSet)
	}
	return secrets.NewEncrypter(w.Key)
}

// RuntimeConfig holds process-supervision settings plus the two private
// directories the daemon owns: the local channel database and the avatar cache.
type RuntimeConfig struct {
	RunDir              string
	SessionDir          string
	RestartInitialDelay time.Duration
	RestartMaxDelay     time.Duration
	StartupTimeout      time.Duration
	ShutdownTimeout     time.Duration
	MaxRestartAttempts  int
	// DataDir holds the local SQLite database of managed channels.
	DataDir string
	// CacheDir holds cached channel avatars. It must stay outside any served
	// directory; the gateway never serves files from it directly.
	CacheDir string
	// AvatarTTL is how long a cached avatar is served before a refresh.
	AvatarTTL time.Duration
	// PeerDir holds each worker's channel access-hash memory.
	PeerDir string
	// Reconnect is how many seconds a client waits before retrying a connection
	// that is down. It is a plain number in the file, because that is what the
	// operator thinks in ("1" = every second).
	Reconnect time.Duration
}

// Server is the custom backend's connection configuration.
type Server struct {
	Host             string
	Port             int
	DCID             int
	RSAPublicKeyPath string
	Transport        string
	Routing          string
	Obfuscated       bool
}

// Endpoint returns host:port.
func (s Server) Endpoint() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Client is one configured Telegram instance.
type Client struct {
	Key           string
	Type          Type
	Enabled       bool
	AppID         int
	AppHashEnv    string
	APIHash       string `json:"-"`
	PhoneEnv      string
	Phone         string `json:"-"`
	SessionPath   string
	RPCSocketPath string
	// PeerCachePath is this worker's private channel access-hash memory.
	// MTProto cannot address a channel by numeric id without it.
	PeerCachePath string
	Server        *Server
}

// Config is the whole configuration file.
type Config struct {
	API     APIConfig
	Runtime RuntimeConfig
	// Automation is the background automation subsystem: event ingestion, the
	// durable job queue, webhooks, workflows and AI polishing.
	Automation AutomationConfig
	// Send is direct message sending: messages a caller composed and files they
	// uploaded, accepted over HTTP and delivered in the background by the same
	// durable queue the automation subsystem runs on.
	Send    SendConfig
	Clients []Client
}

// Loopback reports whether the API listens on a loopback address only.
func (a APIConfig) Loopback() bool { return isLoopback(a.ListenHost) }

// OperatorToken is the fixed token the API accepts on a loopback listener.
// It is empty on a non-loopback listener, where it is refused at load time.
func (a APIConfig) OperatorToken() string {
	if !a.Loopback() {
		return ""
	}
	return a.AuthToken
}

// Find returns the client with the given key.
func (c Config) Find(key string) (Client, bool) {
	for _, cl := range c.Clients {
		if cl.Key == key {
			return cl, true
		}
	}
	return Client{}, false
}

// EnabledClients returns the clients that should run.
func (c Config) EnabledClients() []Client {
	out := make([]Client, 0, len(c.Clients))
	for _, cl := range c.Clients {
		if cl.Enabled {
			out = append(out, cl)
		}
	}
	return out
}

// Describe renders the configuration without secret values.
func (c Config) Describe() []string {
	out := []string{
		fmt.Sprintf("api.listen_addr=%s", c.API.ListenAddr),
		fmt.Sprintf("api.request_timeout=%s", c.API.RequestTimeout),
	}
	if c.API.DocsDir == EmbeddedDocs {
		out = append(out, "api.docs_dir=(embedded)")
	} else {
		out = append(out, fmt.Sprintf("api.docs_dir=%s", c.API.DocsDir))
	}
	if c.API.JWT.SecretInFile {
		out = append(out, fmt.Sprintf("api.jwt=%s from api.jwt.secret in the configuration file ttl=%s",
			MaskSecret(c.API.JWT.Secret), c.API.JWT.TTL))
	} else if c.API.JWT.SecretEnv != "" {
		out = append(out, fmt.Sprintf("api.jwt=%s via %s ttl=%s",
			MaskSecret(c.API.JWT.Secret), c.API.JWT.SecretEnv, c.API.JWT.TTL))
	} else {
		out = append(out, "api.jwt=none (login disabled)")
	}
	switch {
	case c.API.WordPress.KeyFile != "":
		out = append(out, fmt.Sprintf("api.wordpress=key from %s", c.API.WordPress.KeyFile))
	case c.API.WordPress.KeyInFile:
		out = append(out, "api.wordpress=key from api.wordpress.encryption_key in the configuration file")
	case c.API.WordPress.KeyEnv != "":
		out = append(out, fmt.Sprintf("api.wordpress=key via %s", c.API.WordPress.KeyEnv))
	default:
		out = append(out, "api.wordpress=disabled (no encryption key; WordPress site routes answer 503)")
	}
	if c.API.AuthTokenEnv != "" {
		out = append(out, fmt.Sprintf("api.operator_token=%s via %s (loopback only)",
			MaskSecret(c.API.AuthToken), c.API.AuthTokenEnv))
	} else {
		out = append(out, "api.operator_token=none")
	}
	out = append(out,
		fmt.Sprintf("runtime.run_dir=%s", c.Runtime.RunDir),
		fmt.Sprintf("runtime.session_dir=%s", c.Runtime.SessionDir),
		fmt.Sprintf("runtime.reconnect=%ds", int(c.Runtime.Reconnect.Seconds())),
		fmt.Sprintf("runtime.restart=%s..%s max_attempts=%d",
			c.Runtime.RestartInitialDelay, c.Runtime.RestartMaxDelay, c.Runtime.MaxRestartAttempts),
	)
	for _, cl := range c.Clients {
		out = append(out, fmt.Sprintf("client.%s.type=%s enabled=%t", cl.Key, cl.Type, cl.Enabled))
		hashSource := "app_hash=(literal)"
		if cl.AppHashEnv != "" {
			hashSource = "app_hash_env=" + cl.AppHashEnv
		}
		out = append(out, fmt.Sprintf("client.%s.app_id=%d %s", cl.Key, cl.AppID, hashSource))
		if cl.PhoneEnv != "" {
			out = append(out, fmt.Sprintf("client.%s.phone_env=%s", cl.Key, cl.PhoneEnv))
		}
		if cl.Phone != "" {
			out = append(out, fmt.Sprintf("client.%s.phone=%s", cl.Key, MaskPhone(cl.Phone)))
		}
		out = append(out,
			fmt.Sprintf("client.%s.session_path=%s", cl.Key, cl.SessionPath),
			fmt.Sprintf("client.%s.rpc_socket_path=%s", cl.Key, cl.RPCSocketPath),
		)
		if cl.Server != nil {
			out = append(out,
				fmt.Sprintf("client.%s.server=%s dc_id=%d", cl.Key, cl.Server.Endpoint(), cl.Server.DCID),
				fmt.Sprintf("client.%s.transport=%s routing=%s obfuscated=%t",
					cl.Key, cl.Server.Transport, cl.Server.Routing, cl.Server.Obfuscated),
				fmt.Sprintf("client.%s.rsa_public_key_path=%s", cl.Key, cl.Server.RSAPublicKeyPath),
			)
		}
	}
	return out
}

type rawConfig struct {
	API        rawAPI        `json:"api"`
	Runtime    rawRuntime    `json:"runtime"`
	Automation rawAutomation `json:"automation"`
	Send       rawSend       `json:"send"`
	Clients    []rawClient   `json:"clients"`
}

type rawAPI struct {
	ListenAddr     string          `json:"listen_addr"`
	RequestTimeout string          `json:"request_timeout"`
	AuthTokenEnv   string          `json:"auth_token_env"`
	PublicBaseURL  string          `json:"public_base_url"`
	JWT            rawJWTAPI       `json:"jwt"`
	WordPress      rawWordPressAPI `json:"wordpress"`
	DocsDir        string          `json:"docs_dir"`
}

type rawWordPressAPI struct {
	EncryptionKeyEnv  string  `json:"encryption_key_env"`
	EncryptionKeyPath string  `json:"encryption_key_path"`
	EncryptionKey     *string `json:"encryption_key"`
}

type rawJWTAPI struct {
	SecretEnv string `json:"secret_env"`
	// Secret is a pointer so "absent" and "present but empty" are different:
	// an empty literal is a mistake worth naming, an absent one means "use
	// secret_env or nothing".
	Secret   *string `json:"secret"`
	TokenTTL string  `json:"token_ttl"`
}

type rawRuntime struct {
	RunDir              string `json:"run_dir"`
	SessionDir          string `json:"session_dir"`
	DataDir             string `json:"data_dir"`
	CacheDir            string `json:"cache_dir"`
	AvatarTTL           string `json:"avatar_ttl"`
	PeerDir             string `json:"peer_dir"`
	Reconnect           *int   `json:"reconnect"`
	RestartInitialDelay string `json:"restart_initial_delay"`
	RestartMaxDelay     string `json:"restart_max_delay"`
	StartupTimeout      string `json:"startup_timeout"`
	ShutdownTimeout     string `json:"shutdown_timeout"`
	MaxRestartAttempts  *int   `json:"max_restart_attempts"`
}

type rawClient struct {
	Key           string     `json:"key"`
	Type          string     `json:"type"`
	Enabled       *bool      `json:"enabled"`
	AppID         int        `json:"app_id"`
	AppHash       string     `json:"app_hash"`
	AppHashEnv    string     `json:"app_hash_env"`
	Phone         string     `json:"phone"`
	PhoneEnv      string     `json:"phone_env"`
	SessionPath   string     `json:"session_path"`
	RPCSocketPath string     `json:"rpc_socket_path"`
	PeerCachePath string     `json:"peer_cache_path"`
	Server        *rawServer `json:"server"`
}

type rawServer struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	DCID             int    `json:"dc_id"`
	RSAPublicKeyPath string `json:"rsa_public_key_path"`
	Transport        string `json:"transport"`
	Routing          string `json:"routing"`
	Obfuscated       bool   `json:"obfuscated"`
}

// Load reads and validates the configuration file at path.
//
// Relative paths resolve against the directory holding the file, so a
// configuration can be moved as a unit.
func Load(path string, lookup func(string) string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("configuration file %q does not exist", path)
		}
		return Config{}, fmt.Errorf("read configuration file %q: %w", path, err)
	}

	var raw rawConfig
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("parse configuration file %q: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("parse configuration file %q: unexpected trailing data", path)
	}

	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration directory: %w", err)
	}
	return build(raw, baseDir, lookup)
}

func build(raw rawConfig, baseDir string, lookup func(string) string) (Config, error) {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	cfg := Config{}

	// --- API ---
	cfg.API.ListenAddr = strings.TrimSpace(raw.API.ListenAddr)
	if cfg.API.ListenAddr == "" {
		cfg.API.ListenAddr = DefaultListenAddr
	}
	listenHost := ""
	if host, port, err := net.SplitHostPort(cfg.API.ListenAddr); err != nil {
		fail("api.listen_addr %q is not host:port", cfg.API.ListenAddr)
	} else if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		fail("api.listen_addr %q has an invalid port", cfg.API.ListenAddr)
	} else {
		listenHost = host
		cfg.API.ListenHost = host
	}

	cfg.API.RequestTimeout = DefaultRequestTimeout
	if v := strings.TrimSpace(raw.API.RequestTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("api.request_timeout %q must be a positive duration such as 30s", v)
		} else {
			cfg.API.RequestTimeout = d
		}
	}
	cfg.API.AuthTokenEnv = strings.TrimSpace(raw.API.AuthTokenEnv)
	if cfg.API.AuthTokenEnv != "" {
		token := strings.TrimSpace(lookup(cfg.API.AuthTokenEnv))
		if token == "" {
			fail("api.auth_token_env names %s, which is not set", cfg.API.AuthTokenEnv)
		}
		cfg.API.AuthToken = token
	}

	// The rendered documentation is built in: GET /docs serves the copy of
	// docs/api the generator embedded (internal/docsite), so nothing has to be
	// configured or found on disk. A directory is named only to override it,
	// and then it must exist, so a typo is reported here rather than as an
	// empty site at runtime.
	cfg.API.DocsDir = EmbeddedDocs
	if v := strings.TrimSpace(raw.API.DocsDir); v != "" {
		cfg.API.DocsDir = resolvePath(baseDir, v, v)
		if _, err := os.Stat(cfg.API.DocsDir); err != nil {
			fail("api.docs_dir %q does not exist; point it at a documentation tree, for example docs/api in the repository: %v", cfg.API.DocsDir, err)
		}
	} else if dir := findDocsUpwards(baseDir); dir != "" {
		cfg.API.DocsDir = dir
	}

	// --- API: user login (JWT) ---
	cfg.API.JWT.TTL = DefaultTokenTTL
	cfg.API.JWT.SecretEnv = strings.TrimSpace(raw.API.JWT.SecretEnv)
	// The key is either the value of the named variable or a literal in the
	// file. The literal form makes the key readable by anyone who can read the
	// file, so it is allowed but called out on a listener other than loopback.
	cfg.API.JWT.SecretInFile = raw.API.JWT.Secret != nil
	literalSecret := ""
	if raw.API.JWT.Secret != nil {
		literalSecret = *raw.API.JWT.Secret
	}
	switch {
	case cfg.API.JWT.SecretEnv != "" && cfg.API.JWT.SecretInFile:
		fail("api.jwt: set either secret_env or secret, not both")
	case cfg.API.JWT.SecretInFile:
		if literalSecret = strings.TrimSpace(literalSecret); literalSecret == "" {
			fail("api.jwt.secret is empty; give it a random key of at least %d bytes (openssl rand -base64 48)",
				MinJWTSecretLen)
		} else if len(literalSecret) < MinJWTSecretLen {
			fail("api.jwt.secret is shorter than %d bytes; use a random key such as openssl rand -base64 48",
				MinJWTSecretLen)
		} else {
			cfg.API.JWT.Secret = literalSecret
		}
	case cfg.API.JWT.SecretEnv != "":
		secret := lookup(cfg.API.JWT.SecretEnv)
		if secret == "" {
			fail("api.jwt.secret_env names %s, which is not set", cfg.API.JWT.SecretEnv)
		} else if len(secret) < MinJWTSecretLen {
			fail("api.jwt.secret_env names %s, whose value is shorter than %d bytes; use a random key of at least %d bytes",
				cfg.API.JWT.SecretEnv, MinJWTSecretLen, MinJWTSecretLen)
		} else {
			cfg.API.JWT.Secret = secret
		}
	}
	if v := strings.TrimSpace(raw.API.JWT.TokenTTL); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil || d <= 0:
			fail("api.jwt.token_ttl %q must be a positive duration such as 1h", v)
		case d > MaxTokenTTL:
			fail("api.jwt.token_ttl %q must be at most %s", v, MaxTokenTTL)
		default:
			cfg.API.JWT.TTL = d
		}
	}

	// --- API: WordPress site credentials ---
	cfg.API.WordPress.KeyEnv = strings.TrimSpace(raw.API.WordPress.EncryptionKeyEnv)
	keyFilePath := strings.TrimSpace(raw.API.WordPress.EncryptionKeyPath)
	cfg.API.WordPress.KeyInFile = raw.API.WordPress.EncryptionKey != nil || keyFilePath != ""
	literalKey := ""
	if raw.API.WordPress.EncryptionKey != nil {
		literalKey = *raw.API.WordPress.EncryptionKey
	}
	// Exactly one of the three forms may be set; two would leave the operator
	// unsure which key the daemon uses.
	forms := 0
	for _, set := range []bool{cfg.API.WordPress.KeyEnv != "", keyFilePath != "", raw.API.WordPress.EncryptionKey != nil} {
		if set {
			forms++
		}
	}
	switch {
	case forms > 1:
		fail("api.wordpress: set only one of encryption_key_env, encryption_key_path or encryption_key, not both")
	case keyFilePath != "":
		cfg.API.WordPress.KeyFile = resolvePath(baseDir, keyFilePath, keyFilePath)
		if key, err := keyFromFile(cfg.API.WordPress.KeyFile); err != nil {
			fail("api.wordpress: %v", err)
		} else {
			cfg.API.WordPress.Key = key
		}
	case raw.API.WordPress.EncryptionKey != nil:
		if strings.TrimSpace(literalKey) == "" {
			fail("api.wordpress.encryption_key is empty; give it a base64 %d-byte key (openssl rand -base64 %d)",
				secrets.KeySize, secrets.KeySize)
		} else if key, err := secrets.ParseKey(literalKey); err != nil {
			fail("api.wordpress.encryption_key is not a valid key: %v", err)
		} else {
			cfg.API.WordPress.Key = key
		}
	case cfg.API.WordPress.KeyEnv != "":
		key, err := secrets.KeyFromEnv(cfg.API.WordPress.KeyEnv, lookup)
		if err != nil {
			fail("api.wordpress: %v", err)
		} else {
			cfg.API.WordPress.Key = key
		}
	}

	if !isLoopback(listenHost) {
		if cfg.API.JWT.Secret == "" {
			fail("api.listen_addr %q is not loopback; set api.jwt.secret_env (or api.jwt.secret) so logins work and the API requires a token", cfg.API.ListenAddr)
		}
		if cfg.API.AuthToken != "" {
			// The fixed token grants the entire API. It is shared out of band,
			// cannot be revoked per person, and is easy to leak; a non-loopback
			// listener must use per-user logins instead.
			fail("api.auth_token_env is only accepted on a loopback listener; remove it and set api.jwt.secret_env for a non-loopback API")
		}
	}

	// --- Runtime ---
	cfg.Runtime.RunDir = resolvePath(baseDir, raw.Runtime.RunDir, "run")
	cfg.Runtime.SessionDir = resolvePath(baseDir, raw.Runtime.SessionDir, filepath.Join("data", "sessions"))
	cfg.Runtime.DataDir = resolvePath(baseDir, raw.Runtime.DataDir, filepath.Join("data", "db"))
	cfg.Runtime.CacheDir = resolvePath(baseDir, raw.Runtime.CacheDir, filepath.Join("data", "cache", "avatars"))
	cfg.Runtime.PeerDir = resolvePath(baseDir, raw.Runtime.PeerDir, filepath.Join("data", "peers"))

	cfg.Runtime.Reconnect = time.Duration(DefaultReconnectSeconds) * time.Second
	if raw.Runtime.Reconnect != nil {
		switch {
		case *raw.Runtime.Reconnect < 1:
			fail("runtime.reconnect must be a whole number of seconds of at least 1")
		case *raw.Runtime.Reconnect > maxReconnectSeconds:
			fail("runtime.reconnect must be at most %d seconds", maxReconnectSeconds)
		default:
			cfg.Runtime.Reconnect = time.Duration(*raw.Runtime.Reconnect) * time.Second
		}
	}

	cfg.Runtime.AvatarTTL = 24 * time.Hour
	if v := strings.TrimSpace(raw.Runtime.AvatarTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("runtime.avatar_ttl %q must be a positive duration such as 24h", v)
		} else {
			cfg.Runtime.AvatarTTL = d
		}
	}
	// The cache and the database are private state. Putting either inside the
	// run directory (sockets) or beside a served tree is a configuration
	// mistake worth refusing rather than silently honoring.
	// The cache and the database are private state; the documentation is
	// published. Keeping any of them inside the run directory (sockets) or
	// nested in another file's space is a configuration mistake worth refusing
	// rather than silently honouring.
	dirs := []struct{ key, dir string }{
		{"runtime.data_dir", cfg.Runtime.DataDir},
		{"runtime.cache_dir", cfg.Runtime.CacheDir},
		{"runtime.session_dir", cfg.Runtime.SessionDir},
		{"api.docs_dir", cfg.API.DocsDir},
	}
	for i, a := range dirs {
		if a.dir == "" {
			continue
		}
		if within(cfg.Runtime.RunDir, a.dir) {
			fail("%s %q must not be inside runtime.run_dir", a.key, a.dir)
		}
		for _, b := range dirs[i+1:] {
			if b.dir == "" {
				continue
			}
			if within(a.dir, b.dir) || within(b.dir, a.dir) {
				fail("%s %q must not overlap %s %q", a.key, a.dir, b.key, b.dir)
			}
		}
	}

	cfg.Runtime.RestartInitialDelay = DefaultRestartInitial
	if v := strings.TrimSpace(raw.Runtime.RestartInitialDelay); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("runtime.restart_initial_delay %q must be a positive duration", v)
		} else {
			cfg.Runtime.RestartInitialDelay = d
		}
	}
	cfg.Runtime.RestartMaxDelay = DefaultRestartMax
	if v := strings.TrimSpace(raw.Runtime.RestartMaxDelay); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("runtime.restart_max_delay %q must be a positive duration", v)
		} else {
			cfg.Runtime.RestartMaxDelay = d
		}
	}
	if cfg.Runtime.RestartMaxDelay < cfg.Runtime.RestartInitialDelay {
		fail("runtime.restart_max_delay must not be smaller than runtime.restart_initial_delay")
	}
	cfg.Runtime.StartupTimeout = DefaultStartupTimeout
	if v := strings.TrimSpace(raw.Runtime.StartupTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("runtime.startup_timeout %q must be a positive duration", v)
		} else {
			cfg.Runtime.StartupTimeout = d
		}
	}
	cfg.Runtime.ShutdownTimeout = DefaultShutdownTimeout
	if v := strings.TrimSpace(raw.Runtime.ShutdownTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("runtime.shutdown_timeout %q must be a positive duration", v)
		} else {
			cfg.Runtime.ShutdownTimeout = d
		}
	}
	cfg.Runtime.MaxRestartAttempts = DefaultMaxRestarts
	if raw.Runtime.MaxRestartAttempts != nil {
		if *raw.Runtime.MaxRestartAttempts < 1 {
			fail("runtime.max_restart_attempts must be at least 1")
		} else {
			cfg.Runtime.MaxRestartAttempts = *raw.Runtime.MaxRestartAttempts
		}
	}

	// --- Clients ---
	if len(raw.Clients) == 0 {
		fail("at least one client must be configured")
	}
	seen := map[string]bool{}
	sessionPaths := map[string]string{}
	socketPaths := map[string]string{}

	for i, rc := range raw.Clients {
		label := rc.Key
		if label == "" {
			label = fmt.Sprintf("clients[%d]", i)
		}

		key := strings.TrimSpace(rc.Key)
		if err := validateKey(key); err != nil {
			fail("%s: %v", label, err)
			continue
		}
		if seen[key] {
			fail("duplicate client key %q", key)
			continue
		}
		seen[key] = true

		cl := Client{
			Key:        key,
			Enabled:    rc.Enabled == nil || *rc.Enabled,
			AppID:      rc.AppID,
			AppHashEnv: strings.TrimSpace(rc.AppHashEnv),
			PhoneEnv:   strings.TrimSpace(rc.PhoneEnv),
		}

		switch Type(strings.TrimSpace(rc.Type)) {
		case Official:
			cl.Type = Official
			if rc.Server != nil {
				fail("client %q: type official must not carry a server block; official connects to Telegram production", key)
			}
		case Custom:
			cl.Type = Custom
			if rc.Server == nil {
				fail("client %q: type custom requires a server block", key)
			}
		default:
			fail("client %q: unsupported type %q (expected %q or %q)", key, rc.Type, Official, Custom)
		}

		if cl.AppID <= 0 {
			fail("client %q: app_id is required and must be a positive integer", key)
		}
		// Exactly one hash source: a literal app_hash, or the name of the
		// variable holding it. Both is ambiguous, neither is unusable.
		literalHash := strings.TrimSpace(rc.AppHash)
		switch {
		case literalHash != "" && cl.AppHashEnv != "":
			fail("client %q: set either app_hash or app_hash_env, not both", key)
		case literalHash != "":
			if strings.ContainsAny(literalHash, " \t\r\n") {
				fail("client %q: app_hash must not contain whitespace", key)
			}
			cl.APIHash = literalHash
		case cl.AppHashEnv != "":
			if hash := strings.TrimSpace(lookup(cl.AppHashEnv)); hash == "" {
				fail("client %q: app_hash_env names %s, which is not set", key, cl.AppHashEnv)
			} else {
				cl.APIHash = hash
			}
		default:
			fail("client %q: app_hash (a literal hash) or app_hash_env (the name of the variable holding it) is required", key)
		}

		literalPhone := NormalizePhone(strings.TrimSpace(rc.Phone))
		switch {
		case literalPhone != "" && cl.PhoneEnv != "":
			fail("client %q: set either phone or phone_env, not both", key)
		case literalPhone != "":
			cl.Phone = literalPhone
		case cl.PhoneEnv != "":
			cl.Phone = NormalizePhone(strings.TrimSpace(lookup(cl.PhoneEnv)))
		}

		cl.SessionPath = resolvePath(baseDir, rc.SessionPath, "")
		if rc.SessionPath == "" {
			cl.SessionPath = filepath.Join(cfg.Runtime.SessionDir, key+".json")
		}
		cl.RPCSocketPath = resolvePath(baseDir, rc.RPCSocketPath, "")
		if rc.RPCSocketPath == "" {
			cl.RPCSocketPath = filepath.Join(cfg.Runtime.RunDir, key+".sock")
		}
		cl.PeerCachePath = resolvePath(baseDir, rc.PeerCachePath, "")
		if rc.PeerCachePath == "" {
			cl.PeerCachePath = filepath.Join(cfg.Runtime.PeerDir, key+".json")
		}

		if !within(cfg.Runtime.RunDir, cl.RPCSocketPath) {
			fail("client %q: rpc_socket_path %q must be inside runtime.run_dir %q",
				key, cl.RPCSocketPath, cfg.Runtime.RunDir)
		}
		if within(cfg.Runtime.RunDir, cl.SessionPath) {
			fail("client %q: session_path %q must not be inside runtime.run_dir %q (sessions are credentials, not runtime files)",
				key, cl.SessionPath, cfg.Runtime.RunDir)
		}
		if cl.SessionPath == cl.RPCSocketPath {
			fail("client %q: session_path and rpc_socket_path must differ", key)
		}
		if other, ok := sessionPaths[cl.SessionPath]; ok {
			fail("clients %q and %q share session_path %q", other, key, cl.SessionPath)
		} else {
			sessionPaths[cl.SessionPath] = key
		}
		if other, ok := socketPaths[cl.RPCSocketPath]; ok {
			fail("clients %q and %q share rpc_socket_path %q", other, key, cl.RPCSocketPath)
		} else {
			socketPaths[cl.RPCSocketPath] = key
		}

		if cl.Type == Custom && rc.Server != nil {
			cl.Server = buildServer(baseDir, rc.Server, key, fail)
		}

		cfg.Clients = append(cfg.Clients, cl)
	}

	// --- public base URL ---
	// The origin an outsider reaches this installation at. Nothing invents an
	// address when it is absent: a webhook that would have to carry a media URL
	// is refused at configuration time instead of being sent a link that cannot
	// be opened.
	cfg.API.PublicBaseURL = strings.TrimSuffix(strings.TrimSpace(raw.API.PublicBaseURL), "/")
	if cfg.API.PublicBaseURL != "" {
		u, err := url.Parse(cfg.API.PublicBaseURL)
		switch {
		case err != nil:
			fail("api.public_base_url %q is not a valid URL", raw.API.PublicBaseURL)
		case u.Scheme != "http" && u.Scheme != "https":
			fail("api.public_base_url %q must be an http or https URL", raw.API.PublicBaseURL)
		case u.Host == "":
			fail("api.public_base_url %q must name a host", raw.API.PublicBaseURL)
		case u.User != nil:
			fail("api.public_base_url %q must not carry credentials", raw.API.PublicBaseURL)
		case u.RawQuery != "" || u.Fragment != "":
			fail("api.public_base_url %q must not carry a query or fragment", raw.API.PublicBaseURL)
		}
	}

	// --- automation ---
	automation, automationProblems := raw.Automation.resolve(AutomationConfig{}, lookup)
	problems = append(problems, automationProblems...)
	cfg.Automation = automation

	// --- direct sending ---
	send, sendProblems := raw.Send.resolve(SendConfig{}, lookup)
	problems = append(problems, sendProblems...)
	cfg.Send = send

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("configuration is invalid: %s", strings.Join(problems, "; "))
	}
	return cfg, nil
}

func buildServer(baseDir string, rs *rawServer, key string, fail func(string, ...any)) *Server {
	srv := &Server{
		Host:       strings.TrimSpace(rs.Host),
		Port:       rs.Port,
		DCID:       rs.DCID,
		Transport:  strings.ToLower(strings.TrimSpace(rs.Transport)),
		Routing:    strings.ToLower(strings.TrimSpace(rs.Routing)),
		Obfuscated: rs.Obfuscated,
	}

	switch {
	case srv.Host == "":
		fail("client %q: server.host is required", key)
	case strings.Contains(srv.Host, "://"):
		fail("client %q: server.host must be a bare hostname or IP address, without a URL scheme", key)
	case strings.ContainsAny(srv.Host, "/ \t"):
		fail("client %q: server.host must be a bare hostname or IP address, without a path", key)
	}
	if srv.Port == 0 {
		srv.Port = DefaultCustomPort
	}
	if srv.Port < 1 || srv.Port > 65535 {
		fail("client %q: server.port must be between 1 and 65535", key)
	}
	if srv.DCID == 0 {
		srv.DCID = DefaultCustomDC
	}
	if srv.DCID < 1 || srv.DCID > 5 {
		fail("client %q: server.dc_id must be between 1 and 5", key)
	}
	if srv.Transport == "" {
		srv.Transport = DefaultTransport
	}
	switch srv.Transport {
	case "intermediate", "abridged", "padded-intermediate", "full":
	default:
		fail("client %q: server.transport %q is not supported (expected intermediate, abridged, padded-intermediate or full)",
			key, srv.Transport)
	}
	if srv.Routing == "" {
		srv.Routing = DefaultRouting
	}
	switch srv.Routing {
	case "single-dc", "map-all":
	default:
		fail("client %q: server.routing %q is not supported (expected single-dc or map-all)", key, srv.Routing)
	}

	if strings.TrimSpace(rs.RSAPublicKeyPath) == "" {
		fail("client %q: server.rsa_public_key_path is required", key)
		return srv
	}
	srv.RSAPublicKeyPath = resolvePath(baseDir, rs.RSAPublicKeyPath, "")
	info, err := os.Stat(srv.RSAPublicKeyPath)
	switch {
	case os.IsNotExist(err):
		fail("client %q: server.rsa_public_key_path %q does not exist", key, srv.RSAPublicKeyPath)
	case err != nil:
		fail("client %q: server.rsa_public_key_path %q: %v", key, srv.RSAPublicKeyPath, err)
	case !info.Mode().IsRegular():
		fail("client %q: server.rsa_public_key_path %q is not a regular file", key, srv.RSAPublicKeyPath)
	}
	return srv
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if !keyPattern.MatchString(key) || strings.HasSuffix(key, "-") {
		return fmt.Errorf("key %q is unsafe: use 1-64 lowercase letters, digits or single hyphens, starting with a letter", key)
	}
	return nil
}

// resolvePath resolves p against baseDir, applying def when p is empty.
// findDocsUpwards is the repository's docs/api when dir, or a parent of it,
// holds one: a configuration kept anywhere inside a checkout serves the tree
// the source is being edited in, which is what someone running from a checkout
// expects. The closest tree wins. With none, the binary serves its embedded
// copy (see EmbeddedDocs).
func findDocsUpwards(dir string) string {
	for d := filepath.Clean(dir); ; {
		candidate := filepath.Join(d, "docs", "api")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

func resolvePath(baseDir, p, def string) string {
	if strings.TrimSpace(p) == "" {
		p = def
	}
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p)
}

// within reports whether path is dir itself or inside it.
func within(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// MaskSecret describes a secret without revealing it.
func MaskSecret(v string) string {
	if v == "" {
		return "(not set)"
	}
	return fmt.Sprintf("(set, %d chars)", len(v))
}

// MaskPhone hides the middle digits of a phone number.
func MaskPhone(phone string) string {
	if phone == "" {
		return ""
	}
	prefix := ""
	if strings.HasPrefix(phone, "+") {
		prefix, phone = "+", phone[1:]
	}
	if len(phone) <= 4 {
		return prefix + strings.Repeat("*", len(phone))
	}
	return prefix + phone[:2] + strings.Repeat("*", len(phone)-4) + phone[len(phone)-2:]
}

// NormalizePhone strips formatting characters users copy from address books:
// spaces, dashes, parentheses, dots.
func NormalizePhone(v string) string {
	return strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(v)
}
