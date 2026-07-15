// Package vkturnconf loads the private, per-login WDTT/VK TURN configuration.
// The configuration file lives outside the repository and is deliberately
// immutable after startup: a missing or malformed file must never advertise a
// half-configured transport to clients.
package vkturnconf

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

var allowedLogins = [...]string{"wapmix", "wapmixx", "wapmix2"}

const (
	DefaultWorkers     = 9
	DefaultFingerprint = "chrome"
	DefaultObfsMode    = "audio"
)

var DefaultClientIDs = []string{"6287487", "8202606"}

// Client contains the secrets unique to one MaestroVPN login.
type Client struct {
	Password string             `json:"password"`
	WG       subgen.VKTurnCreds `json:"wg"`
}

// Config is the complete WDTT client configuration served by maestro-panel.
// Clients must contain exactly the three owner-approved logins.
type Config struct {
	Enabled        bool              `json:"enabled"`
	MinVersionCode int               `json:"min_version_code"`
	Server         string            `json:"server"`
	VKHashes       []string          `json:"vk_hashes"`
	Clients        map[string]Client `json:"clients"`
}

// Open reads and validates path. An empty path means the feature was not
// configured and returns nil without touching the filesystem. Any non-empty
// path is explicit operator intent, so errors are returned to make startup fail
// closed rather than silently serving an incomplete configuration.
func Open(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vkturn config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode vkturn config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode vkturn config: trailing JSON data")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate vkturn config: %w", err)
	}
	return &cfg, nil
}

// Validate rejects partial configurations even when Enabled is false. This
// makes flipping the operational switch safe and predictable.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if c.MinVersionCode <= 0 {
		return fmt.Errorf("min_version_code must be positive")
	}
	if strings.TrimSpace(c.Server) == "" {
		return fmt.Errorf("server is required")
	}
	host, port, err := net.SplitHostPort(c.Server)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("server must be host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("server port is invalid")
	}
	if len(c.VKHashes) == 0 || len(c.VKHashes) > 4 {
		return fmt.Errorf("vk_hashes must contain 1..4 values")
	}
	seenHashes := make(map[string]struct{}, len(c.VKHashes))
	for i, hash := range c.VKHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" || hash != c.VKHashes[i] {
			return fmt.Errorf("vk_hashes[%d] is empty", i)
		}
		if len(hash) > 160 || strings.ContainsFunc(hash, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._~-", r)
		}) {
			return fmt.Errorf("vk_hashes[%d] contains unsafe characters", i)
		}
		if _, exists := seenHashes[hash]; exists {
			return fmt.Errorf("vk_hashes[%d] is duplicated", i)
		}
		seenHashes[hash] = struct{}{}
	}
	if len(c.Clients) != len(allowedLogins) {
		return fmt.Errorf("clients must contain exactly wapmix, wapmixx and wapmix2")
	}
	for _, login := range allowedLogins {
		client, ok := c.Clients[login]
		if !ok {
			return fmt.Errorf("client %q is required", login)
		}
		if err := validateClient(login, client); err != nil {
			return err
		}
	}
	return nil
}

// ClientFor returns a copy of the per-login configuration. Unknown logins fail
// closed; matching is intentionally case-sensitive.
func (c *Config) ClientFor(login string) (Client, bool) {
	if c == nil || !c.Enabled {
		return Client{}, false
	}
	client, ok := c.Clients[login]
	return client, ok
}

func validateClient(login string, client Client) error {
	password := strings.TrimSpace(client.Password)
	if password != client.Password || len(password) < 8 || len(password) > 128 {
		return fmt.Errorf("client %q password must be 8..128 characters", login)
	}
	for _, ch := range password {
		if ch < 0x21 || ch > 0x7e {
			return fmt.Errorf("client %q password contains unsafe characters", login)
		}
	}
	wg := client.WG
	if !validWGKey(wg.PeerPublicKey) || !validWGKey(wg.PrivateKey) {
		return fmt.Errorf("client %q wg keys must be 32-byte base64 values", login)
	}
	ip, network, err := net.ParseCIDR(wg.LocalAddress)
	if err != nil || ip.String() != strings.SplitN(wg.LocalAddress, "/", 2)[0] {
		return fmt.Errorf("client %q wg local_address is invalid", login)
	}
	ones, bits := network.Mask.Size()
	if ones != bits {
		return fmt.Errorf("client %q wg local_address must be a host prefix", login)
	}
	return nil
}

func validWGKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == 32
}
