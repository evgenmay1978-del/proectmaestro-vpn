// Package rqlite provides the narrow, fail-closed HTTP client used by the
// MaestroVPN control plane. Mutating requests are never replayed by this
// package because a transport failure can leave their outcome unknown.
package rqlite

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultTimeout          = 15 * time.Second
	defaultMaxResponseBytes = int64(8 << 20)
	defaultMaxBackupBytes   = int64(4 << 30)
)

var (
	errResponseTooLarge     = errors.New("response exceeds configured limit")
	errMalformedResponse    = errors.New("malformed response")
	errUnexpectedResultCount = errors.New("unexpected result count")
	errServerRejected       = errors.New("server rejected request")
)

// Consistency is an rqlite read consistency level.
type Consistency string

const (
	// Linearizable returns data at least as recent as the latest committed write.
	Linearizable Consistency = "linearizable"
	// Strong routes the read through the Raft log. It is retained for migration
	// verification and tests; normal production reads use Linearizable.
	Strong Consistency = "strong"
)

// Statement is one parameterized SQL statement.
type Statement struct {
	SQL  string
	Args []any
}

// Result is the associative response for one statement.
type Result struct {
	LastInsertID int64
	RowsAffected int64
	Types        map[string]string
	Rows         []map[string]any
}

// StatementError reports an error returned for one statement inside an
// otherwise successful HTTP response. It intentionally omits SQL and args.
type StatementError struct {
	Index   int
	Message string
}

func (e *StatementError) Error() string {
	return fmt.Sprintf("rqlite: statement %d failed: %s", e.Index, e.Message)
}

// TransportError represents an HTTP, protocol, response-limit, or I/O failure.
// UnknownOutcome is true for Request failures because the write may have been
// committed before the failure became visible to the client.
type TransportError struct {
	Operation      string
	StatusCode     int
	UnknownOutcome bool
	Err            error
}

func (e *TransportError) Error() string {
	operation := e.Operation
	if operation == "" {
		operation = "operation"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("rqlite: %s failed with HTTP %d", operation, e.StatusCode)
	}
	return fmt.Sprintf("rqlite: %s transport failed", operation)
}

// Unwrap preserves context cancellation and timeout inspection without
// including endpoint URLs, credentials, certificate paths, or response bodies
// in the printable error.
func (e *TransportError) Unwrap() error { return e.Err }

// Config defines cluster endpoints and optional HTTP mTLS/Basic Auth.
type Config struct {
	Endpoints []string

	Username string
	Password string
	CAFile   string
	CertFile string
	KeyFile  string

	Timeout          time.Duration
	MaxResponseBytes int64
	MaxBackupBytes   int64
}

// RQLite is the narrow interface consumed by the control-plane store.
type RQLite interface {
	Request(context.Context, Consistency, bool, ...Statement) ([]Result, error)
	QueryLinearizable(context.Context, ...Statement) ([]Result, error)
	QueryStrong(context.Context, ...Statement) ([]Result, error)
	Backup(context.Context, io.Writer) error
}

// Client talks to a fixed set of rqlite HTTP voters.
type Client struct {
	endpoints []*url.URL
	username  string
	password  string
	http      *http.Client

	maxResponseBytes int64
	maxBackupBytes   int64
	next             atomic.Uint64
}

// New validates all configuration and builds an HTTP client which never
// follows redirects. TLS material paths and credentials are never rendered in
// returned errors.
func New(cfg Config) (*Client, error) {
	endpoints, err := parseEndpoints(cfg.Endpoints)
	if err != nil {
		return nil, err
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("rqlite: Basic Auth requires username and password")
	}
	if cfg.Timeout < 0 || cfg.MaxResponseBytes < 0 || cfg.MaxBackupBytes < 0 {
		return nil, errors.New("rqlite: timeout and size limits cannot be negative")
	}

	tlsConfig, err := loadTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	maxBackupBytes := cfg.MaxBackupBytes
	if maxBackupBytes == 0 {
		maxBackupBytes = defaultMaxBackupBytes
	}

	return &Client{
		endpoints: endpoints,
		username:  cfg.Username,
		password:  cfg.Password,
		http: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maxResponseBytes: maxResponseBytes,
		maxBackupBytes:   maxBackupBytes,
	}, nil
}

func parseEndpoints(rawEndpoints []string) ([]*url.URL, error) {
	if len(rawEndpoints) == 0 {
		return nil, errors.New("rqlite: at least one endpoint is required")
	}
	endpoints := make([]*url.URL, 0, len(rawEndpoints))
	seen := make(map[string]struct{}, len(rawEndpoints))
	for _, raw := range rawEndpoints {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Path != "" && parsed.Path != "/") {
			return nil, errors.New("rqlite: endpoint is invalid")
		}
		parsed.Path = ""
		parsed.RawPath = ""
		normalized := parsed.String()
		if _, exists := seen[normalized]; exists {
			return nil, errors.New("rqlite: duplicate endpoint")
		}
		seen[normalized] = struct{}{}
		endpoints = append(endpoints, parsed)
	}
	return endpoints, nil
}

func loadTLSConfig(cfg Config) (*tls.Config, error) {
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, errors.New("rqlite: client certificate and key must be configured together")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, errors.New("rqlite: cannot load CA certificate")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("rqlite: cannot load system CA certificates")
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("rqlite: CA certificate is invalid")
		}
		tlsConfig.RootCAs = roots
	}
	if cfg.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, errors.New("rqlite: client certificate or key is invalid")
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}

// Request sends one non-retriable unified request to one selected endpoint.
// Callers resolve an unknown write outcome through a separately keyed,
// linearizable operation-row read.
func (c *Client) Request(
	ctx context.Context,
	level Consistency,
	transaction bool,
	statements ...Statement,
) ([]Result, error) {
	endpoint := c.endpoints[c.startIndex()]
	return c.requestAt(ctx, endpoint, level, transaction, true, statements)
}

// QueryLinearizable may try each configured endpoint once because it is
// read-only and remains bounded by the supplied context and client timeout.
func (c *Client) QueryLinearizable(
	ctx context.Context,
	statements ...Statement,
) ([]Result, error) {
	return c.query(ctx, Linearizable, statements)
}

// QueryStrong is reserved for migration verification and explicit tests.
func (c *Client) QueryStrong(
	ctx context.Context,
	statements ...Statement,
) ([]Result, error) {
	return c.query(ctx, Strong, statements)
}

func (c *Client) query(
	ctx context.Context,
	level Consistency,
	statements []Statement,
) ([]Result, error) {
	start := c.startIndex()
	var lastErr error
	for offset := range c.endpoints {
		endpoint := c.endpoints[(start+offset)%len(c.endpoints)]
		results, err := c.requestAt(ctx, endpoint, level, false, false, statements)
		if err == nil {
			return results, nil
		}
		var statementErr *StatementError
		if errors.As(err, &statementErr) || ctx.Err() != nil {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) startIndex() int {
	return int((c.next.Add(1) - 1) % uint64(len(c.endpoints)))
}

func (c *Client) requestAt(
	ctx context.Context,
	endpoint *url.URL,
	level Consistency,
	transaction bool,
	unknownOutcome bool,
	statements []Statement,
) ([]Result, error) {
	if level != Linearizable && level != Strong {
		return nil, errors.New("rqlite: unsupported consistency level")
	}
	payload, err := encodeStatements(statements)
	if err != nil {
		return nil, err
	}

	requestURL := *endpoint
	requestURL.Path = "/db/request"
	query := requestURL.Query()
	query.Set("associative", "true")
	query.Set("level", string(level))
	if transaction {
		query.Set("transaction", "true")
	}
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		requestURL.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, &TransportError{
			Operation:      "request",
			UnknownOutcome: unknownOutcome,
			Err:            err,
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	c.authorize(request)

	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, &TransportError{
			Operation:      "request",
			UnknownOutcome: unknownOutcome,
			Err:            err,
		}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &TransportError{
			Operation:      "request",
			StatusCode:     response.StatusCode,
			UnknownOutcome: unknownOutcome,
		}
	}

	body, err := readBounded(response.Body, c.maxResponseBytes)
	if err != nil {
		return nil, &TransportError{
			Operation:      "request response",
			UnknownOutcome: unknownOutcome,
			Err:            err,
		}
	}
	results, err := decodeResponse(body, len(statements))
	if err != nil {
		var statementErr *StatementError
		if errors.As(err, &statementErr) {
			return nil, statementErr
		}
		return nil, &TransportError{
			Operation:      "request response",
			UnknownOutcome: unknownOutcome,
			Err:            err,
		}
	}
	return results, nil
}

func encodeStatements(statements []Statement) ([]byte, error) {
	if len(statements) == 0 {
		return nil, errors.New("rqlite: at least one statement is required")
	}
	payload := make([][]any, len(statements))
	for index, statement := range statements {
		if strings.TrimSpace(statement.SQL) == "" {
			return nil, fmt.Errorf("rqlite: statement %d has empty SQL", index)
		}
		encoded := make([]any, 1+len(statement.Args))
		encoded[0] = statement.SQL
		copy(encoded[1:], statement.Args)
		payload[index] = encoded
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("rqlite: cannot encode statements")
	}
	return data, nil
}

type wireResponse struct {
	Results []wireResult `json:"results"`
	Error   string       `json:"error"`
}

type wireResult struct {
	LastInsertID int64            `json:"last_insert_id"`
	RowsAffected int64            `json:"rows_affected"`
	Types        map[string]string `json:"types"`
	Rows         []map[string]any `json:"rows"`
	Error        string           `json:"error"`
}

func decodeResponse(body []byte, statementCount int) ([]Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var response wireResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, errMalformedResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errMalformedResponse
	}
	if response.Error != "" {
		return nil, errServerRejected
	}
	for index, result := range response.Results {
		if result.Error != "" {
			return nil, &StatementError{Index: index, Message: result.Error}
		}
	}
	if len(response.Results) != statementCount {
		return nil, errUnexpectedResultCount
	}

	results := make([]Result, len(response.Results))
	for index, result := range response.Results {
		results[index] = Result{
			LastInsertID: result.LastInsertID,
			RowsAffected: result.RowsAffected,
			Types:        result.Types,
			Rows:         result.Rows,
		}
	}
	return results, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errResponseTooLarge
	}
	return body, nil
}

// Backup streams a DELETE-journal-mode SQLite backup from one endpoint. It is
// deliberately not retried after partial output.
func (c *Client) Backup(ctx context.Context, writer io.Writer) error {
	if writer == nil {
		return errors.New("rqlite: backup writer is required")
	}
	endpoint := c.endpoints[c.startIndex()]
	requestURL := *endpoint
	requestURL.Path = "/db/backup"
	query := requestURL.Query()
	query.Set("fmt", "delete")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return &TransportError{Operation: "backup", Err: err}
	}
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return &TransportError{Operation: "backup", Err: err}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &TransportError{Operation: "backup", StatusCode: response.StatusCode}
	}

	limited := &io.LimitedReader{R: response.Body, N: c.maxBackupBytes + 1}
	written, err := io.Copy(writer, limited)
	if err != nil {
		return &TransportError{Operation: "backup", Err: err}
	}
	if written > c.maxBackupBytes {
		return &TransportError{Operation: "backup", Err: errResponseTooLarge}
	}
	return nil
}

func (c *Client) authorize(request *http.Request) {
	if c.username != "" {
		request.SetBasicAuth(c.username, c.password)
	}
}
