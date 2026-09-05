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
	"math"
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
	DesiredPath           = "/v1/desired"
	ReceiptPath           = "/v1/receipt"
	UsagePath             = "/v1/usage"
	UseLeasePath          = "/v1/use-lease"
	LeaseReceiptsPath     = "/v1/lease-receipts"
	LeaseAckPath          = "/v1/lease-ack"
	ActionKeyHeader       = "X-Maestro-Action-Key"
	DesiredSHA256Header   = "X-Maestro-Desired-SHA256"
	MaxRequestBytes       = 1 << 20
	maxResponseBytes      = 64 << 10
	maxUsageResponseBytes = 4 << 20
	maxLeaseUsers         = 4096
	maxLeaseFinalReceipts = 32
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
	Receipt              Receipt               `json:"receipt"`
	SampledAt            time.Time             `json:"sampled_at"`
	Users                []UsageUser           `json:"users"`
	UnavailableUsers     []string              `json:"unavailable_users"`
	LeaseChallenge       *UseLeaseChallenge    `json:"lease_challenge,omitempty"`
	FinalReceipts        []ManagedFinalReceipt `json:"final_receipts,omitempty"`
	HasMoreFinalReceipts bool                  `json:"has_more_final_receipts,omitempty"`
	PendingUseLease      *UseLeaseRequest      `json:"pending_use_lease,omitempty"`
}

// BOOTTIME values are opaque agent-clock coordinates. The backend may add a
// conservative duration to the read start, but never compares them with its own
// clock. The agent independently validates its saved nonce/domain/deadline.
type UseLeaseChallenge struct {
	Schema                int      `json:"schema"`
	Nonce                 string   `json:"nonce"`
	ClockDomain           string   `json:"clock_domain"`
	ReadStartedBoottimeNS int64    `json:"read_started_boottime_ns"`
	MaxDeadlineBoottimeNS int64    `json:"max_deadline_boottime_ns"`
	ManagedUsers          []string `json:"managed_users"`
}

type ManagedControl struct {
	Schema             int    `json:"schema"`
	Operation          string `json:"operation"`
	Email              string `json:"email"`
	BootID             string `json:"boot_id"`
	ConfigDigest       string `json:"config_digest"`
	Generation         uint64 `json:"generation"`
	ClockDomain        string `json:"clock_domain"`
	DeadlineBoottimeNS int64  `json:"deadline_boottime_ns,omitempty"`
}

type ManagedReceipt struct {
	Schema             int     `json:"schema"`
	State              string  `json:"state"`
	Email              string  `json:"email"`
	BootID             string  `json:"boot_id"`
	ConfigDigest       string  `json:"config_digest"`
	Generation         uint64  `json:"generation"`
	ResetSequence      uint64  `json:"reset_sequence"`
	ObservedAt         string  `json:"observed_at"`
	Uplink             *int64  `json:"uplink,omitempty"`
	Downlink           *int64  `json:"downlink,omitempty"`
	ClockDomain        string  `json:"clock_domain"`
	DeadlineBoottimeNS int64   `json:"deadline_boottime_ns,omitempty"`
	LeaseRemainingMS   *uint32 `json:"lease_remaining_ms,omitempty"`
}

type ManagedFinalReceipt struct {
	ReceiptID            string         `json:"receipt_id"`
	ProofSHA256          string         `json:"proof_sha256"`
	ActionKey            string         `json:"action_key"`
	OriginID             string         `json:"origin_id"`
	ReleaseID            string         `json:"release_id"`
	DesiredGeneration    int64          `json:"desired_generation"`
	ManagedUserSetDigest string         `json:"managed_user_set_digest"`
	Control              ManagedControl `json:"control"`
	Receipt              ManagedReceipt `json:"receipt"`
}

type LeaseReceiptProof struct {
	ActionKey            string         `json:"action_key"`
	OriginID             string         `json:"origin_id"`
	ReleaseID            string         `json:"release_id"`
	DesiredGeneration    int64          `json:"desired_generation"`
	ManagedUserSetDigest string         `json:"managed_user_set_digest"`
	Control              ManagedControl `json:"control"`
	Receipt              ManagedReceipt `json:"receipt"`
}

type FinalReceiptPage struct {
	Schema               int                   `json:"schema"`
	FinalReceipts        []ManagedFinalReceipt `json:"final_receipts"`
	HasMoreFinalReceipts bool                  `json:"has_more_final_receipts"`
	PendingUseLease      *UseLeaseRequest      `json:"pending_use_lease,omitempty"`
}

type FinalReceiptACK struct {
	ReceiptID   string `json:"receipt_id"`
	ProofSHA256 string `json:"proof_sha256"`
}

type UseLeaseRequest struct {
	Schema                int      `json:"schema"`
	ActionKey             string   `json:"action_key"`
	XrayProcessBootID     string   `json:"xray_process_boot_id"`
	ConfigDigest          string   `json:"config_digest"`
	ManagedUserSetDigest  string   `json:"managed_user_set_digest"`
	Nonce                 string   `json:"nonce"`
	ClockDomain           string   `json:"clock_domain"`
	ReadStartedBoottimeNS int64    `json:"read_started_boottime_ns"`
	DeadlineBoottimeNS    int64    `json:"deadline_boottime_ns"`
	Emails                []string `json:"emails"`
}

type UseLeaseResponse struct {
	Schema          int                 `json:"schema"`
	Nonce           string              `json:"nonce"`
	Complete        bool                `json:"complete"`
	NeedsFreshNonce bool                `json:"needs_fresh_nonce"`
	Receipts        []LeaseReceiptProof `json:"receipts"`
}

type usageUserWire struct {
	Email         string  `json:"email"`
	UplinkBytes   *uint64 `json:"uplink_bytes"`
	DownlinkBytes *uint64 `json:"downlink_bytes"`
}

type usageSnapshotWire struct {
	Receipt              Receipt               `json:"receipt"`
	SampledAt            time.Time             `json:"sampled_at"`
	Users                []usageUserWire       `json:"users"`
	UnavailableUsers     []string              `json:"unavailable_users"`
	LeaseChallenge       *UseLeaseChallenge    `json:"lease_challenge,omitempty"`
	FinalReceipts        []ManagedFinalReceipt `json:"final_receipts,omitempty"`
	HasMoreFinalReceipts bool                  `json:"has_more_final_receipts,omitempty"`
	PendingUseLease      *UseLeaseRequest      `json:"pending_use_lease,omitempty"`
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
		lookupContext := context.WithoutCancel(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			// Detach cancellation for exact receipt recovery, not the operation budget.
			var cancelLookup context.CancelFunc
			lookupContext, cancelLookup = context.WithDeadline(lookupContext, deadline)
			defer cancelLookup()
		}
		receipt, lookupErr := client.LookupReceipt(lookupContext, actionKey)
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

// NewUseLeaseRequest preserves the agent's read-start anchor. budget is a
// conservative freshness duration, never a translation of the backend clock.
func NewUseLeaseRequest(snapshot UsageSnapshot, budget time.Duration, emails []string) (UseLeaseRequest, error) {
	if !validUsageSnapshot(snapshot, snapshot.Receipt.ActionKey) || snapshot.LeaseChallenge == nil ||
		budget <= 0 || budget > 5*time.Second || !validLeaseEmails(emails) {
		return UseLeaseRequest{}, ErrInvalidRequest
	}
	challenge := snapshot.LeaseChallenge
	for _, email := range emails {
		index := sort.SearchStrings(challenge.ManagedUsers, email)
		if index == len(challenge.ManagedUsers) || challenge.ManagedUsers[index] != email {
			return UseLeaseRequest{}, ErrInvalidRequest
		}
	}
	deadline := challenge.ReadStartedBoottimeNS + int64(budget)
	if deadline > challenge.MaxDeadlineBoottimeNS {
		return UseLeaseRequest{}, ErrInvalidRequest
	}
	return UseLeaseRequest{Schema: 2, ActionKey: snapshot.Receipt.ActionKey, XrayProcessBootID: snapshot.Receipt.XrayProcessBootID,
		ConfigDigest: snapshot.Receipt.ConfigDigest, ManagedUserSetDigest: snapshot.Receipt.ManagedUserSetDigest,
		Nonce: challenge.Nonce, ClockDomain: challenge.ClockDomain, ReadStartedBoottimeNS: challenge.ReadStartedBoottimeNS,
		DeadlineBoottimeNS: deadline, Emails: append([]string{}, emails...)}, nil
}

// Lease requests never get retried or recovered as a fresh grant here. The
// agent journals exact operations; uncertainty leaves the old deadline intact.
func (client *Client) PostUseLease(ctx context.Context, value UseLeaseRequest) (UseLeaseResponse, error) {
	if value.Schema != 2 || !validActionKey(value.ActionKey) || !validLeaseDigest(value.XrayProcessBootID) ||
		!validLeaseDigest(value.ConfigDigest) || !validLeaseDigest(value.ManagedUserSetDigest) || !validLeaseDigest(value.Nonce) ||
		!validLeaseDigest(value.ClockDomain) || value.ReadStartedBoottimeNS <= 0 || value.DeadlineBoottimeNS <= value.ReadStartedBoottimeNS ||
		value.DeadlineBoottimeNS-value.ReadStartedBoottimeNS > int64(5*time.Second) || !validLeaseEmails(value.Emails) {
		return UseLeaseResponse{}, ErrInvalidRequest
	}
	var response UseLeaseResponse
	status, err := client.leaseJSON(ctx, http.MethodPost, UseLeasePath, value, &response)
	if err != nil {
		return UseLeaseResponse{}, err
	}
	if response.Schema != 2 || response.Nonce != value.Nonce || response.Receipts == nil || len(response.Receipts) > maxLeaseUsers {
		return UseLeaseResponse{}, ErrDeliveryUnknown
	}
	granted := make(map[string]bool, len(value.Emails))
	for _, proof := range response.Receipts {
		if !validLeaseProof(proof) ||
			proof.Control.BootID != value.XrayProcessBootID || proof.Control.ConfigDigest != value.ConfigDigest || proof.Control.ClockDomain != value.ClockDomain {
			return UseLeaseResponse{}, ErrDeliveryUnknown
		}
		if proof.Control.Operation != "fence" {
			index := sort.SearchStrings(value.Emails, proof.Control.Email)
			if proof.ActionKey != value.ActionKey || proof.ManagedUserSetDigest != value.ManagedUserSetDigest || proof.Control.DeadlineBoottimeNS != value.DeadlineBoottimeNS || index == len(value.Emails) || value.Emails[index] != proof.Control.Email || granted[proof.Control.Email] {
				return UseLeaseResponse{}, ErrDeliveryUnknown
			}
			granted[proof.Control.Email] = true
		}
	}
	if status != http.StatusOK || !response.Complete || response.NeedsFreshNonce {
		return response, ErrRequestRejected
	}
	if len(granted) != len(value.Emails) {
		return UseLeaseResponse{}, ErrDeliveryUnknown
	}
	return response, nil
}

// This read is deliberately independent of current desired/readiness so old
// boots and removed identities cannot strand final counters behind /usage.
func (client *Client) LookupFinalReceipts(ctx context.Context) (FinalReceiptPage, error) {
	var wire struct {
		Schema          int                   `json:"schema"`
		FinalReceipts   []ManagedFinalReceipt `json:"final_receipts"`
		HasMore         *bool                 `json:"has_more_final_receipts"`
		PendingUseLease *UseLeaseRequest      `json:"pending_use_lease,omitempty"`
	}
	status, err := client.leaseJSON(ctx, http.MethodGet, LeaseReceiptsPath, nil, &wire)
	if err != nil {
		return FinalReceiptPage{}, err
	}
	if status != http.StatusOK || wire.Schema != 2 || wire.FinalReceipts == nil || wire.HasMore == nil || !validFinalReceiptSet(wire.FinalReceipts) || (*wire.HasMore && len(wire.FinalReceipts) == 0) {
		return FinalReceiptPage{}, ErrDeliveryUnknown
	}
	if wire.PendingUseLease != nil && !validPendingUseLease(*wire.PendingUseLease) {
		return FinalReceiptPage{}, ErrDeliveryUnknown
	}
	return FinalReceiptPage{Schema: 2, FinalReceipts: wire.FinalReceipts, HasMoreFinalReceipts: *wire.HasMore, PendingUseLease: wire.PendingUseLease}, nil
}

func (client *Client) AckFinalReceipts(ctx context.Context, receipts []FinalReceiptACK) error {
	if len(receipts) == 0 || len(receipts) > maxLeaseFinalReceipts {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, receipt := range receipts {
		if !validLeaseDigest(receipt.ReceiptID) || !validLeaseDigest(receipt.ProofSHA256) || seen[receipt.ReceiptID] {
			return ErrInvalidRequest
		}
		seen[receipt.ReceiptID] = true
	}
	request := struct {
		Schema   int               `json:"schema"`
		Receipts []FinalReceiptACK `json:"receipts"`
	}{2, receipts}
	var response struct {
		Schema   int  `json:"schema"`
		Complete bool `json:"complete"`
	}
	status, err := client.leaseJSON(ctx, http.MethodPost, LeaseAckPath, request, &response)
	if err != nil {
		return err
	}
	if status != http.StatusOK || response.Schema != 2 || !response.Complete {
		return ErrRequestRejected
	}
	return nil
}

func (client *Client) leaseJSON(ctx context.Context, method, path string, value, target any) (int, error) {
	if client == nil || client.httpClient == nil || ctx == nil || client.requestTimeout <= 0 || client.lookupTimeout <= 0 {
		return 0, ErrInvalidRequest
	}
	var raw []byte
	if value != nil {
		var err error
		raw, err = json.Marshal(value)
		if err != nil || len(raw) > MaxRequestBytes {
			return 0, ErrInvalidRequest
		}
	}
	limit := client.requestTimeout
	if method == http.MethodGet {
		limit = client.lookupTimeout
	}
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, method, client.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, ErrInvalidRequest
	}
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, ErrDeliveryUnknown
	}
	defer response.Body.Close()
	responseLimit := maxUsageResponseBytes
	// The complete operation result is journaled inside the agent's bounded
	// 16 MiB state before return. 4096 individual proofs exceed the usage cap.
	if path == UseLeasePath {
		responseLimit = 16 << 20
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil || len(raw) == 0 || len(raw) > responseLimit {
		return response.StatusCode, ErrDeliveryUnknown
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return response.StatusCode, ErrDeliveryUnknown
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return response.StatusCode, ErrDeliveryUnknown
	}
	return response.StatusCode, nil
}

func validLeaseDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validPendingUseLease(value UseLeaseRequest) bool {
	return value.Schema == 2 && validActionKey(value.ActionKey) && validLeaseDigest(value.XrayProcessBootID) &&
		validLeaseDigest(value.ConfigDigest) && validLeaseDigest(value.ManagedUserSetDigest) && validLeaseDigest(value.Nonce) &&
		validLeaseDigest(value.ClockDomain) && value.ReadStartedBoottimeNS > 0 && value.DeadlineBoottimeNS > value.ReadStartedBoottimeNS &&
		value.DeadlineBoottimeNS-value.ReadStartedBoottimeNS <= int64(5*time.Second) && validLeaseEmails(value.Emails)
}

func validLeaseEmails(emails []string) bool {
	if emails == nil || len(emails) > maxLeaseUsers {
		return false
	}
	for index, email := range emails {
		parts := strings.Split(email, ":")
		if !validUsageEmail(email) || len(parts) != 3 || parts[0] != "wl" || parts[1] == "" || len(parts[2]) != 7 || !strings.HasPrefix(parts[2], "exit-s") || parts[2][6] < '1' || parts[2][6] > '4' || (index > 0 && email <= emails[index-1]) {
			return false
		}
	}
	return true
}

func validLeaseChallenge(value *UseLeaseChallenge, users []string) bool {
	if value == nil || value.Schema != 2 || !validLeaseDigest(value.Nonce) || !validLeaseDigest(value.ClockDomain) || value.ReadStartedBoottimeNS <= 0 ||
		value.ReadStartedBoottimeNS > math.MaxInt64-int64(5*time.Second) || value.MaxDeadlineBoottimeNS != value.ReadStartedBoottimeNS+int64(5*time.Second) ||
		!validLeaseEmails(value.ManagedUsers) || len(value.ManagedUsers) != len(users) {
		return false
	}
	for index, email := range users {
		if value.ManagedUsers[index] != email {
			return false
		}
	}
	return true
}

func validLeaseProof(proof LeaseReceiptProof) bool {
	c, r := proof.Control, proof.Receipt
	if !validActionKey(proof.ActionKey) || proof.OriginID == "" || proof.ReleaseID == "" || proof.DesiredGeneration <= 0 || !validLeaseDigest(proof.ManagedUserSetDigest) ||
		c.Schema != 2 || r.Schema != 2 || !validLeaseEmails([]string{c.Email}) || r.Email != c.Email || !validLeaseDigest(c.BootID) || r.BootID != c.BootID ||
		!validLeaseDigest(c.ConfigDigest) || r.ConfigDigest != c.ConfigDigest || c.Generation == 0 || r.Generation != c.Generation ||
		!validLeaseDigest(c.ClockDomain) || r.ClockDomain != c.ClockDomain || r.ResetSequence != 0 {
		return false
	}
	observed, err := time.Parse(time.RFC3339Nano, r.ObservedAt)
	if err != nil || observed.Unix() <= 0 || observed.UTC().Format(time.RFC3339Nano) != r.ObservedAt {
		return false
	}
	if c.Operation == "fence" {
		if c.DeadlineBoottimeNS != 0 || r.DeadlineBoottimeNS != 0 || r.LeaseRemainingMS != nil {
			return false
		}
		if r.State == "fenced_unused" {
			return r.Uplink == nil && r.Downlink == nil
		}
		return r.State == "fenced" && r.Uplink != nil && r.Downlink != nil && *r.Uplink >= 0 && *r.Downlink >= 0
	}
	return (c.Operation == "grant" || c.Operation == "renew") && r.State == "granted" && c.DeadlineBoottimeNS > 0 && r.DeadlineBoottimeNS == c.DeadlineBoottimeNS &&
		r.LeaseRemainingMS != nil && *r.LeaseRemainingMS <= 5000 && r.Uplink == nil && r.Downlink == nil
}

func (value ManagedFinalReceipt) Proof() LeaseReceiptProof {
	return LeaseReceiptProof{value.ActionKey, value.OriginID, value.ReleaseID, value.DesiredGeneration, value.ManagedUserSetDigest, value.Control, value.Receipt}
}

// ValidateManagedFinalReceipt validates the exact immutable proof used by ACK.
// It makes no assertion that a final observation is a billing-period boundary.
func ValidateManagedFinalReceipt(value ManagedFinalReceipt) error {
	proof := value.Proof()
	if !validLeaseDigest(value.ReceiptID) || !validLeaseDigest(value.ProofSHA256) || value.Control.Operation != "fence" || !validLeaseProof(proof) {
		return ErrInvalidRequest
	}
	identity := struct {
		ActionKey            string         `json:"action_key"`
		OriginID             string         `json:"origin_id"`
		ReleaseID            string         `json:"release_id"`
		DesiredGeneration    int64          `json:"desired_generation"`
		ManagedUserSetDigest string         `json:"managed_user_set_digest"`
		Control              ManagedControl `json:"control"`
	}{value.ActionKey, value.OriginID, value.ReleaseID, value.DesiredGeneration, value.ManagedUserSetDigest, value.Control}
	raw, err := json.Marshal(identity)
	if err != nil {
		return ErrInvalidRequest
	}
	id := sha256.Sum256(raw)
	raw, err = json.Marshal(proof)
	if err != nil {
		return ErrInvalidRequest
	}
	digest := sha256.Sum256(raw)
	if value.ReceiptID != hex.EncodeToString(id[:]) || value.ProofSHA256 != hex.EncodeToString(digest[:]) {
		return ErrInvalidRequest
	}
	return nil
}

func validFinalReceiptSet(values []ManagedFinalReceipt) bool {
	if len(values) > maxLeaseFinalReceipts {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if ValidateManagedFinalReceipt(value) != nil || seen[value.ReceiptID] {
			return false
		}
		seen[value.ReceiptID] = true
	}
	return true
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
	raw, err := io.ReadAll(io.LimitReader(body, maxUsageResponseBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxUsageResponseBytes {
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
		LeaseChallenge:   wire.LeaseChallenge, FinalReceipts: wire.FinalReceipts,
		HasMoreFinalReceipts: wire.HasMoreFinalReceipts,
		PendingUseLease:      wire.PendingUseLease,
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
	if value.LeaseChallenge != nil && !validLeaseChallenge(value.LeaseChallenge, managedUsers) {
		return false
	}
	if !validFinalReceiptSet(value.FinalReceipts) || (value.HasMoreFinalReceipts && len(value.FinalReceipts) == 0) {
		return false
	}
	if value.PendingUseLease != nil {
		pending := value.PendingUseLease
		if value.LeaseChallenge != nil || !validPendingUseLease(*pending) || pending.ActionKey != value.Receipt.ActionKey ||
			pending.XrayProcessBootID != value.Receipt.XrayProcessBootID || pending.ConfigDigest != value.Receipt.ConfigDigest || pending.ManagedUserSetDigest != value.Receipt.ManagedUserSetDigest {
			return false
		}
	}
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
