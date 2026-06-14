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

// naivePanelMaxDays caps the expiry pushed to the rixxx-panel, which silently
// turns users with >180-day expiry into permanent ones. We keep the panel expiry
// short and enforce the real expiry from our own store (re-sync / delete on
// lapse). See AddNaiveUser.
const naivePanelMaxDays = 179

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
	capTime := time.Now().Add(naivePanelMaxDays * 24 * time.Hour)
	exp := realExpiry
	if exp.After(capTime) {
		exp = capTime
	}
	body, _ := json.Marshal(map[string]string{
		"username": NaivePrefix + login, "password": pass,
		"expiresAt": exp.UTC().Format("2006-01-02T15:04:05Z"),
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
