package applyagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
)

type fakeXUIClient struct {
	snapshot       XUIInboundSnapshot
	snapshotErr    error
	upsertErr      error
	deleteErr      error
	calls          []string
	upserts        []XUIUserPatch
	deletes        []string
	observedSHA256 string
}

func (f *fakeXUIClient) Snapshot(_ context.Context, inboundID int) (XUIInboundSnapshot, error) {
	f.calls = append(f.calls, "snapshot")
	if f.snapshotErr != nil {
		return XUIInboundSnapshot{}, f.snapshotErr
	}
	f.snapshot.InboundID = inboundID
	return f.snapshot, nil
}

func (f *fakeXUIClient) UpsertUser(_ context.Context, inboundID int, user XUIUserPatch) error {
	f.calls = append(f.calls, "upsert")
	if f.upsertErr != nil {
		return f.upsertErr
	}
	user.InboundID = inboundID
	f.upserts = append(f.upserts, user)
	f.snapshot.Users = append(f.snapshot.Users, XUIUser{
		Login: user.Login, UUID: user.UUID, SubID: user.SubID,
		Flow: user.Flow, AbsoluteExpiryUnix: user.AbsoluteExpiryUnix,
		Generation: user.Generation, PayloadSHA256: user.PayloadSHA256,
	})
	return nil
}

func (f *fakeXUIClient) DeleteUser(_ context.Context, inboundID int, login string) error {
	f.calls = append(f.calls, "delete")
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletes = append(f.deletes, login)
	kept := f.snapshot.Users[:0]
	for _, user := range f.snapshot.Users {
		if user.Login != login || user.InboundID != inboundID {
			kept = append(kept, user)
		}
	}
	f.snapshot.Users = kept
	return nil
}

func (f *fakeXUIClient) ObservedSHA256(context.Context, int) (string, error) {
	f.calls = append(f.calls, "observed")
	if f.observedSHA256 != "" {
		return f.observedSHA256, nil
	}
	payload, err := json.Marshal(f.snapshot.Users)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func TestXUIDriverRejectsRemoteEndpointBeforeClientCall(t *testing.T) {
	for _, endpoint := range []string{
		"http://203.0.113.10:54321",
		"https://s1.maestro.example:54321",
		"http://[2001:db8::11]:54321",
	} {
		client := &fakeXUIClient{}
		_, err := NewXUIDriver(XUIDriverConfig{
			NodeID: "s1", ServiceID: "s1-vless", Endpoint: mustURL(t, endpoint),
			InboundID: 7, Client: client,
		})
		if !errors.Is(err, ErrDriverInvalidTarget) {
			t.Fatalf("endpoint %s error=%v, want ErrDriverInvalidTarget", endpoint, err)
		}
		if len(client.calls) != 0 {
			t.Fatalf("remote endpoint %s reached x-ui client: %v", endpoint, client.calls)
		}
	}
}

func TestXUIDriverPreservesLoginUUIDSubIDFlowAndAbsoluteExpiry(t *testing.T) {
	client := &fakeXUIClient{}
	driver := newTestXUIDriver(t, "s1", "s1-vless", client)
	snapshot := xuiSnapshot("s1", "s1-vless", MaterializedEntry{
		CustomerID: "cust-1", OperationID: "op-1", PayloadKind: XUIPayloadKind,
		Generation: 42, Body: xuiBody(t, "wapmix", "11111111-1111-4111-8111-111111111111", "sub-wapmix", "xtls-rprx-vision", 1798761600),
		BodySHA256: sha256Hex([]byte("body")), DesiredSHA256: sha256Hex([]byte("desired")),
	})
	prepared, err := driver.Prepare(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("Prepare returned %v", err)
	}
	if len(client.upserts) != 1 {
		t.Fatalf("upserts=%d, want 1", len(client.upserts))
	}
	got := client.upserts[0]
	if got.Login != "wapmix" || got.UUID != "11111111-1111-4111-8111-111111111111" || got.SubID != "sub-wapmix" || got.Flow != "xtls-rprx-vision" || got.AbsoluteExpiryUnix != 1798761600 {
		t.Fatalf("x-ui patch lost identity/expiry: %#v", got)
	}
}

func TestXUIDriverIdempotentAddUpdateDeleteAndSameGenerationNoop(t *testing.T) {
	client := &fakeXUIClient{}
	driver := newTestXUIDriver(t, "s1", "s1-vless", client)
	first := xuiSnapshot("s1", "s1-vless", xuiEntry(t, "cust-1", "wapmix", 1, false))
	client.observedSHA256 = first.SnapshotSHA256
	prepared, err := driver.Prepare(context.Background(), first)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if _, err := driver.Commit(context.Background(), prepared); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	callsAfterFirst := len(client.calls)
	actual, err := driver.Inspect(context.Background(), first)
	if err != nil {
		t.Fatalf("inspect after first commit: %v", err)
	}
	if !actual.Healthy || actual.SnapshotSHA256 != first.SnapshotSHA256 {
		t.Fatalf("actual=%#v, want healthy same-generation no-op", actual)
	}
	if len(client.calls) != callsAfterFirst+2 { // snapshot + observed only, no upsert/delete
		t.Fatalf("same generation caused mutation calls: %v", client.calls[callsAfterFirst:])
	}
	deleted := xuiSnapshot("s1", "s1-vless", xuiEntry(t, "cust-1", "wapmix", 2, true))
	if _, err := driver.Prepare(context.Background(), deleted); err != nil {
		t.Fatalf("delete prepare: %v", err)
	}
	if len(client.deletes) != 1 || client.deletes[0] != "wapmix" {
		t.Fatalf("deletes=%v, want exactly wapmix", client.deletes)
	}
}

func TestXUIDriverAPIErrorIsNotAbsenceAndRollbackReceiptMismatch(t *testing.T) {
	boom := errors.New("raw x-ui body must not escape")
	client := &fakeXUIClient{snapshotErr: boom}
	driver := newTestXUIDriver(t, "s1", "s1-vless", client)
	if _, err := driver.Prepare(context.Background(), xuiSnapshot("s1", "s1-vless", xuiEntry(t, "cust-1", "wapmix", 1, false))); !errors.Is(err, ErrDriverInspect) {
		t.Fatalf("snapshot API error=%v, want fixed ErrDriverInspect", err)
	}
	if len(client.upserts) != 0 || len(client.deletes) != 0 {
		t.Fatalf("API error was interpreted as absence and mutated state: upserts=%v deletes=%v", client.upserts, client.deletes)
	}
	client = &fakeXUIClient{observedSHA256: sha256Hex([]byte("wrong-live-state"))}
	driver = newTestXUIDriver(t, "s1", "s1-vless", client)
	prepared, err := driver.Prepare(context.Background(), xuiSnapshot("s1", "s1-vless", xuiEntry(t, "cust-1", "wapmix", 1, false)))
	if err != nil {
		t.Fatalf("prepare for mismatch: %v", err)
	}
	if _, err := driver.Commit(context.Background(), prepared); !errors.Is(err, ErrDriverCommit) {
		t.Fatalf("commit mismatch error=%v, want ErrDriverCommit", err)
	}
}

func TestRqliteXUICompositionHasNoRemoteEndpoint(t *testing.T) {
	composition := XUIRqliteComposition{
		NodeID: "s1", ServiceID: "s1-vless", Endpoint: mustURL(t, "http://10.10.10.10:54321"),
		InboundID: 7, PayloadKind: XUIPayloadKind,
	}
	if _, err := NewXUIDriverFromRqliteComposition(composition, &fakeXUIClient{}); !errors.Is(err, ErrDriverInvalidTarget) {
		t.Fatalf("remote rqlite composition error=%v, want ErrDriverInvalidTarget", err)
	}
}

func newTestXUIDriver(t *testing.T, nodeID, serviceID string, client *fakeXUIClient) Driver {
	t.Helper()
	driver, err := NewXUIDriver(XUIDriverConfig{
		NodeID: nodeID, ServiceID: serviceID, Endpoint: mustURL(t, "http://127.0.0.1:54321"),
		InboundID: 7, PayloadKind: XUIPayloadKind, Client: client,
	})
	if err != nil {
		t.Fatalf("NewXUIDriver: %v", err)
	}
	return driver
}

func xuiSnapshot(nodeID, serviceID string, entries ...MaterializedEntry) MaterializedSnapshot {
	return MaterializedSnapshot{NodeID: nodeID, ServiceID: serviceID, TriggerOperationID: "task12-red", SnapshotSHA256: sha256Hex([]byte("desired-snapshot")), Entries: entries}
}

func xuiEntry(t *testing.T, customerID, login string, generation int64, tombstone bool) MaterializedEntry {
	t.Helper()
	body := xuiBody(t, login, "11111111-1111-4111-8111-111111111111", "sub-"+login, "xtls-rprx-vision", 1798761600)
	if tombstone {
		body = json.RawMessage(`{"login":"` + login + `","tombstone":true}`)
	}
	return MaterializedEntry{CustomerID: customerID, OperationID: "op-" + customerID, PayloadKind: XUIPayloadKind, Generation: generation, Tombstone: tombstone, Body: body, BodySHA256: sha256Hex(body), DesiredSHA256: sha256Hex([]byte(customerID))}
}

func xuiBody(t *testing.T, login, uuid, subID, flow string, absoluteExpiryUnix int64) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"login": login, "uuid": uuid, "sub_id": subID, "flow": flow, "absolute_expiry_unix": absoluteExpiryUnix})
	if err != nil {
		t.Fatalf("marshal xui body: %v", err)
	}
	return payload
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}
