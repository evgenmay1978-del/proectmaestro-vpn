package server2

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// NaivePrefix marks naive users created for the TV app. The backend ONLY ever
// touches users with this prefix — the pre-existing naive customers managed by
// the server-2 bot are never read-modified-deleted here.
const NaivePrefix = "mtv_"

// naivePanelMaxDays is the panel's accepted upper bound (1..3650). The earlier
// belief that ">180 days = unlimited" was a misdiagnosis: the panel API reads
// the `expireDays` NUMBER (not an `expiresAt` date) and computes the date itself,
// correctly for any 1..3650. Sending `expiresAt` left expireDays empty → the
// panel treated the user as permanent. We send expireDays, so the panel shows
// the right days for any duration.
const naivePanelMaxDays = 3650

// daysUntil returns whole days from now to t, clamped to the panel's 1..3650.
func daysUntil(t time.Time) int {
	d := int(time.Until(t).Hours()/24) + 1 // round up the partial day
	if d < 1 {
		d = 1
	}
	if d > naivePanelMaxDays {
		d = naivePanelMaxDays
	}
	return d
}

// naiveClient logs in to the rixxx-panel (reachable at Config.NaivePanelURL, e.g.
// http://85.137.166.237:8080) and returns a cookie-authenticated HTTP client.
func (c *Client) naiveClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("server2: naive cookie jar: %w", err)
	}
	hc := &http.Client{
		Jar:       jar,
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // owner's panel
	}
	body, _ := json.Marshal(map[string]string{"username": c.cfg.NaivePanelUser, "password": c.cfg.NaivePanelPass})
	resp, err := hc.Post(c.cfg.NaivePanelURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("server2: naive login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server2: naive login HTTP %d", resp.StatusCode)
	}
	return hc, nil
}

// AddNaiveUser creates (or refreshes) an app naive user. The username carries
// NaivePrefix. expiry is capped to the panel-safe window; the real expiry is
// enforced by the caller's store + sync.
func (c *Client) AddNaiveUser(login, pass string, realExpiry time.Time) error {
	hc, err := c.naiveClient()
	if err != nil {
		return err
	}
	// Send expireDays (the number the panel API actually reads) so the panel
	// computes + displays the correct expiry for ANY duration.
	body, _ := json.Marshal(map[string]any{
		"username": NaivePrefix + login, "password": pass, "expireDays": daysUntil(realExpiry),
	})
	resp, err := hc.Post(c.cfg.NaivePanelURL+"/api/naive/users", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("server2: add naive user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server2: add naive user HTTP %d", resp.StatusCode)
	}
	return nil
}

// SetNaiveExpiry updates an app naive user's expiry (PATCH expireDays) so the
// panel shows the correct remaining days. Used on renewal and for the one-time
// re-sync of existing customers from the bot's real expiry.
func (c *Client) SetNaiveExpiry(login string, realExpiry time.Time) error {
	hc, err := c.naiveClient()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"expireDays": daysUntil(realExpiry)})
	req, _ := http.NewRequest(http.MethodPatch, c.cfg.NaivePanelURL+"/api/naive/users/"+NaivePrefix+login, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("server2: patch naive expiry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server2: patch naive expiry HTTP %d", resp.StatusCode)
	}
	return nil
}

// DelNaiveUser removes an app naive user (prefix enforced).
func (c *Client) DelNaiveUser(login string) error {
	hc, err := c.naiveClient()
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodDelete, c.cfg.NaivePanelURL+"/api/naive/users/"+NaivePrefix+login, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("server2: delete naive user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("server2: delete naive user HTTP %d", resp.StatusCode)
	}
}

// ListAppNaiveUsers returns the logins (without prefix) of app-managed naive
// users, so the caller can reconcile without ever seeing pre-existing customers.
func (c *Client) ListAppNaiveUsers() ([]string, error) {
	hc, err := c.naiveClient()
	if err != nil {
		return nil, err
	}
	resp, err := hc.Get(c.cfg.NaivePanelURL + "/api/naive/users")
	if err != nil {
		return nil, fmt.Errorf("server2: list naive users: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server2: list naive users HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Users []struct {
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("server2: parse naive users: %w", err)
	}
	var out []string
	for _, u := range parsed.Users {
		if strings.HasPrefix(u.Username, NaivePrefix) {
			out = append(out, strings.TrimPrefix(u.Username, NaivePrefix))
		}
	}
	return out, nil
}
