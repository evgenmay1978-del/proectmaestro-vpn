// Package anytls manages a LOCAL standalone sing-box AnyTLS server on server 1 — the same
// box as the panel. Unlike the server-2 protocols (hy2/naive/mieru, synced over SSH), the
// AnyTLS server is co-located with the panel, so the panel writes its config + restarts the
// service directly (no SSH). AnyTLS is a NATIVE sing-box outbound — no on-device helper.
package anytls

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// User is one AnyTLS credential (server-side). The client authenticates by password; name
// is a server-side label, so each customer's password must be unique.
type User struct {
	Name     string
	Password string
}

// Manager renders the standalone sing-box AnyTLS server config + reloads its service.
type Manager struct {
	ConfigPath string // e.g. /etc/sing-box-anytls/config.json
	CertPath   string // TLS cert (self-signed is fine — client uses insecure)
	KeyPath    string
	ListenPort int    // e.g. 8444
	Service    string // systemd unit, e.g. "sing-box-anytls"
}

// SyncUsers rewrites the server config with exactly `users` (the active customers) and
// restarts the service so the new user set takes effect. An empty set keeps the server up
// with no valid users (it never tears the service down). Atomic write + restart.
func (m *Manager) SyncUsers(users []User) error {
	us := make([]map[string]any, 0, len(users))
	for _, u := range users {
		us = append(us, map[string]any{"name": u.Name, "password": u.Password})
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []map[string]any{{
			"type": "anytls", "tag": "anytls-in",
			"listen": "::", "listen_port": m.ListenPort,
			"users": us,
			"tls": map[string]any{
				"enabled":          true,
				"certificate_path": m.CertPath,
				"key_path":         m.KeyPath,
			},
		}},
		"outbounds": []map[string]any{{"type": "direct", "tag": "direct"}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("anytls: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("anytls: mkdir: %w", err)
	}
	tmp := m.ConfigPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("anytls: write: %w", err)
	}
	if err := os.Rename(tmp, m.ConfigPath); err != nil {
		return fmt.Errorf("anytls: rename: %w", err)
	}
	if out, err := exec.Command("systemctl", "restart", m.Service).CombinedOutput(); err != nil {
		return fmt.Errorf("anytls: restart %s: %w (%s)", m.Service, err, string(out))
	}
	return nil
}
