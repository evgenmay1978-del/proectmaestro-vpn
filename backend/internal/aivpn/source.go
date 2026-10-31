package aivpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// Source is where a subscriber's AIVPN connection key comes from, plus the two
// gates that belong to the credentials themselves (minimum app versionCode and
// the login whitelist). The api layer adds the request-side gates (feature
// enabled, active subscription, User-Agent).
//
// Implementations must never return a key that does not Parse: the panel only
// relays keys, it cannot mint them (it does not hold the server's private key).
type Source interface {
	// Enabled reports whether issuance is switched on for this host.
	Enabled() bool
	// MinVersionCode is the lowest app versionCode that understands AIVPN. Must
	// be > 0 when enabled, so a zero/unknown versionCode (curl, Karing, old app)
	// can never match.
	MinVersionCode() int
	// Allowed reports whether login is on the issuance whitelist.
	Allowed(login string) bool
	// Key returns login's connection key in aivpn:// form.
	Key(ctx context.Context, login string) (string, error)
	// KeyForDevice returns the connection key for one of login's devices. The
	// device id is the app's per-install random id (never a credential): a
	// Source may use it to give each install its own credential. An empty
	// device means "no device scoping" and must behave exactly like Key.
	KeyForDevice(ctx context.Context, login, device string) (string, error)
}

var (
	// ErrDisabled is returned when the feature is off.
	ErrDisabled = errors.New("aivpn: feature disabled")
	// ErrNoClient is returned when the source holds no usable key for a login:
	// unknown login, or a client that was revoked on the AIVPN server.
	ErrNoClient = errors.New("aivpn: no key for this login")
)

// safeLoginRe bounds a login that becomes an AIVPN client name (and, in the CLI
// source, an argv word). Same charset the panel uses for claim codes.
var safeLoginRe = regexp.MustCompile("^[A-Za-z0-9._@-]{1,64}$")

func validLogin(login string) bool { return safeLoginRe.MatchString(login) }

// Config is the pilot's file-backed configuration (MAESTRO_AIVPN_FILE):
//
//	{
//	  "enabled": true,
//	  "min_version_code": 108,
//	  "logins": ["alice"],
//	  "keys": {"alice": "aivpn://..."}
//	}
//
// logins is the whitelist and every listed login must have a key. Keys are the
// exact strings the AIVPN server issued for those clients — the panel relays
// them, it does not mint them.
type Config struct {
	Enabled        bool              `json:"enabled"`
	MinVersionCode int               `json:"min_version_code"`
	Logins         []string          `json:"logins"`
	Keys           map[string]string `json:"keys"`
}

// Static serves keys from StaticConfig (the pilot path): a fixed map of
// subscriber login -> pre-issued connection key.
type Static struct {
	cfg   Config
	allow map[string]bool
	keys  map[string]string
}

var _ Source = (*Static)(nil)

// OpenStatic loads path. An empty path or a missing file means OFF (nil, nil):
// the pilot is opt-in per host, and a host without the file must behave exactly
// as before. A file that exists but does not validate aborts startup, because a
// half-broken whitelist must not silently advertise an empty credential set.
func OpenStatic(path string) (*Static, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("aivpn: read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("aivpn: config %s is not valid JSON: %w", path, err)
	}
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.MinVersionCode <= 0 {
		return nil, fmt.Errorf("aivpn: config %s: min_version_code must be > 0 when enabled", path)
	}
	if len(cfg.Keys) == 0 {
		return nil, fmt.Errorf("aivpn: config %s: enabled but keys is empty", path)
	}
	allow := make(map[string]bool, len(cfg.Logins))
	for _, login := range cfg.Logins {
		login = strings.TrimSpace(login)
		if !validLogin(login) {
			return nil, fmt.Errorf("aivpn: config %s: login %q must match [A-Za-z0-9._@-]{1,64}", path, login)
		}
		if allow[login] {
			return nil, fmt.Errorf("aivpn: config %s: duplicate login %q", path, login)
		}
		if _, ok := cfg.Keys[login]; !ok {
			return nil, fmt.Errorf("aivpn: config %s: login %q has no key", path, login)
		}
		allow[login] = true
	}
	if len(allow) == 0 {
		return nil, fmt.Errorf("aivpn: config %s: enabled but the logins whitelist is empty", path)
	}
	keys := make(map[string]string, len(allow))
	for login := range allow {
		raw := strings.TrimSpace(cfg.Keys[login])
		if _, err := Parse(raw); err != nil {
			// The error carries no key material (see Parse).
			return nil, fmt.Errorf("aivpn: config %s: key for %q: %w", path, login, err)
		}
		keys[login] = raw
	}
	return &Static{cfg: cfg, allow: allow, keys: keys}, nil
}

// Enabled implements Source.
func (s *Static) Enabled() bool { return s != nil && s.cfg.Enabled }

// MinVersionCode implements Source.
func (s *Static) MinVersionCode() int {
	if s == nil {
		return 0
	}
	return s.cfg.MinVersionCode
}

// Allowed implements Source.
func (s *Static) Allowed(login string) bool { return s != nil && s.allow[login] }

// Key implements Source.
func (s *Static) Key(ctx context.Context, login string) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	key, ok := s.keys[login]
	if !ok {
		return "", ErrNoClient
	}
	return key, nil
}

// KeyForDevice implements Source. The pilot's file holds exactly one pre-issued
// key per login, so every device of that login gets the same key: device
// scoping is a Server-mode (S1 management API) feature, and a pilot config must
// never fail a device request.
func (s *Static) KeyForDevice(ctx context.Context, login, _ string) (string, error) {
	return s.Key(ctx, login)
}

// Environment variables read by OpenOS. Everything is opt-in: with none of them
// set the feature is OFF and the panel behaves exactly as before.
const (
	// EnvSource selects the implementation: static (default) | http | cli.
	EnvSource = "MAESTRO_AIVPN_SOURCE"
	// EnvFile is the static pilot config (see StaticConfig).
	EnvFile = "MAESTRO_AIVPN_FILE"
	// EnvLogins is a comma separated whitelist (http/cli sources).
	EnvLogins = "MAESTRO_AIVPN_LOGINS"
	// EnvMinVC is the minimum app versionCode that may receive a key.
	EnvMinVC = "MAESTRO_AIVPN_MIN_VC"
	// EnvSocket overrides the management unix socket (default /run/aivpn/api.sock).
	EnvSocket = "MAESTRO_AIVPN_SOCKET"
	// EnvCLI is the aivpn-server binary for the CLI source.
	EnvCLI = "MAESTRO_AIVPN_CLI"
	// EnvCLIConfig is --config for the CLI source.
	EnvCLIConfig = "MAESTRO_AIVPN_CLI_CONFIG"
	// EnvServerAddr is the public host:port the CLI stamps into the key.
	EnvServerAddr = "MAESTRO_AIVPN_SERVER_ADDR"
)

// DefaultSocket is aivpn-server's management socket path.
const DefaultSocket = "/run/aivpn/api.sock"

// OpenOS builds the source from the environment. It returns (nil, nil) when the
// feature is off, and a non-nil error when it is configured but unusable — the
// caller must fail startup rather than serve a feature that cannot work.
func OpenOS(getenv func(string) string) (Source, error) {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvSource))) {
	case "", "static":
		st, err := OpenStatic(getenv(EnvFile))
		if err != nil {
			return nil, err
		}
		if st == nil || !st.Enabled() {
			return nil, nil
		}
		return st, nil
	case "http", "rest":
		return openServer(getenv, false)
	case "cli":
		return openServer(getenv, true)
	default:
		return nil, fmt.Errorf("aivpn: %s=%q is not one of static, http, cli", EnvSource, getenv(EnvSource))
	}
}

// openServer builds the S1-backed source (management REST or CLI).
func openServer(getenv func(string) string, cli bool) (Source, error) {
	logins, err := splitLogins(getenv(EnvLogins))
	if err != nil {
		return nil, err
	}
	cfg := ServerConfig{
		Enabled:        true,
		Logins:         logins,
		MinVersionCode: atoi(getenv(EnvMinVC)),
	}
	if cli {
		cfg.CLIPath = strings.TrimSpace(getenv(EnvCLI))
		cfg.CLIConfigPath = strings.TrimSpace(getenv(EnvCLIConfig))
		cfg.ServerAddr = strings.TrimSpace(getenv(EnvServerAddr))
	} else {
		cfg.SocketPath = envOr(getenv, EnvSocket, DefaultSocket)
	}
	return NewServer(cfg)
}

// splitLogins parses the comma separated whitelist and rejects entries that
// could not be a client name.
func splitLogins(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		login := strings.TrimSpace(part)
		if login == "" {
			continue
		}
		if !validLogin(login) {
			return nil, fmt.Errorf("aivpn: %s: login %q must match [A-Za-z0-9._@-]{1,64}", EnvLogins, login)
		}
		if seen[login] {
			continue
		}
		seen[login] = true
		out = append(out, login)
	}
	return out, nil
}

func envOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

// atoi is a lenient integer parse: empty or garbage becomes 0, which the gates
// treat as "not enabled" rather than as a wildcard.
func atoi(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1<<31 {
			return 0
		}
	}
	return n
}
