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
	"strconv"
	"strings"
	"time"
)

const (
	DesiredPath         = "/v1/desired"
	ReceiptPath         = "/v1/receipt"
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
		actionKey == "" || strings.TrimSpace(actionKey) != actionKey || strings.ContainsAny(actionKey, "\x00\r\n\t") {
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

type receipt struct {
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

func decodeReceipt(body io.Reader, actionKey string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxResponseBytes {
		return nil, ErrDeliveryUnknown
	}
	var value receipt
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

func validBaseURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}
