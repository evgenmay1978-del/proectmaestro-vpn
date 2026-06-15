package server2

import (
	"fmt"
	"strings"
)

// NaivePrefix marks naive (Caddy basic_auth) users created for the TV app. The
// backend ONLY ever manages users inside the `# MTV-MANAGED` block of the
// Caddyfile — the pre-existing naive customers (outside that block) are never
// read-modified-deleted here.
const NaivePrefix = "mtv_"

// NaiveUser is one Naive credential = a Caddy forward_proxy basic_auth entry.
// User already carries NaivePrefix (set by the provisioner).
type NaiveUser struct {
	User string
	Pass string
}

const caddyfilePath = "/etc/caddy/Caddyfile"

// SyncNaiveUsers regenerates the app-managed naive users as a delimited
// `# MTV-MANAGED-START … # MTV-MANAGED-END` block of basic_auth lines INSIDE
// Caddy's forward_proxy directive, inserted right after the last existing
// basic_auth so every pre-existing user is left byte-for-byte untouched. It
// `caddy validate`s the result and `systemctl reload caddy`s, restoring the
// previous Caddyfile on ANY failure so a bad write never takes naive down.
//
// The whole app naive user set is regenerated each call (full sync, like
// Hysteria/Mieru), so an expired/disabled customer dropped from `users` can no
// longer connect after the next sync.
func (c *Client) SyncNaiveUsers(users []NaiveUser) error {
	var b strings.Builder
	b.WriteString("    # MTV-MANAGED-START\n")
	for _, u := range users {
		fmt.Fprintf(&b, "    basic_auth %s %s\n", u.User, u.Pass)
	}
	b.WriteString("    # MTV-MANAGED-END")

	// The script reads the new MTV block from stdin. It backs up, strips any old
	// MTV block, inserts the fresh one after the last basic_auth line (an existing
	// user), validates, reloads, and rolls back on failure.
	script := fmt.Sprintf(`set -e
CF=%[1]s
cp -a "$CF" "$CF.mtvbak"
BLOCK=$(cat)
sed -i '/# MTV-MANAGED-START/,/# MTV-MANAGED-END/d' "$CF"
awk -v block="$BLOCK" '/basic_auth/{last=NR}{l[NR]=$0}END{for(i=1;i<=NR;i++){print l[i]; if(i==last)print block}}' "$CF" > "$CF.new"
mv "$CF.new" "$CF"
if ! caddy validate --adapter caddyfile --config "$CF" >/dev/null 2>&1; then
  mv "$CF.mtvbak" "$CF"
  echo "naive: caddy validate failed, rolled back" >&2
  exit 1
fi
if ! systemctl reload caddy; then
  mv "$CF.mtvbak" "$CF"
  systemctl reload caddy || true
  echo "naive: caddy reload failed, rolled back" >&2
  exit 1
fi
rm -f "$CF.mtvbak"
sleep 1
systemctl is-active caddy`, caddyfilePath)

	out, err := c.run(script, b.String())
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "active" {
		return fmt.Errorf("server2: caddy not active after naive sync: %q", strings.TrimSpace(out))
	}
	return nil
}

// ReadNaiveUser returns the password of an EXISTING Caddy basic_auth user (any
// user, app-managed or not) so a customer already on the naive panel can activate
// the app with their current credential. Empty pass + false when not found.
func (c *Client) ReadNaiveUser(username string) (string, bool, error) {
	out, err := c.run(fmt.Sprintf(
		`awk '/basic_auth %s /{print $3; exit}' %s`, escapeAwk(username), caddyfilePath), "")
	if err != nil {
		return "", false, err
	}
	pass := strings.TrimSpace(out)
	if pass == "" {
		return "", false, nil
	}
	return pass, true, nil
}

// escapeAwk neutralises regex metacharacters in a username used in an awk match.
func escapeAwk(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `/`, `\/`, `.`, `\.`, `*`, `\*`, `[`, `\[`, `]`, `\]`, `(`, `\(`, `)`, `\)`, `+`, `\+`, `?`, `\?`, `^`, `\^`, `$`, `\$`)
	return r.Replace(s)
}

// s2BotDB is the server-2 NaiveProxy bot's SQLite DB (bot_minimal.py). Its
// `subscriptions` table holds the REAL end date for naive customers, which the
// naive panel/Caddyfile do not.
const s2BotDB = "/opt/vpn_bot/bot_minimal.db"

// ReadProxyExpiry returns the raw ISO end date for a naive customer from the
// server-2 bot DB, so a customer already on the naive panel keeps their actual
// subscription end when they activate the app (instead of a default). Empty +
// false when they have no subscription row there.
func (c *Client) ReadProxyExpiry(proxyUser string) (string, bool, error) {
	q := strings.ReplaceAll(proxyUser, "'", "''")
	out, err := c.run(fmt.Sprintf(
		`sqlite3 %s "SELECT expires_at FROM subscriptions WHERE proxy_user='%s' ORDER BY expires_at DESC LIMIT 1;" 2>/dev/null`,
		s2BotDB, q), "")
	if err != nil {
		return "", false, err
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}
