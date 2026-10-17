package xrayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type leaseTestRPC struct {
	t        *testing.T
	response []byte
	err      error
	calls    [][]byte
	contexts []context.Context
	after    func()
}

func (rpc *leaseTestRPC) Invoke(ctx context.Context, method string, request, reply any, _ ...grpc.CallOption) error {
	rpc.t.Helper()
	if method != runtimefence.Method {
		rpc.t.Fatal("managed control used a different RPC method")
	}
	in, ok := request.(*wrapperspb.BytesValue)
	if !ok {
		rpc.t.Fatal("managed control did not use the existing BytesValue request")
	}
	out, ok := reply.(*wrapperspb.BytesValue)
	if !ok {
		rpc.t.Fatal("managed control did not use the existing BytesValue response")
	}
	rpc.calls = append(rpc.calls, bytes.Clone(in.Value))
	rpc.contexts = append(rpc.contexts, ctx)
	out.Value = bytes.Clone(rpc.response)
	if rpc.after != nil {
		rpc.after()
	}
	return rpc.err
}

func leaseTestControl() runtimefence.Control {
	return runtimefence.Control{
		Schema: 2, Operation: "grant", Email: "wl:lease-test:exit-s4",
		BootID: strings.Repeat("a", 64), ConfigDigest: strings.Repeat("b", 64),
		Generation: 7, ClockDomain: strings.Repeat("c", 64),
		DeadlineBoottimeNS: 14 * int64(time.Second),
	}
}

func leaseTestReceipt(control runtimefence.Control) runtimefence.Receipt {
	remaining := uint32(3000)
	return runtimefence.Receipt{
		Schema: 2, State: "granted", Email: control.Email, BootID: control.BootID,
		ConfigDigest: control.ConfigDigest, Generation: control.Generation,
		ClockDomain: control.ClockDomain, DeadlineBoottimeNS: control.DeadlineBoottimeNS,
		LeaseRemainingMS: &remaining, ObservedAt: "2026-09-06T00:00:00.123456789Z",
	}
}

func leaseTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func leaseTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func leaseTestClient(t *testing.T, control runtimefence.Control, receipt runtimefence.Receipt) (*Client, *leaseTestRPC) {
	t.Helper()
	rpc := &leaseTestRPC{t: t, response: leaseTestJSON(t, receipt)}
	return &Client{
		leaseRPC: rpc,
		leaseClock: func() (string, int64, error) {
			return control.ClockDomain, 10 * int64(time.Second), nil
		},
	}, rpc
}

func TestManagedControlPreservesExactCommandAndCallerContext(t *testing.T) {
	for _, operation := range []string{"grant", "renew"} {
		t.Run(operation, func(t *testing.T) {
			control := leaseTestControl()
			control.Operation = operation
			receipt := leaseTestReceipt(control)
			client, rpc := leaseTestClient(t, control, receipt)
			ctx := leaseTestContext(t)
			for attempt := 0; attempt < 2; attempt++ {
				got, err := client.ApplyManagedControl(ctx, control)
				if err != nil || !bytes.Equal(leaseTestJSON(t, got), leaseTestJSON(t, receipt)) {
					t.Fatalf("exact caller operation failed: %v", err)
				}
			}
			if len(rpc.calls) != 2 || !bytes.Equal(rpc.calls[0], rpc.calls[1]) ||
				!bytes.Equal(rpc.calls[0], leaseTestJSON(t, control)) || rpc.contexts[0] != ctx || rpc.contexts[1] != ctx {
				t.Fatal("bridge changed the tuple, context, or number of caller-issued operations")
			}
		})
	}
}

func TestManagedControlRejectsBeforeRPC(t *testing.T) {
	for name, change := range map[string]func(*runtimefence.Control){
		"old schema":         func(c *runtimefence.Control) { c.Schema = 1 },
		"default operation":  func(c *runtimefence.Control) { c.Operation = "" },
		"ordinary identity":  func(c *runtimefence.Control) { c.Email = "ordinary" },
		"counter separator":  func(c *runtimefence.Control) { c.Email = "wl:bad>name:exit-s4" },
		"space":              func(c *runtimefence.Control) { c.Email = "wl:bad name:exit-s4" },
		"unbounded identity": func(c *runtimefence.Control) { c.Email = "wl:" + strings.Repeat("x", 200) + ":exit-s4" },
		"boot":               func(c *runtimefence.Control) { c.BootID = "boot" },
		"digest":             func(c *runtimefence.Control) { c.ConfigDigest = strings.Repeat("B", 64) },
		"sequence":           func(c *runtimefence.Control) { c.Generation = 0 },
		"domain":             func(c *runtimefence.Control) { c.ClockDomain = "domain" },
		"zero deadline":      func(c *runtimefence.Control) { c.DeadlineBoottimeNS = 0 },
		"expired deadline":   func(c *runtimefence.Control) { c.DeadlineBoottimeNS = 10 * int64(time.Second) },
		"overlong deadline":  func(c *runtimefence.Control) { c.DeadlineBoottimeNS = 15*int64(time.Second) + 1 },
		"overflow deadline":  func(c *runtimefence.Control) { c.DeadlineBoottimeNS = math.MaxInt64 },
		"fence deadline":     func(c *runtimefence.Control) { c.Operation = "fence" },
	} {
		t.Run(name, func(t *testing.T) {
			control := leaseTestControl()
			client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
			change(&control)
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if !errors.Is(err, ErrInvalidManagedControl) || got.Schema != 0 || len(rpc.calls) != 0 {
				t.Fatal("invalid control reached RPC or returned evidence")
			}
		})
	}
	control := leaseTestControl()
	client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
	if _, err := client.ApplyManagedControl(context.Background(), control); !errors.Is(err, ErrInvalidManagedControl) {
		t.Fatal("bridge accepted a caller context without a deadline")
	}
	ctx, cancel := context.WithCancel(leaseTestContext(t))
	cancel()
	if _, err := client.ApplyManagedControl(ctx, control); !errors.Is(err, context.Canceled) || len(rpc.calls) != 0 {
		t.Fatal("cancelled context reached RPC")
	}
	for _, mode := range []string{"failure", "other domain", "zero clock"} {
		t.Run(mode, func(t *testing.T) {
			client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
			client.leaseClock = func() (string, int64, error) {
				switch mode {
				case "failure":
					return "", 0, errors.New("clock unavailable")
				case "other domain":
					return strings.Repeat("d", 64), 10 * int64(time.Second), nil
				default:
					return control.ClockDomain, 0, nil
				}
			}
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if !errors.Is(err, ErrInvalidManagedControl) || got.Schema != 0 || len(rpc.calls) != 0 {
				t.Fatal("untrusted local clock reached RPC")
			}
		})
	}
}

func TestManagedControlRejectsUnboundOrMalformedReceipts(t *testing.T) {
	control := leaseTestControl()
	for name, change := range map[string]func(map[string]any){
		"schema":            func(r map[string]any) { r["schema"] = 1 },
		"state":             func(r map[string]any) { r["state"] = "fenced_unused" },
		"email":             func(r map[string]any) { r["email"] = "wl:another:exit-s4" },
		"boot":              func(r map[string]any) { r["boot_id"] = strings.Repeat("d", 64) },
		"config":            func(r map[string]any) { r["config_digest"] = strings.Repeat("d", 64) },
		"sequence":          func(r map[string]any) { r["generation"] = 8 },
		"clock domain":      func(r map[string]any) { r["clock_domain"] = strings.Repeat("d", 64) },
		"deadline":          func(r map[string]any) { r["deadline_boottime_ns"] = control.DeadlineBoottimeNS + 1 },
		"reset":             func(r map[string]any) { r["reset_sequence"] = 1 },
		"missing reset":     func(r map[string]any) { delete(r, "reset_sequence") },
		"null reset":        func(r map[string]any) { r["reset_sequence"] = nil },
		"timestamp":         func(r map[string]any) { r["observed_at"] = "invalid" },
		"non UTC timestamp": func(r map[string]any) { r["observed_at"] = "2026-09-06T03:00:00+03:00" },
		"missing remaining": func(r map[string]any) { delete(r, "lease_remaining_ms") },
		"null remaining":    func(r map[string]any) { r["lease_remaining_ms"] = nil },
		"long remaining":    func(r map[string]any) { r["lease_remaining_ms"] = 5001 },
		"grant counters":    func(r map[string]any) { r["uplink"] = 0; r["downlink"] = 0 },
		"unknown":           func(r map[string]any) { r["lease_ms"] = 5000 },
		"case alias":        func(r map[string]any) { r["Schema"] = r["schema"]; delete(r, "schema") },
	} {
		t.Run(name, func(t *testing.T) {
			client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
			var fields map[string]any
			if err := json.Unmarshal(rpc.response, &fields); err != nil {
				t.Fatal(err)
			}
			change(fields)
			rpc.response = leaseTestJSON(t, fields)
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if !errors.Is(err, ErrManagedControlUnknown) || got.Schema != 0 || len(rpc.calls) != 1 {
				t.Fatal("untrusted response was accepted as evidence or retried")
			}
		})
	}
	for name, raw := range map[string][]byte{
		"empty":     nil,
		"oversized": bytes.Repeat([]byte(" "), 4097),
		"array":     []byte("[]"),
		"duplicate": []byte(strings.Replace(string(leaseTestJSON(t, leaseTestReceipt(control))), `"schema":2`, `"schema":2,"schema":2`, 1)),
		"trailing":  append(leaseTestJSON(t, leaseTestReceipt(control)), []byte("{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
			rpc.response = raw
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if !errors.Is(err, ErrManagedControlUnknown) || got.Schema != 0 || len(rpc.calls) != 1 {
				t.Fatal("malformed response was accepted or retried")
			}
		})
	}
}

func TestManagedControlRetainsVerifiedReceiptWhenLeaseIsNoLongerLive(t *testing.T) {
	control := leaseTestControl()
	for _, mode := range []string{"expired", "regressed", "domain", "clock failure", "zero remaining", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			receipt := leaseTestReceipt(control)
			if mode == "zero remaining" {
				zero := uint32(0)
				receipt.LeaseRemainingMS = &zero
			}
			client, rpc := leaseTestClient(t, control, receipt)
			ctx, cancel := context.WithCancel(leaseTestContext(t))
			defer cancel()
			returned := false
			client.leaseClock = func() (string, int64, error) {
				if !returned {
					return control.ClockDomain, 10 * int64(time.Second), nil
				}
				switch mode {
				case "expired":
					return control.ClockDomain, control.DeadlineBoottimeNS, nil
				case "regressed":
					return control.ClockDomain, 9 * int64(time.Second), nil
				case "domain":
					return strings.Repeat("d", 64), 11 * int64(time.Second), nil
				case "clock failure":
					return "", 0, errors.New("kernel read unavailable")
				default:
					return control.ClockDomain, 11 * int64(time.Second), nil
				}
			}
			rpc.after = func() {
				returned = true
				if mode == "cancelled" {
					cancel()
				}
			}
			got, err := client.ApplyManagedControl(ctx, control)
			if !errors.Is(err, ErrManagedLeaseNotLive) || !bytes.Equal(leaseTestJSON(t, got), leaseTestJSON(t, receipt)) || len(rpc.calls) != 1 {
				t.Fatal("bridge lost verified evidence, granted stale authority, or retried")
			}
		})
	}
}

func TestManagedControlFencePreservesActualCountersWithoutClock(t *testing.T) {
	control := leaseTestControl()
	control.Operation, control.DeadlineBoottimeNS = "fence", 0
	for _, state := range []string{"fenced", "fenced_unused"} {
		t.Run(state, func(t *testing.T) {
			receipt := leaseTestReceipt(control)
			receipt.State, receipt.LeaseRemainingMS = state, nil
			if state == "fenced" {
				up, down := int64(0), int64(math.MaxInt64)
				receipt.Uplink, receipt.Downlink = &up, &down
			}
			client, rpc := leaseTestClient(t, control, receipt)
			client.leaseClock = func() (string, int64, error) {
				t.Fatal("fence must not depend on clock availability")
				return "", 0, errors.New("unavailable")
			}
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if err != nil || !bytes.Equal(leaseTestJSON(t, got), leaseTestJSON(t, receipt)) || len(rpc.calls) != 1 {
				t.Fatal("fence lost actual counters or fabricated an unused sample")
			}
		})
	}
	for _, mode := range []string{"missing pair", "negative", "unused zero pair", "fence lease"} {
		t.Run(mode, func(t *testing.T) {
			receipt := leaseTestReceipt(control)
			receipt.State, receipt.LeaseRemainingMS = "fenced", nil
			up, down := int64(1), int64(2)
			receipt.Uplink, receipt.Downlink = &up, &down
			switch mode {
			case "missing pair":
				receipt.Downlink = nil
			case "negative":
				up = -1
			case "unused zero pair":
				receipt.State = "fenced_unused"
				up, down = 0, 0
			case "fence lease":
				remaining := uint32(1)
				receipt.LeaseRemainingMS = &remaining
			}
			client, rpc := leaseTestClient(t, control, receipt)
			got, err := client.ApplyManagedControl(leaseTestContext(t), control)
			if !errors.Is(err, ErrManagedControlUnknown) || got.Schema != 0 || len(rpc.calls) != 1 {
				t.Fatal("invalid final proof was accepted")
			}
		})
	}
}

func TestManagedControlUnknownOutcomeDoesNotRetry(t *testing.T) {
	control := leaseTestControl()
	client, rpc := leaseTestClient(t, control, leaseTestReceipt(control))
	rpc.err = errors.New("remote detail must not escape")
	got, err := client.ApplyManagedControl(leaseTestContext(t), control)
	if !errors.Is(err, ErrManagedControlUnknown) || strings.Contains(err.Error(), "remote detail") || got.Schema != 0 || len(rpc.calls) != 1 {
		t.Fatal("unknown outcome was retried, leaked, or returned as proof")
	}
}
