package shadowbilling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
)

func TestDurableStoreSQLBackedSemantics(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}

	t.Run("first sample and positive delta", func(t *testing.T) {
		store, policy := newMeteringFixture(t, db, "meter-first")
		baseline := meteringEvent(policy, "first-baseline", "s2-xray-a", 1, 1, 100)
		result, err := store.ApplyOrdered(context.Background(), baseline, policy)
		if err != nil {
			t.Fatalf("first sample: %v", err)
		}
		if result.Decision.Diagnostic != DiagnosticEpochStarted || result.Decision.Interval != nil {
			t.Fatalf("first decision=%#v", result.Decision)
		}
		if result.Projection.UsedBytes != 0 || result.Projection.IncludedBytes != 100 ||
			result.Projection.RemainingBytes != 100 || !result.Projection.Pending ||
			result.Projection.Diagnostic != DiagnosticEpochStarted {
			t.Fatalf("first projection=%#v", result.Projection)
		}

		result, err = store.ApplyOrdered(context.Background(), meteringEvent(policy, "first-delta", "s2-xray-a", 1, 2, 250), policy)
		if err != nil {
			t.Fatalf("positive delta: %v", err)
		}
		if result.Decision.Interval == nil || result.Decision.Interval.DownlinkBytes != 150 ||
			result.Decision.Interval.BillableBytes != 50 {
			t.Fatalf("positive decision=%#v", result.Decision)
		}
		if result.Projection.UsedBytes != 150 || result.Projection.IncludedBytes != 100 ||
			result.Projection.RemainingBytes != 0 || !result.Projection.SoftLimitReached ||
			result.Projection.Suspension.Recommended || result.Projection.Pending ||
			result.Projection.Diagnostic != "" {
			t.Fatalf("positive projection=%#v", result.Projection)
		}
	})

	t.Run("same event replay and conflicting event id", func(t *testing.T) {
		store, policy := newMeteringFixture(t, db, "meter-replay")
		if _, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "replay-baseline", "s2-xray-a", 1, 1, 0), policy); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		event := meteringEvent(policy, "replay-delta", "s2-xray-a", 1, 2, 125)
		first, err := store.ApplyOrdered(context.Background(), event, policy)
		if err != nil {
			t.Fatalf("first event: %v", err)
		}
		replayed, err := store.ApplyOrdered(context.Background(), event, policy)
		if err != nil {
			t.Fatalf("same event replay: %v", err)
		}
		if !reflect.DeepEqual(replayed, first) {
			t.Fatalf("replay changed stored result:\nfirst=%#v\nreplay=%#v", first, replayed)
		}

		before := meteringSnapshot(t, db, policy)
		conflict := event
		conflict.DownlinkBytes++
		_, err = store.ApplyOrdered(context.Background(), conflict, policy)
		if !errors.Is(err, ErrEventIDConflict) {
			t.Fatalf("conflicting event error=%v", err)
		}
		if after := meteringSnapshot(t, db, policy); after != before {
			t.Fatalf("conflict mutated durable state:\nbefore=%s\nafter=%s", before, after)
		}
	})

	t.Run("late sequence generation reset and counter rollback", func(t *testing.T) {
		store, policy := newMeteringFixture(t, db, "meter-order")
		policy.IncludedBytes = 0
		policy.SoftLimitBytes = 0
		policy.HardLimitBytes = 0
		policy.GraceBytes = 0
		if _, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-baseline", "s2-xray-a", 1, 1, 0), policy); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		accepted, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-accepted", "s2-xray-a", 1, 3, 100), policy)
		if err != nil || accepted.Decision.Interval == nil || accepted.Decision.Interval.BillableBytes != 100 {
			t.Fatalf("accepted=%#v err=%v", accepted, err)
		}

		late, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-late", "s2-xray-a", 1, 2, 50), policy)
		if err != nil || late.Decision.Diagnostic != DiagnosticLateSample || late.Decision.Interval != nil {
			t.Fatalf("late=%#v err=%v", late, err)
		}
		if late.Projection.UsedBytes != 100 || !late.Projection.Pending || late.Projection.Diagnostic != DiagnosticLateSample {
			t.Fatalf("late projection=%#v", late.Projection)
		}

		beforeRollback := meteringSnapshot(t, db, policy)
		_, err = store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-rollback", "s2-xray-a", 1, 4, 50), policy)
		if !errors.Is(err, ErrResetGenerationRequired) {
			t.Fatalf("counter rollback error=%v", err)
		}
		if after := meteringSnapshot(t, db, policy); after != beforeRollback {
			t.Fatalf("counter rollback mutated durable state:\nbefore=%s\nafter=%s", beforeRollback, after)
		}

		reset, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-reset", "s2-xray-a", 2, 1, 50), policy)
		if err != nil || reset.Decision.Diagnostic != DiagnosticCounterReset || reset.Decision.Interval != nil {
			t.Fatalf("generation reset=%#v err=%v", reset, err)
		}
		postReset, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "order-post-reset", "s2-xray-a", 2, 2, 60), policy)
		if err != nil || postReset.Decision.Interval == nil || postReset.Decision.Interval.BillableBytes != 10 ||
			postReset.Projection.UsedBytes != 110 {
			t.Fatalf("post reset=%#v err=%v", postReset, err)
		}
	})

	t.Run("restart persistence and cross instance separation", func(t *testing.T) {
		store, policy := newMeteringFixture(t, db, "meter-restart")
		if _, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "restart-s2-base", "s2-xray-a", 1, 1, 0), policy); err != nil {
			t.Fatalf("s2 baseline: %v", err)
		}
		if _, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "restart-s2-100", "s2-xray-a", 1, 2, 100), policy); err != nil {
			t.Fatalf("s2 first delta: %v", err)
		}

		reopened, err := NewDurableStore(db)
		if err != nil {
			t.Fatalf("reopen durable store: %v", err)
		}
		s2, err := reopened.ApplyOrdered(context.Background(), meteringEvent(policy, "restart-s2-125", "s2-xray-a", 1, 3, 125), policy)
		if err != nil || s2.Decision.Interval == nil || s2.Decision.Interval.DownlinkBytes != 25 {
			t.Fatalf("reopened s2=%#v err=%v", s2, err)
		}
		if _, err := reopened.ApplyOrdered(context.Background(), meteringEvent(policy, "restart-s3-base", "s3-xray-b", 1, 1, 500), policy); err != nil {
			t.Fatalf("s3 baseline: %v", err)
		}
		s3, err := reopened.ApplyOrdered(context.Background(), meteringEvent(policy, "restart-s3-550", "s3-xray-b", 1, 2, 550), policy)
		if err != nil || s3.Decision.Interval == nil || s3.Decision.Interval.DownlinkBytes != 50 ||
			s3.Projection.UsedBytes != 175 {
			t.Fatalf("separate s3=%#v err=%v", s3, err)
		}
		if got := meteringCheckpointCount(t, db, policy); got != 2 {
			t.Fatalf("checkpoint streams=%d, want 2", got)
		}
	})

	t.Run("transaction rollback keeps checkpoint event and projection together", func(t *testing.T) {
		store, policy := newMeteringFixture(t, db, "meter-rollback")
		if _, err := store.ApplyOrdered(context.Background(), meteringEvent(policy, "tx-baseline", "s2-xray-a", 1, 1, 0), policy); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		before := meteringSnapshot(t, db, policy)
		db.failNextTransaction()
		event := meteringEvent(policy, "tx-delta", "s2-xray-a", 1, 2, 100)
		if _, err := store.ApplyOrdered(context.Background(), event, policy); err == nil {
			t.Fatal("injected transaction failure returned nil")
		}
		if after := meteringSnapshot(t, db, policy); after != before {
			t.Fatalf("failed transaction changed durable state:\nbefore=%s\nafter=%s", before, after)
		}

		reopened, err := NewDurableStore(db)
		if err != nil {
			t.Fatalf("reopen after rollback: %v", err)
		}
		result, err := reopened.ApplyOrdered(context.Background(), event, policy)
		if err != nil || result.Decision.Interval == nil || result.Decision.Interval.DownlinkBytes != 100 ||
			result.Projection.UsedBytes != 100 {
			t.Fatalf("event after rollback=%#v err=%v", result, err)
		}
	})
}

func newMeteringFixture(t *testing.T, db *meteringSQLite, accountID string) (*DurableStore, Policy) {
	t.Helper()
	entitlement := whitelistfixture.MustPersisted(t, accountID)
	var err error
	entitlement, err = entitlement.Activate("profile-a", "preset-a", "release-a", controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatalf("activate entitlement: %v", err)
	}
	policy, err := NewPolicy(entitlement, PolicySpec{
		BillingPeriodID: "period-1",
		Unit:            UnitGBDecimal,
		Basis:           BasisDownlinkOnly,
		IncludedBytes:   100,
		SoftLimitBytes:  150,
		HardLimitBytes:  200,
		GraceBytes:      10,
		Prices: PriceOptions{Global: &Price{
			Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 25000,
		}},
	})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	loginDigest := sha256.Sum256([]byte(accountID))
	db.must(t,
		rqlite.Statement{
			SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,'active',3000000,1,2000000,2000000)`,
			Args: []any{accountID, accountID, hex.EncodeToString(loginDigest[:])},
		},
		rqlite.Statement{
			SQL:  `INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES(?,?,2000000)`,
			Args: []any{policy.EntitlementID(), accountID},
		},
	)
	store, err := NewDurableStore(db)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	return store, policy
}

func meteringEvent(policy Policy, eventID, instanceID string, generation, sequence, down uint64) OrderedUsageEvent {
	return OrderedUsageEvent{
		UsageEvent: UsageEvent{
			EventID: eventID, InstanceID: instanceID, MeterEpoch: "epoch-1",
			XrayIdentity: "wl:" + policy.EntitlementID(), DownlinkBytes: down,
		},
		CounterGeneration: generation,
		SampleSequence:    sequence,
	}
}

func meteringSnapshot(t *testing.T, db *meteringSQLite, policy Policy) string {
	t.Helper()
	args := []any{policy.EntitlementID(), policy.BillingPeriodID()}
	results, err := db.QueryLinearizable(context.Background(),
		rqlite.Statement{SQL: `SELECT policy_sha256 FROM whitelist_metering_periods WHERE entitlement_id=? AND billing_period_id=?`, Args: args},
		rqlite.Statement{SQL: `SELECT instance_id,meter_epoch,xray_identity,counter_generation,sample_sequence,uplink_bytes,downlink_bytes FROM whitelist_metering_checkpoints WHERE entitlement_id=? AND billing_period_id=? ORDER BY instance_id,meter_epoch,xray_identity`, Args: args},
		rqlite.Statement{SQL: `SELECT event_id,payload_sha256,diagnostic,has_interval,result_json FROM whitelist_metering_events WHERE entitlement_id=? AND billing_period_id=? ORDER BY event_id`, Args: args},
		rqlite.Statement{SQL: `SELECT i.event_id,i.uplink_delta_bytes,i.downlink_delta_bytes,i.billable_bytes,i.amount_numerator,i.amount_denominator,i.currency FROM whitelist_metering_intervals i JOIN whitelist_metering_events e ON e.event_id=i.event_id WHERE e.entitlement_id=? AND e.billing_period_id=? ORDER BY i.event_id`, Args: args},
		rqlite.Statement{SQL: `SELECT used_bytes,included_bytes,remaining_bytes,soft_limit_reached,hard_limit_recommended,reconciliation_pending,reconciliation_diagnostic,version FROM whitelist_metering_projections WHERE entitlement_id=? AND billing_period_id=?`, Args: args},
	)
	if err != nil {
		t.Fatalf("metering snapshot: %v", err)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("encode metering snapshot: %v", err)
	}
	return string(encoded)
}

func meteringCheckpointCount(t *testing.T, db *meteringSQLite, policy Policy) int {
	t.Helper()
	results, err := db.QueryLinearizable(context.Background(), rqlite.Statement{
		SQL:  `SELECT instance_id FROM whitelist_metering_checkpoints WHERE entitlement_id=? AND billing_period_id=? ORDER BY instance_id`,
		Args: []any{policy.EntitlementID(), policy.BillingPeriodID()},
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("checkpoint count: results=%#v err=%v", results, err)
	}
	return len(results[0].Rows)
}

type meteringSQLite struct {
	python          string
	path            string
	failNextRequest bool
}

func newMeteringSQLite(t *testing.T) *meteringSQLite {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for durable metering tests")
	}
	return &meteringSQLite{python: python, path: filepath.Join(t.TempDir(), "metering.sqlite")}
}

func (db *meteringSQLite) Request(ctx context.Context, _ rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if db.failNextRequest {
		db.failNextRequest = false
		statements = append(append([]rqlite.Statement(nil), statements...), rqlite.Statement{SQL: `INSERT INTO forced_metering_failure(value) VALUES(1)`})
	}
	return db.execute(ctx, transaction, statements...)
}

func (db *meteringSQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *meteringSQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (*meteringSQLite) Backup(context.Context, io.Writer) error {
	return errors.New("backup is outside the durable metering fixture")
}

func (db *meteringSQLite) failNextTransaction() { db.failNextRequest = true }

func (db *meteringSQLite) must(t *testing.T, statements ...rqlite.Statement) []rqlite.Result {
	t.Helper()
	results, err := db.execute(context.Background(), true, statements...)
	if err != nil {
		t.Fatalf("SQLite fixture: %v", err)
	}
	return results
}

func (db *meteringSQLite) execute(ctx context.Context, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	payload := make([]map[string]any, 0, len(statements))
	for _, statement := range statements {
		args := make([]any, len(statement.Args))
		for index, arg := range statement.Args {
			if blob, ok := arg.([]byte); ok {
				args[index] = map[string]string{"blob": base64.StdEncoding.EncodeToString(blob)}
			} else {
				args[index] = arg
			}
		}
		payload = append(payload, map[string]any{"sql": statement.SQL, "args": args})
	}
	input, err := json.Marshal(map[string]any{"transaction": transaction, "statements": payload})
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, db.python, "-c", meteringSQLiteProgram, db.path)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("metering SQLite process: %w", err)
	}
	var response struct {
		Results []rqlite.Result `json:"results"`
		Error   string          `json:"error"`
		Index   int             `json:"index"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("decode metering SQLite response: %w", err)
	}
	if response.Error != "" {
		return nil, &rqlite.StatementError{Index: response.Index, Message: response.Error}
	}
	return response.Results, nil
}

const meteringSQLiteProgram = `
import base64, json, sqlite3, sys
payload = json.load(sys.stdin)
connection = sqlite3.connect(sys.argv[1], isolation_level=None)
connection.row_factory = sqlite3.Row
connection.create_function("unixepoch", 0, lambda: 2000000)
connection.execute("PRAGMA foreign_keys=ON")
results = []
index = -1
def value(item):
    return base64.b64encode(item).decode() if isinstance(item, bytes) else item
try:
    if payload["transaction"]:
        connection.execute("BEGIN IMMEDIATE")
    for index, statement in enumerate(payload["statements"]):
        args = [base64.b64decode(arg["blob"]) if isinstance(arg, dict) and "blob" in arg else arg for arg in statement.get("args") or []]
        cursor = connection.execute(statement["sql"], args)
        results.append({"Rows": [{key: value(row[key]) for key in row.keys()} for row in cursor.fetchall()], "RowsAffected": max(cursor.rowcount, 0)})
    if payload["transaction"]:
        connection.commit()
    print(json.dumps({"results": results}))
except sqlite3.Error as error:
    if payload["transaction"]:
        connection.rollback()
    print(json.dumps({"error": str(error), "index": index}))
finally:
    connection.close()
`
