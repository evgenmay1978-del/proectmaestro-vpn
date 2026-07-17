// Package vkturnprov provisions WDTT clients on the isolated canary wdtt-server.
// It owns the server side of "add a login from the panel": generate a connection
// password and a WireGuard identity, pre-create the device entries in the canary's
// root-only passwords.json (one per known app install, all sharing the identity, so
// whichever device connects first binds and just works), and restart the container —
// the pinned wdtt-server derives DTLS wrap keys from the password list only at
// startup and has no reload signal. The client half of the same identity is stored
// by the caller in vkturnconf, so /sub and the server always agree on keys.
//
// Fail-closed like vkturnconf: an unset directory returns a nil Provisioner (panel
// endpoint off), a failed restart restores the previous passwords.json and restarts
// again, so a botched edit can not leave existing WDTT users on a dead server.
package vkturnprov

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

// execRunner is the production Runner: run the command, fold stderr into the
// error so a docker failure surfaces its actual message in the panel.
func execRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Runner executes an external command and returns its combined output. It exists
// so tests can fake docker; production uses execRunner.
type Runner func(name string, args ...string) (string, error)

// Client is the freshly provisioned identity handed back to the caller, matching
// what vkturnconf stores per login (the password plus subgen.VKTurnCreds fields).
type Client struct {
	Password        string
	PrivateKey      string // client WireGuard private key (base64)
	ServerPublicKey string // wdtt-server WireGuard public key (base64)
	LocalAddress    string // client tunnel address, host prefix ("10.66.66.5/32")
}

type Provisioner struct {
	mu        sync.Mutex
	dir       string // canary config dir: passwords.json, wg-keys.dat
	container string // docker container restarted after password-list changes
	relayIP   string // bridge IP the host UDP relay targets; "" skips the check
	run       Runner
	sleep     func(time.Duration)
}

// Defaults for the S1 isolated canary contour (ops/wdtt-canary). Overridable via
// the maestro-panel environment for a future non-canary deployment.
const (
	DefaultContainer = "maestro-wdtt-canary-isolated"
	DefaultRelayIP   = "172.17.0.2"
)

// Open returns a Provisioner rooted at dir, or nil when dir is empty (feature
// off — mirrors vkturnconf.OpenStore semantics). container/relayIP fall back to
// the canary defaults when blank.
func Open(dir, container, relayIP string) *Provisioner {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if strings.TrimSpace(container) == "" {
		container = DefaultContainer
	}
	if strings.TrimSpace(relayIP) == "" {
		relayIP = DefaultRelayIP
	}
	return &Provisioner{dir: dir, container: container, relayIP: relayIP, run: execRunner, sleep: time.Sleep}
}

// SetExecHooks overrides external-command execution and sleeping — tests only,
// so the suite never touches real docker or waits real seconds.
func (p *Provisioner) SetExecHooks(run Runner, sleep func(time.Duration)) {
	if run != nil {
		p.run = run
	}
	if sleep != nil {
		p.sleep = sleep
	}
}

// ---- upstream wdtt-server database (passwords.json) ----
// Struct tags mirror the pinned linux-server exactly; every other top-level field
// (main_password, admin_id, bot_token, future additions) is preserved verbatim
// through raw, so a panel edit can never strip server-owned settings.

type passwordEntry struct {
	DeviceID      string `json:"device_id"`
	ExpiresAt     int64  `json:"expires_at"`
	DownBytes     int64  `json:"down_bytes"`
	UpBytes       int64  `json:"up_bytes"`
	VkHash        string `json:"vk_hash,omitempty"`
	Ports         string `json:"ports,omitempty"`
	IsDeactivated bool   `json:"is_deactivated,omitempty"`
}

type clientDevice struct {
	DeviceID string `json:"device_id"`
	IP       string `json:"ip"`
	PrivKey  string `json:"priv_key"`
	PubKey   string `json:"pub_key"`
}

type canaryDB struct {
	raw       map[string]json.RawMessage
	passwords map[string]*passwordEntry
	devices   map[string]*clientDevice
	origBytes []byte // exact on-disk bytes at load, for rollback
}

func (p *Provisioner) dbPath() string { return filepath.Join(p.dir, "passwords.json") }

func (p *Provisioner) loadDB() (*canaryDB, error) {
	b, err := os.ReadFile(p.dbPath())
	if err != nil {
		return nil, fmt.Errorf("canary passwords.json: %w", err)
	}
	db := &canaryDB{raw: map[string]json.RawMessage{}, origBytes: b}
	if err := json.Unmarshal(b, &db.raw); err != nil {
		return nil, fmt.Errorf("canary passwords.json: %w", err)
	}
	db.passwords = map[string]*passwordEntry{}
	if raw, ok := db.raw["passwords"]; ok {
		if err := json.Unmarshal(raw, &db.passwords); err != nil {
			return nil, fmt.Errorf("canary passwords.json passwords: %w", err)
		}
	}
	db.devices = map[string]*clientDevice{}
	if raw, ok := db.raw["devices"]; ok {
		if err := json.Unmarshal(raw, &db.devices); err != nil {
			return nil, fmt.Errorf("canary passwords.json devices: %w", err)
		}
	}
	return db, nil
}

// saveDB writes the mutated database atomically (0600 temp + rename, O_EXCL so a
// stale temp can never relax the mode) and keeps a .bak of the previous content.
func (p *Provisioner) saveDB(db *canaryDB) error {
	pw, err := json.Marshal(db.passwords)
	if err != nil {
		return err
	}
	dev, err := json.Marshal(db.devices)
	if err != nil {
		return err
	}
	db.raw["passwords"] = pw
	db.raw["devices"] = dev
	out, err := json.MarshalIndent(db.raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.dbPath()+".bak", db.origBytes, 0o600); err != nil {
		return fmt.Errorf("canary backup: %w", err)
	}
	return writeFileAtomic(p.dbPath(), out)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// serverPublicKey reads the wdtt-server WireGuard public key: line 2 of
// wg-keys.dat (serverPrivate, serverPublic, clientPrivate, clientPublic — the
// pinned server's loadOrGenerateKeys format).
func (p *Provisioner) serverPublicKey() (string, error) {
	b, err := os.ReadFile(filepath.Join(p.dir, "wg-keys.dat"))
	if err != nil {
		return "", fmt.Errorf("canary wg-keys.dat: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 || !validWGKey(strings.TrimSpace(lines[1])) {
		return "", fmt.Errorf("canary wg-keys.dat: unexpected format")
	}
	return strings.TrimSpace(lines[1]), nil
}

func validWGKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(b) == 32
}

// passChars matches the pinned wdtt-server alphabet (no 0/O/1/l/I lookalikes) so
// panel-issued passwords are indistinguishable from bot-issued ones.
const passChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

func generatePassword() (string, error) {
	out := make([]byte, 16)
	buf := make([]byte, 1)
	for i := range out {
		for {
			if _, err := rand.Read(buf); err != nil {
				return "", err
			}
			// Rejection sampling keeps the distribution uniform over the alphabet.
			if int(buf[0]) < 256-256%len(passChars) {
				out[i] = passChars[int(buf[0])%len(passChars)]
				break
			}
		}
	}
	return string(out), nil
}

func generateKeyPair() (privB64, pubB64 string, err error) {
	priv := make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return "", "", err
	}
	// Standard X25519 clamping — same as wireguard-go / the pinned server.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// nextIP allocates the lowest free address in the server's fixed 10.66.66.0/24
// (.1 is the server, upstream getNextIP scans 2..250 the same way).
func nextIP(devices map[string]*clientDevice) (string, error) {
	used := map[string]bool{}
	for _, d := range devices {
		used[d.IP] = true
	}
	for i := 2; i <= 250; i++ {
		ip := fmt.Sprintf("10.66.66.%d", i)
		if !used[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("no free tunnel addresses in 10.66.66.0/24")
}

// AddClient provisions login on the canary and returns the client half of the
// identity. deviceIDs are the login's known app installs (from the customer
// store); a device entry is pre-created for each with the SAME keys/IP, so the
// password binds to whichever physical device connects first and immediately
// finds a matching WireGuard peer. The password entry itself starts unbound.
func (p *Provisioner) AddClient(login string, deviceIDs []string) (Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ids := dedupeIDs(deviceIDs)
	if len(ids) == 0 {
		return Client{}, fmt.Errorf("login %q has no known app installs", login)
	}
	db, err := p.loadDB()
	if err != nil {
		return Client{}, err
	}
	for _, id := range ids {
		if _, exists := db.devices[id]; exists {
			return Client{}, fmt.Errorf("device %q is already provisioned (another login on the same phone?)", id)
		}
	}
	serverPub, err := p.serverPublicKey()
	if err != nil {
		return Client{}, err
	}
	password, err := generatePassword()
	for err == nil {
		if _, clash := db.passwords[password]; !clash {
			break
		}
		password, err = generatePassword()
	}
	if err != nil {
		return Client{}, err
	}
	priv, pub, err := generateKeyPair()
	if err != nil {
		return Client{}, err
	}
	ip, err := nextIP(db.devices)
	if err != nil {
		return Client{}, err
	}

	db.passwords[password] = &passwordEntry{} // unbound; binds on first GETCONF
	for _, id := range ids {
		db.devices[id] = &clientDevice{DeviceID: id, IP: ip, PrivKey: priv, PubKey: pub}
	}
	if err := p.saveDB(db); err != nil {
		return Client{}, err
	}
	if err := p.restartAndVerify(); err != nil {
		p.rollback(db.origBytes)
		return Client{}, fmt.Errorf("canary restart failed (previous config restored): %w", err)
	}
	return Client{Password: password, PrivateKey: priv, ServerPublicKey: serverPub, LocalAddress: ip + "/32"}, nil
}

// RemoveClient deletes the password entry and every device sharing the client's
// tunnel address. Idempotent: absent entries are not an error, so a half-removed
// login can be removed again.
func (p *Provisioner) RemoveClient(password, localAddress string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ip := strings.SplitN(strings.TrimSpace(localAddress), "/", 2)[0]
	db, err := p.loadDB()
	if err != nil {
		return err
	}
	changed := false
	if _, ok := db.passwords[password]; ok && password != "" {
		delete(db.passwords, password)
		changed = true
	}
	for id, dev := range db.devices {
		if ip != "" && dev.IP == ip {
			delete(db.devices, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := p.saveDB(db); err != nil {
		return err
	}
	if err := p.restartAndVerify(); err != nil {
		p.rollback(db.origBytes)
		return fmt.Errorf("canary restart failed (previous config restored): %w", err)
	}
	return nil
}

func dedupeIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// restartAndVerify bounces the container (wrap keys are derived from the password
// list only at startup) and requires it back Running on the SAME bridge IP — the
// host UDP relay forwards to that address statically, so an IP change would
// silently strand every WDTT user even though the container looks healthy.
func (p *Provisioner) restartAndVerify() error {
	if _, err := p.run("docker", "restart", "-t", "5", p.container); err != nil {
		return err
	}
	var last string
	for attempt := 0; attempt < 20; attempt++ {
		// range over .Networks: the legacy top-level .NetworkSettings.IPAddress key
		// is gone on modern Docker (29.x on S1 errors on it), while the per-network
		// map works everywhere; the canary sits on exactly one bridge network.
		out, err := p.run("docker", "inspect", "-f", "{{.State.Running}} {{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", p.container)
		if err == nil {
			fields := strings.Fields(strings.TrimSpace(out))
			if len(fields) == 2 && fields[0] == "true" && (p.relayIP == "" || fields[1] == p.relayIP) {
				return nil
			}
			last = strings.TrimSpace(out)
		} else {
			last = err.Error()
		}
		p.sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container not healthy on %s (last state: %s)", p.relayIP, last)
}

// rollback restores the pre-edit passwords.json and restarts once more, best
// effort — the previous config was known-good, so this is strictly recovery.
func (p *Provisioner) rollback(orig []byte) {
	if err := writeFileAtomic(p.dbPath(), orig); err != nil {
		return
	}
	_, _ = p.run("docker", "restart", "-t", "5", p.container)
}
