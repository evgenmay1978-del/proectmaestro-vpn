// Package server2 provisions protocols on the second server (85.137.166.237)
// over SSH. It owns the Hysteria2 user list and regenerates the Hysteria config
// from a template on every change (full regen, not in-place YAML surgery), then
// reloads only the hysteria-server service. Naive (Caddy) and 3x-ui are managed
// elsewhere and never touched here.
package server2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"text/template"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config is the SSH target + the fixed Hysteria listener facts (from the install).
type Config struct {
	Host     string // e.g. "85.137.166.237"
	SSHPort  int    // 22
	User     string // "root"
	Password string

	Hy2Port    int    // 8443
	Hy2CertPem string // /etc/hysteria/server.crt (already on the box)
	Hy2KeyPem  string // /etc/hysteria/server.key

	// rixxx-panel (Naive) — reachable only on server-2 localhost (UFW-denied
	// externally), so we drive it over the same SSH connection.
	NaivePanelURL  string // e.g. http://127.0.0.1:3000
	NaivePanelUser string
	NaivePanelPass string

	// AnyTLS — standalone sing-box "anytls" server on server 2, ADDITIVE on its own
	// port (8443/tcp) + systemd unit (sing-box-anytls), driven over the same SSH
	// connection. Independent of caddy(naive):443/tcp, hysteria:8443/udp.
	AnyTLSPort       int    // 8443 (RU-reachable TCP)
	AnyTLSCert       string // /etc/sing-box-anytls/cert.pem (on server 2)
	AnyTLSKey        string // /etc/sing-box-anytls/key.pem
	AnyTLSService    string // sing-box-anytls
	AnyTLSConfigPath string // /etc/sing-box-anytls/config.json
}

// Hy2User is one Hysteria2 userpass credential.
type Hy2User struct {
	User string
	Pass string
}

const hy2ConfigPath = "/etc/hysteria/config.yaml"

var hy2Tmpl = template.Must(template.New("hy2").Parse(`listen: :{{.Port}}
tls:
  cert: {{.Cert}}
  key: {{.Key}}
auth:
  type: userpass
  userpass:
{{- range .Users}}
    {{.User}}: {{.Pass}}
{{- end}}
masquerade:
  type: proxy
  proxy:
    url: https://www.bing.com/
    rewriteHost: true
`))

// Client runs commands on server 2.
type Client struct{ cfg Config }

// New returns a server-2 client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) dial() (*ssh.Client, error) {
	port := c.cfg.SSHPort
	if port == 0 {
		port = 22
	}
	conf := &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(c.cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // #nosec G106 -- SSH to the owner's own S2/S3 by password; known_hosts pinning is a separate hardening item
		Timeout:         15 * time.Second,
	}
	cl, err := ssh.Dial("tcp", net.JoinHostPort(c.cfg.Host, fmt.Sprint(port)), conf)
	if err != nil {
		return nil, fmt.Errorf("server2: ssh dial: %w", err)
	}
	return cl, nil
}

// run executes a command, optionally feeding stdin, and returns stdout.
func (c *Client) run(cmd, stdin string) (string, error) {
	cl, err := c.dial()
	if err != nil {
		return "", err
	}
	defer func() { _ = cl.Close() }()
	sess, err := cl.NewSession()
	if err != nil {
		return "", fmt.Errorf("server2: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errb
	if err := sess.Run(cmd); err != nil {
		return out.String(), fmt.Errorf("server2: run %q: %w (stderr: %s)", cmd, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// hy2SafeUser reports whether a username is safe to embed as a Hysteria (viper) userpass key.
// Hysteria parses its YAML through viper, which treats "." as a KEY-NESTING delimiter: a username
// containing "." is read as a nested map ("a.b: p" → {a:{b:p}}), so mapstructure then rejects the
// WHOLE `auth` block ("expected string, got map") and Hysteria fails to start — taking Hy2 down for
// EVERY user, not just the offending one (this actually happened: a "trial-roman.pfa" login put the
// server in a 4800-restart crash-loop). Any YAML-structural char (":", whitespace, leading "-") on a
// bare "  user: pass" line is likewise unsafe. Restrict to a conservative login charset; such a
// username can't authenticate on Hysteria anyway, and the user still has every other protocol.
func hy2SafeUser(u string) bool {
	if u == "" || len(u) > 64 {
		return false
	}
	for _, r := range u {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// renderHy2 builds the full Hysteria config for the given user set.
func (c *Client) renderHy2(users []Hy2User) (string, error) {
	cert := c.cfg.Hy2CertPem
	if cert == "" {
		cert = "/etc/hysteria/server.crt"
	}
	key := c.cfg.Hy2KeyPem
	if key == "" {
		key = "/etc/hysteria/server.key"
	}
	port := c.cfg.Hy2Port
	if port == 0 {
		port = 8443
	}
	// Defensive filter: one malformed login must NEVER be able to crash Hy2 for the whole fleet.
	safe := make([]Hy2User, 0, len(users))
	for _, u := range users {
		if !hy2SafeUser(u.User) {
			log.Printf("server2: skipping Hy2 user %q — unsafe as a hysteria/viper config key", u.User)
			continue
		}
		safe = append(safe, u)
	}
	users = safe
	var b bytes.Buffer
	err := hy2Tmpl.Execute(&b, map[string]any{"Port": port, "Cert": cert, "Key": key, "Users": users})
	if err != nil {
		return "", fmt.Errorf("server2: render hy2 config: %w", err)
	}
	return b.String(), nil
}

// SyncHy2Users regenerates /etc/hysteria/config.yaml from the full user list and
// reloads hysteria-server. A backup is kept; on a failed restart the previous
// config is restored so a bad write never leaves Hysteria down.
//
// SAFETY INVARIANT: /etc/hysteria/config.yaml is APP-EXCLUSIVE — the MaestroVPN
// panel is its sole owner. Unlike the Naive Caddyfile (shared with 14 pre-existing
// customers, so app users live in an isolated # MTV-MANAGED block), Hysteria on
// server 2 has NO non-app users, which is what makes this full-overwrite safe.
// DO NOT add externally-managed hysteria users to this file — they would be wiped
// on the next sync. If that ever changes, switch to add-only/marker-block discipline
// like SyncNaiveUsers before introducing any external user.
func (c *Client) SyncHy2Users(users []Hy2User) error {
	cfg, err := c.renderHy2(users)
	if err != nil {
		return err
	}
	// Write atomically via stdin, back up the old one, restart, verify, rollback on failure.
	// `systemctl restart` returns 0 as soon as the unit STARTS, even if the process then dies and
	// enters a Restart=always crash-loop (a bad config = FATAL-on-parse). So checking the restart
	// exit code alone misses crash-loops. Instead: restart, wait, and if it isn't genuinely `active`
	// afterwards, roll the previous config back — so a bad write never leaves Hysteria down.
	script := fmt.Sprintf(`set -e
cp -a %[1]s %[1]s.bak 2>/dev/null || true
cat > %[1]s.new
mv %[1]s.new %[1]s
systemctl restart hysteria-server || true
sleep 2
if [ "$(systemctl is-active hysteria-server)" != active ]; then
  echo "hysteria not active after restart — rolling back to last-good config" >&2
  [ -f %[1]s.bak ] && mv %[1]s.bak %[1]s
  systemctl restart hysteria-server || true
  sleep 2
fi
systemctl is-active hysteria-server`, hy2ConfigPath)
	// The script is the SSH command; its `cat > …` reads the config from stdin.
	out, err := c.run(script, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "active" {
		return fmt.Errorf("server2: hysteria not active after sync: %q", strings.TrimSpace(out))
	}
	return nil
}

// AnyTLSUser is one standalone sing-box AnyTLS credential (server-side). The client
// authenticates by password; Name is a server-side label, so each password must be unique.
type AnyTLSUser struct {
	Name string
	Pass string
}

const anytlsConfigPath = "/etc/sing-box-anytls/config.json"

// renderAnyTLS builds the FULL standalone sing-box AnyTLS server config (one `anytls`
// inbound with the complete user set + TLS, direct outbound) as JSON — a full regen,
// not a patch, so an expired customer dropped from `users` can no longer connect after
// the next apply. Mirrors the config the panel used to write locally on server 1.
func renderAnyTLS(port int, cert, key string, users []AnyTLSUser) (string, error) {
	if port == 0 {
		port = 8443
	}
	if cert == "" {
		cert = "/etc/sing-box-anytls/cert.pem"
	}
	if key == "" {
		key = "/etc/sing-box-anytls/key.pem"
	}
	us := make([]map[string]string, 0, len(users))
	for _, u := range users {
		us = append(us, map[string]string{"name": u.Name, "password": u.Pass})
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []map[string]any{{
			"type": "anytls", "tag": "anytls-in",
			"listen": "::", "listen_port": port,
			"users": us,
			"tls": map[string]any{
				"enabled":          true,
				"certificate_path": cert,
				"key_path":         key,
			},
		}},
		"outbounds": []map[string]any{{"type": "direct", "tag": "direct"}},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("server2: render anytls config: %w", err)
	}
	return string(b), nil
}

// SyncAnyTLSUsers regenerates the standalone sing-box AnyTLS config on server 2 from the
// full active user set and restarts the service, keeping a backup and rolling back on a
// failed restart so a bad write never leaves AnyTLS down. ADDITIVE — its own port
// (8443/tcp), its own systemd unit — so this never touches caddy(naive)/hysteria.
//
// SAFETY INVARIANT: the AnyTLS config is APP-EXCLUSIVE — the MaestroVPN panel is its sole
// owner (no external users), exactly like Hysteria on server 2, which is what makes the
// full overwrite safe. DO NOT add externally-managed anytls users to this file.
func (c *Client) SyncAnyTLSUsers(users []AnyTLSUser) error {
	path := c.cfg.AnyTLSConfigPath
	if path == "" {
		path = anytlsConfigPath
	}
	svc := c.cfg.AnyTLSService
	if svc == "" {
		svc = "sing-box-anytls"
	}
	cfg, err := renderAnyTLS(c.cfg.AnyTLSPort, c.cfg.AnyTLSCert, c.cfg.AnyTLSKey, users)
	if err != nil {
		return err
	}
	// Atomic write via stdin (umask 077 → the password-bearing config stays 0600),
	// back up the old one, restart, verify, rollback on failure.
	script := fmt.Sprintf(`set -e
cp -a %[1]s %[1]s.bak 2>/dev/null || true
umask 077
cat > %[1]s.new
mv %[1]s.new %[1]s
if ! systemctl restart %[2]s; then
  echo "restart failed, rolling back" >&2
  [ -f %[1]s.bak ] && mv %[1]s.bak %[1]s && systemctl restart %[2]s
  exit 1
fi
sleep 2
systemctl is-active %[2]s`, path, svc)
	out, err := c.run(script, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "active" {
		return fmt.Errorf("server2: %s not active after sync: %q", svc, strings.TrimSpace(out))
	}
	return nil
}

// HealthCheck verifies SSH connectivity + that the existing services we must not
// break are still healthy (caddy = naive, hysteria).
func (c *Client) HealthCheck() (string, error) {
	out, err := c.run(
		`echo "caddy=$(systemctl is-active caddy) hysteria=$(systemctl is-active hysteria-server) `+
			`naive443=$(timeout 4 bash -c 'echo>/dev/tcp/127.0.0.1/443' 2>/dev/null && echo ok || echo no)"`, "")
	return strings.TrimSpace(out), err
}
