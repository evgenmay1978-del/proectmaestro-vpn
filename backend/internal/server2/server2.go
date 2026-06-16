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

	// Mieru (mita) — additive daemon on its OWN port (default 2027), driven over
	// the same SSH connection. Never collides with naive:443 / hysteria:8443.
	MitaPort int
	// MitaTransport is the SINGLE protocol mita binds on MitaPort ("TCP" or "UDP").
	// It must match the mieru client transport. UDP lets mita share port 443 with
	// Caddy/naive (which holds 443/TCP) once Caddy's HTTP/3 is disabled.
	MitaTransport string
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // owner's server, password auth
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
	script := fmt.Sprintf(`set -e
cp -a %[1]s %[1]s.bak 2>/dev/null || true
cat > %[1]s.new
mv %[1]s.new %[1]s
if ! systemctl restart hysteria-server; then
  echo "restart failed, rolling back" >&2
  [ -f %[1]s.bak ] && mv %[1]s.bak %[1]s && systemctl restart hysteria-server
  exit 1
fi
sleep 2
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

// MieruUser is one Mieru (mita) credential.
type MieruUser struct {
	User string
	Pass string
}

const mitaConfigPath = "/etc/mita/maestro_config.json"

// renderMita builds the FULL mita server config (port bindings + the complete
// user set) as JSON — a full regen, not a patch, so an expired customer dropped
// from `users` can no longer connect after the next apply.
func renderMita(port int, transport string, users []MieruUser) (string, error) {
	if port == 0 {
		port = 2027
	}
	// Bind ONLY the configured transport's protocol. Binding both used to be
	// harmless on a dedicated port, but on 443 a TCP binding would collide with
	// Caddy/naive — so mita must take UDP only there. The mieru client uses a
	// single transport anyway, so one binding is all that's needed.
	proto := strings.ToUpper(strings.TrimSpace(transport))
	if proto != "UDP" {
		proto = "TCP"
	}
	us := make([]map[string]string, 0, len(users))
	for _, u := range users {
		us = append(us, map[string]string{"name": u.User, "password": u.Pass})
	}
	cfg := map[string]any{
		"portBindings": []map[string]any{
			{"port": port, "protocol": proto},
		},
		"users":        us,
		"loggingLevel": "INFO",
		"mtu":          1400,
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("server2: render mita config: %w", err)
	}
	return string(b), nil
}

// SyncMieruUsers regenerates the mita config from the full active user set and
// applies it, then RESTARTS mita. mita is ADDITIVE — its own port, its own
// systemd unit — so this never touches naive (caddy) or hysteria.
//
// CRITICAL (verified e2e 2026-06-16): a full `systemctl restart mita` is required,
// NOT `mita reload`. `mita reload` re-reads the config but does NOT re-initialize
// the per-user cipher state for users added/changed since the daemon last started,
// so those users' first-packet handshake can never be decrypted server-side
// (metrics show FailedIterateDecrypt == IterateDecrypt, no session forms) — which
// is exactly why Mieru never connected for anyone. A clean restart re-derives every
// user's key from the applied config and fixes it. The brief connection drop is
// acceptable: Mieru clients reconnect, and syncs only happen on provision/renewal.
// `mita apply config` persists the config to mita's state, which the restart loads.
func (c *Client) SyncMieruUsers(users []MieruUser) error {
	cfg, err := renderMita(c.cfg.MitaPort, c.cfg.MitaTransport, users)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`set -e
mkdir -p /etc/mita
cat > %[1]s
mita apply config %[1]s
systemctl restart mita
sleep 3
mita status`, mitaConfigPath)
	out, err := c.run(script, cfg)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(out), "RUNNING") {
		return fmt.Errorf("server2: mita not running after sync: %q", strings.TrimSpace(out))
	}
	return nil
}

// HealthCheck verifies SSH connectivity + that the existing services we must not
// break are still healthy (caddy = naive, hysteria) and reports mita's state.
func (c *Client) HealthCheck() (string, error) {
	out, err := c.run(
		`echo "caddy=$(systemctl is-active caddy) hysteria=$(systemctl is-active hysteria-server) `+
			`mita=$(systemctl is-active mita 2>/dev/null || echo absent) `+
			`naive443=$(timeout 4 bash -c 'echo>/dev/tcp/127.0.0.1/443' 2>/dev/null && echo ok || echo no)"`, "")
	return strings.TrimSpace(out), err
}
