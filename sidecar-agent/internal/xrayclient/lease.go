package xrayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	// ErrInvalidManagedControl means the bridge rejected the call before RPC.
	ErrInvalidManagedControl = errors.New("xray client: invalid managed control")
	// ErrManagedControlUnknown carries no verified receipt. The caller must
	// resolve its durable operation without allocating a new sequence/deadline.
	ErrManagedControlUnknown = errors.New("xray client: managed control outcome unavailable")
	// ErrManagedLeaseNotLive accompanies a fully validated grant/renew receipt,
	// retained as evidence, which must not be used as current lease authority.
	ErrManagedLeaseNotLive = errors.New("xray client: managed lease is not live")
)

const managedControlMaxBytes = 4096

type leaseRPC interface {
	Invoke(context.Context, string, any, any, ...grpc.CallOption) error
}

// ApplyManagedControl sends exactly one caller-owned schema-2 operation on the
// existing isolated mTLS connection. It neither creates authority nor retries.
// The caller supplies a bounded context and durably records the complete tuple
// before calling. A nonzero receipt with ErrManagedLeaseNotLive is verified
// evidence to retain, not permission to reopen a user or extend its deadline.
func (client *Client) ApplyManagedControl(ctx context.Context, control runtimefence.Control) (runtimefence.Receipt, error) {
	if client == nil || client.leaseRPC == nil || ctx == nil || !validManagedControl(control) {
		return runtimefence.Receipt{}, ErrInvalidManagedControl
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return runtimefence.Receipt{}, ErrInvalidManagedControl
	}
	if err := ctx.Err(); err != nil {
		return runtimefence.Receipt{}, err
	}
	readClock := client.leaseClock
	if readClock == nil {
		readClock = runtimefence.ReadLeaseClock
	}
	leasing := control.Operation != "fence"
	var started int64
	if leasing {
		domain, now, err := readClock()
		if err != nil || domain != control.ClockDomain || now <= 0 ||
			control.DeadlineBoottimeNS <= now || control.DeadlineBoottimeNS-now > int64(5*time.Second) {
			return runtimefence.Receipt{}, ErrInvalidManagedControl
		}
		started = now
	}
	raw, err := json.Marshal(control)
	if err != nil || len(raw) > managedControlMaxBytes {
		return runtimefence.Receipt{}, ErrInvalidManagedControl
	}
	response := new(wrapperspb.BytesValue)
	if err := client.leaseRPC.Invoke(ctx, runtimefence.Method, wrapperspb.Bytes(raw), response,
		grpc.MaxCallRecvMsgSize(managedControlMaxBytes+64)); err != nil {
		return runtimefence.Receipt{}, ErrManagedControlUnknown
	}
	receipt, err := decodeManagedReceipt(response.Value)
	if err != nil || !managedReceiptMatches(control, receipt) {
		return runtimefence.Receipt{}, ErrManagedControlUnknown
	}
	if leasing {
		domain, now, err := readClock()
		if err != nil || domain != control.ClockDomain || now < started ||
			now >= control.DeadlineBoottimeNS || *receipt.LeaseRemainingMS == 0 || ctx.Err() != nil {
			return receipt, ErrManagedLeaseNotLive
		}
	}
	// Fence receipts retain real counters even if the caller's deadline elapsed
	// or local BOOTTIME became unreadable after the runtime completed its drain.
	return receipt, nil
}

func validManagedControl(control runtimefence.Control) bool {
	if control.Schema != 2 || control.Generation == 0 || !managedEmail.MatchString(control.Email) ||
		len(control.Email) > 200 || !utf8.ValidString(control.Email) ||
		!managedDigest(control.BootID) || !managedDigest(control.ConfigDigest) || !managedDigest(control.ClockDomain) {
		return false
	}
	for _, character := range control.Email {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '>' {
			return false
		}
	}
	switch control.Operation {
	case "grant", "renew":
		return control.DeadlineBoottimeNS > 0
	case "fence":
		return control.DeadlineBoottimeNS == 0
	default:
		return false
	}
}

func managedDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func managedReceiptMatches(control runtimefence.Control, receipt runtimefence.Receipt) bool {
	if receipt.Schema != control.Schema || receipt.Email != control.Email || receipt.BootID != control.BootID ||
		receipt.ConfigDigest != control.ConfigDigest || receipt.Generation != control.Generation ||
		receipt.ClockDomain != control.ClockDomain || receipt.ResetSequence != 0 {
		return false
	}
	observed, err := time.Parse(time.RFC3339Nano, receipt.ObservedAt)
	if err != nil || observed.IsZero() || !strings.HasSuffix(receipt.ObservedAt, "Z") ||
		observed.Format(time.RFC3339Nano) != receipt.ObservedAt {
		return false
	}
	if control.Operation != "fence" {
		return receipt.State == "granted" && receipt.Uplink == nil && receipt.Downlink == nil &&
			receipt.DeadlineBoottimeNS == control.DeadlineBoottimeNS &&
			receipt.LeaseRemainingMS != nil && *receipt.LeaseRemainingMS <= 5000
	}
	if receipt.DeadlineBoottimeNS != 0 || receipt.LeaseRemainingMS != nil {
		return false
	}
	switch receipt.State {
	case "fenced":
		return receipt.Uplink != nil && receipt.Downlink != nil && *receipt.Uplink >= 0 && *receipt.Downlink >= 0
	case "fenced_unused":
		return receipt.Uplink == nil && receipt.Downlink == nil
	default:
		return false
	}
}

// Reuse the actual runtime Receipt type. Token inspection rejects duplicate
// keys and null values; its own JSON tags determine required and optional keys,
// so aliases or omitted zero-valued required fields cannot pass as evidence.
func decodeManagedReceipt(raw []byte) (runtimefence.Receipt, error) {
	invalid := func() (runtimefence.Receipt, error) { return runtimefence.Receipt{}, ErrManagedControlUnknown }
	if len(raw) == 0 || len(raw) > managedControlMaxBytes {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return invalid()
	}
	keys := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || keys[key] {
			return invalid()
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
		keys[key] = true
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return invalid()
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return invalid()
	}
	var receipt runtimefence.Receipt
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil {
		return invalid()
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return invalid()
	}
	var canonicalKeys map[string]json.RawMessage
	if json.Unmarshal(encoded, &canonicalKeys) != nil || len(canonicalKeys) != len(keys) {
		return invalid()
	}
	for key := range canonicalKeys {
		if !keys[key] {
			return invalid()
		}
	}
	return receipt, nil
}
