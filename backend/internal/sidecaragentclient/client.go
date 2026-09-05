package sidecaragentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DesiredPath         = "/v1/desired"
	ReceiptPath         = "/v1/receipt"
	UsagePath           = "/v1/usage"
	ActionKeyHeader     = "X-Maestro-Action-Key"
	DesiredSHA256Header = "X-Maestro-Desired-SHA256"
	MaxRequestBytes     = 1 << 20
	maxResponseBytes    = 64 << 10
)

type definitelyNotSentError string

func (err definitelyNotSentError) Error() string { return string(err) }

func (definitelyNotSentError) DefinitelyNotSent() bool { return true }

var (
	ErrInvalidRequest        = errors.New("sidecar agent client: invalid request")
	ErrBeforeSend      error = definitelyNotSentError("sidecar agent client: request was not sent")
	ErrDeliveryUnknown       = errors.New("sidecar agent client: delivery outcome is unknown")
	ErrReceiptNotFound       = errors.New("sidecar agent client: receipt not found")
	ErrUsageNotFound         = errors.New("sidecar agent client: usage snapshot not found")
	ErrStaleGeneration       = errors.New("sidecar agent client: stale desired generation")
	ErrRequestRejected       = errors.New("sidecar agent client: request rejected")
)

type Config struct {
	BaseURL              string
	ServerName           string
	CAFile               string
	CertFile             string
	KeyFile              string
	RequestTimeout       time.Duration
	ReceiptLookupTimeout time.Duration
}

type Client struct {
	baseURL        string
	requestTimeout time.Duration
	lookupTimeout  time.Duration
	httpClient     *http.Client
}

type Receipt struct {
	ActionKey            string    `json:"action_key"`
	OriginID             string    `json:"origin_id"`
	ReleaseID            string    `json:"release_id"`
	XrayProcessBootID    string    `json:"xray_process_boot_id"`
	ConfigDigest         string    `json:"config_digest"`
	DesiredGeneration    int64     `json:"desired_generation"`
	ManagedUserSetDigest string    `json:"managed_user_set_digest"`
	AppliedAt            time.Time `json:"applied_at"`
	ExpiresAt            time.Time `json:"expires_at"`
}

type UsageUser struct {
	Email         string `json:"email"`
	UplinkBytes   uint64 `json:"uplink_bytes"`
	DownlinkBytes uint64 `json:"downlink_bytes"`
}

type UsageSnapshot struct {
	Receipt          Receipt     `json:"receipt"`
	SampledAt        time.Time   `json:"sampled_at"`
	Users            []UsageUser `json:"users"`
	UnavailableUsers []string    `json:"unavailable_users"`
}

type usageUserWire struct {
	Email         string  `json:"email"`
	UplinkBytes   *uint64 `json:"uplink_bytes"`
	DownlinkBytes *uint64 `json:"downlink_bytes"`
}

type usageSnapshotWire struct {
	Receipt          Receipt         `json:"receipt"`
	SampledAt        time.Time       `json:"sampled_at"`
	Users            []usageUserWire `json:"users"`
	UnavailableUsers []string        `json:"unavailable_users"`
}

func New(config Config) (*Client, error) {
	baseURL, ok := validBaseURL(config.BaseURL)
	if !ok || strings.TrimSpace(config.ServerName) == "" || strings.TrimSpace(config.CAFile) == "" ||
		strings.TrimSpace(config.CertFile) == "" || strings.TrimSpace(config.KeyFile) == "" ||
		config.RequestTimeout <= 0 || config.ReceiptLookupTimeout <= 0 {
		return nil, errors.New("sidecar agent client: incomplete mTLS configuration")
	}
	caPEM, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("sidecar agent client: load CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("sidecar agent client: parse CA")
	}
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, errors.New("sidecar agent client: load client certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: config.ServerName,
		Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h2", "http/1.1"},
	}}
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("sidecar agent client: redirects are not allowed")
		},
	}
	return newWithHTTPClient(baseURL, config.RequestTimeout, config.ReceiptLookupTimeout, httpClient), nil
}

func newWithHTTPClient(baseURL string, requestTimeout, lookupTimeout time.Duration, httpClient *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), requestTimeout: requestTimeout,
		lookupTimeout: lookupTimeout, httpClient: httpClient,
	}
}

func (client *Client) Post(ctx context.Context, desired []byte) ([]byte, error) {
	if client == nil || client.httpClient == nil || ctx == nil || client.requestTimeout <= 0 ||
		client.lookupTimeout <= 0 || len(desired) == 0 || len(desired) > MaxRequestBytes {
		return nil, ErrInvalidRequest
	}
	var binding struct {
		NodeID     string `json:"node_id"`
		Generation int64  `json:"generation"`
	}
	if err := json.Unmarshal(desired, &binding); err != nil || binding.NodeID == "" ||
		strings.TrimSpace(binding.NodeID) != binding.NodeID || binding.Generation < 1 {
		return nil, ErrInvalidRequest
	}
	digest := sha256.Sum256(desired)
	desiredSHA := hex.EncodeToString(digest[:])
	actionKey := binding.NodeID + ":" + strconv.FormatInt(binding.Generation, 10) + ":" + desiredSHA

	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	wroteRequest := false
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest = true }}
	requestContext = httptrace.WithClientTrace(requestContext, trace)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, client.baseURL+DesiredPath, bytes.NewReader(desired))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ActionKeyHeader, actionKey)
	request.Header.Set(DesiredSHA256Header, desiredSHA)
	response, err := client.httpClient.Do(request)
	if err != nil {
		if !wroteRequest {
			return nil, ErrBeforeSend
		}
		receipt, lookupErr := client.LookupReceipt(context.WithoutCancel(ctx), actionKey)
		if lookupErr == nil {
			return receipt, nil
		}
		return nil, ErrDeliveryUnknown
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return decodeReceipt(response.Body, actionKey)
	case http.StatusConflict:
		return nil, ErrStaleGeneration
	default:
		return nil, ErrRequestRejected
	}
}

// LookupReceipt performs one read-only exact-action-key lookup.
func (client *Client) LookupReceipt(ctx context.Context, actionKey string) ([]byte, error) {
	if client == nil || client.httpClient == nil || ctx == nil || client.lookupTimeout <= 0 ||
		!validActionKey(actionKey) {
		return nil, ErrInvalidRequest
	}
	lookupContext, cancel := context.WithTimeout(ctx, client.lookupTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(lookupContext, http.MethodGet, client.baseURL+ReceiptPath, nil)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	request.Header.Set(ActionKeyHeader, actionKey)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, ErrDeliveryUnknown
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return decodeReceipt(response.Body, actionKey)
	case http.StatusNotFound:
		return nil, ErrReceiptNotFound
	default:
		return nil, ErrDeliveryUnknown
	}
}

// LookupUsage performs one authenticated read-only counter snapshot for the
// exact currently applied action key.
func (client *Client) LookupUsage(ctx context.Context, actionKey string) (UsageSnapshot, error) {
	if client == nil || client.httpClient == nil || ctx == nil || client.lookupTimeout <= 0 ||
		!validActionKey(actionKey) {
		return UsageSnapshot{}, ErrInvalidRequest
	}
	lookupContext, cancel := context.WithTimeout(ctx, client.lookupTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(lookupContext, http.MethodGet, client.baseURL+UsagePath, nil)
	if err != nil {
		return UsageSnapshot{}, ErrInvalidRequest
	}
	request.Header.Set(ActionKeyHeader, actionKey)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return UsageSnapshot{}, ErrDeliveryUnknown
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return decodeUsage(response.Body, actionKey)
	case http.StatusNotFound:
		return UsageSnapshot{}, ErrUsageNotFound
	default:
		return UsageSnapshot{}, ErrDeliveryUnknown
	}
}

func decodeReceipt(body io.Reader, actionKey string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxResponseBytes {
		return nil, ErrDeliveryUnknown
	}
	var value Receipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrDeliveryUnknown
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || value.ActionKey != actionKey {
		return nil, ErrDeliveryUnknown
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrDeliveryUnknown
	}
	return canonical, nil
}

func decodeUsage(body io.Reader, actionKey string) (UsageSnapshot, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxResponseBytes {
		return UsageSnapshot{}, ErrDeliveryUnknown
	}
	var wire usageSnapshotWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.Users == nil || wire.UnavailableUsers == nil {
		return UsageSnapshot{}, ErrDeliveryUnknown
	}
	value := UsageSnapshot{
		Receipt:          wire.Receipt,
		SampledAt:        wire.SampledAt,
		Users:            make([]UsageUser, 0, len(wire.Users)),
		UnavailableUsers: make([]string, len(wire.UnavailableUsers)),
	}
	copy(value.UnavailableUsers, wire.UnavailableUsers)
	for _, user := range wire.Users {
		if user.UplinkBytes == nil || user.DownlinkBytes == nil {
			return UsageSnapshot{}, ErrDeliveryUnknown
		}
		value.Users = append(value.Users, UsageUser{
			Email: user.Email, UplinkBytes: *user.UplinkBytes, DownlinkBytes: *user.DownlinkBytes,
		})
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		!validUsageSnapshot(value, actionKey) {
		return UsageSnapshot{}, ErrDeliveryUnknown
	}
	return value, nil
}

func validUsageSnapshot(value UsageSnapshot, actionKey string) bool {
	if value.Receipt.ActionKey != actionKey || value.Receipt.OriginID == "" ||
		value.Receipt.ReleaseID == "" || value.Receipt.XrayProcessBootID == "" ||
		value.Receipt.ConfigDigest == "" || value.Receipt.DesiredGeneration < 1 ||
		value.Receipt.ManagedUserSetDigest == "" || value.Receipt.AppliedAt.IsZero() ||
		value.Receipt.ExpiresAt.IsZero() || value.SampledAt.IsZero() ||
		value.Receipt.AppliedAt.After(value.SampledAt) || !value.Receipt.ExpiresAt.After(value.SampledAt) ||
		value.Users == nil || value.UnavailableUsers == nil {
		return false
	}
	seen := make(map[string]struct{}, len(value.Users)+len(value.UnavailableUsers))
	previous := ""
	for index, user := range value.Users {
		if !validUsageEmail(user.Email) || (index > 0 && user.Email <= previous) {
			return false
		}
		seen[user.Email] = struct{}{}
		previous = user.Email
	}
	previous = ""
	for index, email := range value.UnavailableUsers {
		if !validUsageEmail(email) || (index > 0 && email <= previous) {
			return false
		}
		if _, exists := seen[email]; exists {
			return false
		}
		seen[email] = struct{}{}
		previous = email
	}
	managedUsers := make([]string, 0, len(seen))
	for email := range seen {
		managedUsers = append(managedUsers, email)
	}
	sort.Strings(managedUsers)
	encoded, err := json.Marshal(managedUsers)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(encoded)
	return value.Receipt.ManagedUserSetDigest == hex.EncodeToString(digest[:])
}

func validActionKey(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n\t")
}

func validUsageEmail(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n\t")
}

func validBaseURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}
