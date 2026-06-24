// Package xui provisions VLESS clients in the 3x-ui panel on server 1 via its
// HTTP API. Two non-obvious facts learned from the live panel:
//
//   - 3x-ui rejects requests whose Host header does not match its configured
//     webDomain (wapmixx.ru) with 403 — so Config.Host MUST be set and is sent
//     as the Host header even when dialing 127.0.0.1.
//   - The admin password is bcrypt-hashed in x-ui.db (not recoverable), so the
//     operator supplies the plaintext admin credentials in the backend config.
//
// The client logs in (cookie session) and calls /panel/api/inbounds/*.
package xui

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// VLESSClient is a 3x-ui client object (the fields the API expects).
type VLESSClient struct {
	ID         string `json:"id"`         // uuid
	Email      string `json:"email"`      // unique label
	Flow       string `json:"flow"`       // e.g. xtls-rprx-vision
	TotalGB    int64  `json:"totalGB"`    // 0 = unlimited
	ExpiryTime int64  `json:"expiryTime"` // unix millis; 0 = never
	Enable     bool   `json:"enable"`
	SubID      string `json:"subId"`   // subscription id → /sub/<subId>
	LimitIP    int    `json:"limitIp"` // 0 = unlimited devices
}

// ExistingClient is the subset of a 3x-ui client looked up by email — used to
// activate an already-existing panel customer in the app by their login.
type ExistingClient struct {
	UUID       string
	Email      string
	ExpiryTime int64  // unix millis; 0 = never
	SubID      string // the 3x-ui subId — for a bot-sold client this is the :2096 sub id; preserve it
}

// Config configures the 3x-ui client. Creds are operator-supplied.
//
// 3x-ui v3.x CSRF-protects the login form, so the supported path is a **Bearer
// API token** (created in the panel → "API token"): set Token and every request
// carries `Authorization: Bearer <token>`, which the panel accepts directly. The
// existing vpn_bot uses exactly this. Username/Password remain for the legacy
// cookie-login path but are not the recommended route.
type Config struct {
	BaseURL  string // e.g. https://wapmixx.ru:2053/33NXEZDAMyG5MLLw3x  (no trailing slash)
	Host     string // Host header the panel requires, e.g. wapmixx.ru
	Token    string // Bearer API token (preferred)
	Username string // legacy cookie login
	Password string
	Insecure bool // skip TLS verify (dialing the domain cert)
}

// Client talks to one 3x-ui panel.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a client with a cookie jar for the session.
func New(cfg Config) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("xui: cookie jar: %w", err)
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Jar:     jar,
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure}, //nolint:gosec // domain cert on loopback
			},
		},
	}, nil
}

// do issues a request with the required Host header.
func (c *Client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.cfg.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("xui: build request: %w", err)
	}
	if c.cfg.Host != "" {
		req.Host = c.cfg.Host
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xui: %s %s: %w", method, path, err)
	}
	return resp, nil
}

type apiResp struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

// Login establishes a session for the legacy cookie path. With a Bearer Token
// configured it is a no-op (the token authorizes every request directly).
func (c *Client) Login() error {
	if c.cfg.Token != "" {
		return nil
	}
	form := url.Values{"username": {c.cfg.Username}, "password": {c.cfg.Password}}
	resp, err := c.do(http.MethodPost, "/login", strings.NewReader(form.Encode()),
		"application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r apiResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("xui: login decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if !r.Success {
		return fmt.Errorf("xui: login failed: %s", r.Msg)
	}
	return nil
}

// AddClient adds a VLESS client. This panel (3x-ui v3.2.8) uses the client-centric
// API `/panel/api/clients/add` with a {client, inboundIds} body — NOT the older
// `/panel/api/inbounds/addClient`. Matches the working vpn_bot exactly.
func (c *Client) AddClient(inboundID int, client VLESSClient) error {
	return c.postJSON("/panel/api/clients/add", map[string]any{
		"client": map[string]any{
			"id": client.ID, "email": client.Email, "enable": client.Enable,
			"expiryTime": client.ExpiryTime, "totalGB": client.TotalGB,
			"subId": client.SubID, "limitIp": client.LimitIP,
			"flow": client.Flow, "tgId": 0, "reset": 0,
		},
		"inboundIds": []int{inboundID},
	})
}

// UpdateClient updates a client (used to extend expiry) via
// `/panel/api/clients/update/{email}` with the client record (flat, not wrapped).
func (c *Client) UpdateClient(_ int, _ string, client VLESSClient) error {
	return c.postJSON("/panel/api/clients/update/"+url.PathEscape(client.Email), map[string]any{
		"id": client.ID, "email": client.Email, "enable": client.Enable,
		"expiryTime": client.ExpiryTime, "totalGB": client.TotalGB,
		"subId": client.SubID, "limitIp": client.LimitIP, "flow": client.Flow,
	})
}

// GetClient looks up an existing client by email (login). Returns nil if there is
// no such client. Used for the "activate existing panel customer by login" path.
func (c *Client) GetClient(email string) (*ExistingClient, error) {
	resp, err := c.do(http.MethodGet, "/panel/api/clients/get/"+url.PathEscape(email), nil, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r apiResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("xui: get client decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if len(r.Obj) == 0 || string(r.Obj) == "null" {
		return nil, nil // no such client
	}
	// obj is either {client:{...}} or the client record directly.
	var wrap struct {
		Client json.RawMessage `json:"client"`
	}
	_ = json.Unmarshal(r.Obj, &wrap)
	raw := wrap.Client
	if len(raw) == 0 {
		raw = r.Obj
	}
	var rec struct {
		Email      string `json:"email"`
		UUID       string `json:"uuid"`
		ExpiryTime int64  `json:"expiryTime"`
		SubID      string `json:"subId"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Email == "" {
		return nil, nil
	}
	return &ExistingClient{UUID: rec.UUID, Email: rec.Email, ExpiryTime: rec.ExpiryTime, SubID: rec.SubID}, nil
}

// postJSON POSTs a JSON body and expects {success:true}.
func (c *Client) postJSON(path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("xui: marshal %s: %w", path, err)
	}
	resp, err := c.do(http.MethodPost, path, bytes.NewReader(data), "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r apiResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("xui: %s decode (HTTP %d): %w", path, resp.StatusCode, err)
	}
	if !r.Success {
		return fmt.Errorf("xui: %s failed: %s", path, r.Msg)
	}
	return nil
}

func (c *Client) postExpectSuccess(path string, form url.Values) error {
	resp, err := c.do(http.MethodPost, path, strings.NewReader(form.Encode()),
		"application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r apiResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("xui: %s decode (HTTP %d): %w", path, resp.StatusCode, err)
	}
	if !r.Success {
		return fmt.Errorf("xui: %s failed: %s", path, r.Msg)
	}
	return nil
}
