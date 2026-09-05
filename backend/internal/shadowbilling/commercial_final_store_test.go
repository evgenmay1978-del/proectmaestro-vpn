package shadowbilling

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCommercialFinalStoreAcceptsOlderOtherOriginWithoutChangingOrdinaryOrdering(t *testing.T) {
	f := newCommercialFinalFixture(t)
	ctx := context.Background()
	observed := f.clock.now.Unix()
	other := f.physicalSource
	other.OriginID += "-other"
	other.CounterSourceID = "xray-api:" + other.OriginID + ":" + other.ExitID
	newer := finalStoreEvent(t, f.store, f.policy, other, "newer-other-origin", 0, 0, observed+1)
	if _, err := f.store.ApplyCommercialOrdered(ctx, newer, f.policy, f.service); err != nil {
		t.Fatal(err)
	}
	ordinary := finalStoreEvent(t, f.store, f.policy, f.physicalSource, "ordinary-old-observation", 19, 23, observed)
	if _, err := f.store.ApplyCommercialOrdered(ctx, ordinary, f.policy, f.service); err == nil {
		t.Fatal("ordinary sample bypassed the existing global timestamp guard")
	}
	f.clock.now = f.clock.now.Add(time.Second)
	result, err := f.store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service)
	if err != nil || result.Decision.Interval == nil || result.Decision.Interval.BillableBytes != 42 {
		t.Fatalf("older authenticated terminal observation did not settle all real bytes: %v", err)
	}
	accepted := finalStoreAccepted(t, f.db, f.final.ReceiptID)
	if accepted.Event.SampledAtUnix != observed || accepted.Event.SampleSequence != 1 || !accepted.FirstCumulative {
		t.Fatal("terminal acceptance changed its observation time or first-counter mode")
	}
	if commercialMeteringCount(t, f.db, "whitelist_metering_events", f.policy.EntitlementID()) != 2 || finalStoreAcceptanceCount(t, f.db) != 1 {
		t.Fatal("rejected ordinary sample or duplicate terminal event was persisted")
	}
	balance, err := f.service.WhiteListBalanceSnapshot(ctx, f.clock.now.Unix(), f.policy.EntitlementID())
	if err != nil || balance.Projection.LifetimeConsumedBytes != 42 || balance.AvailableBytes != 20_000_000-42 {
		t.Fatalf("terminal observation did not debit the existing purchased balance: %v", err)
	}
}

func TestCommercialFinalStoreKeepsSameOriginPeriodAndCounterGuards(t *testing.T) {
	for _, mode := range []string{"newer same origin", "current period closed", "counter regression", "authorization changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommercialFinalFixture(t)
			ctx := context.Background()
			beforeEvents := 0
			switch mode {
			case "newer same origin", "counter regression":
				sampled, up := f.clock.now.Unix()+1, uint64(0)
				if mode == "counter regression" {
					sampled, up = f.clock.now.Unix()-1, 20
				}
				baseline := finalStoreEvent(t, f.store, f.policy, f.physicalSource, "existing-physical-sample", up, 0, sampled)
				if _, err := f.store.ApplyCommercialOrdered(ctx, baseline, f.policy, f.service); err != nil {
					t.Fatal(err)
				}
				f.clock.now = f.clock.now.Add(time.Second)
				beforeEvents = 1
			case "current period closed":
				f.db.must(t, rqlite.Statement{SQL: `UPDATE whitelist_balance_projections SET current_period_id=NULL WHERE entitlement_id=?`, Args: []any{f.policy.EntitlementID()}})
			case "authorization changed":
				f.db.must(t, rqlite.Statement{SQL: `UPDATE idempotency_requests SET request_hash=? WHERE scope='whitelist-final-proof' AND command_type='accept-agent-fence' AND idempotency_key=?`, Args: []any{strings.Repeat("e", 64), f.final.ReceiptID}})
			}
			if _, err := f.store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err == nil {
				t.Fatal("terminal proof bypassed a required source, period, or authorization guard")
			}
			if finalStoreAcceptanceCount(t, f.db) != 0 || commercialMeteringCount(t, f.db, "whitelist_metering_events", f.policy.EntitlementID()) != beforeEvents ||
				commercialMeteringCount(t, f.db, "whitelist_commercial_debit_outbox", f.policy.EntitlementID()) != 0 {
				t.Fatal("failed terminal settlement left an accepted source, event, or debit")
			}
		})
	}
}

type finalStoreFailEventTransaction struct {
	rqlite.RQLite
	failed bool
}

func (db *finalStoreFailEventTransaction) Request(ctx context.Context, level rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if !db.failed && transaction {
		for _, statement := range statements {
			if strings.Contains(statement.SQL, "INSERT INTO whitelist_metering_events") {
				db.failed = true
				statements = append(append([]rqlite.Statement(nil), statements...), rqlite.Statement{SQL: `SELECT abs(-9223372036854775808)`})
				break
			}
		}
	}
	return db.RQLite.Request(ctx, level, transaction, statements...)
}

func TestCommercialFinalStoreRollsBackSequenceBindingWithFailedEvent(t *testing.T) {
	f := newCommercialFinalFixture(t)
	ctx := context.Background()
	failing := &finalStoreFailEventTransaction{RQLite: f.db}
	store, err := NewDurableStore(failing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err == nil || !failing.failed {
		t.Fatal("injected event transaction failure did not occur")
	}
	if finalStoreAcceptanceCount(t, f.db) != 0 || commercialMeteringCount(t, f.db, "whitelist_metering_events", f.policy.EntitlementID()) != 0 {
		t.Fatal("failed event transaction left an independently committed sequence binding")
	}
	// A normal writer can now accept the unused sequence. Because the final
	// event never committed, its retry may resolve the next cursor under the
	// same stable event ID; no accepted operation is reminted.
	baseline := finalStoreEvent(t, f.store, f.policy, f.physicalSource, "racing-normal-zero-pair", 0, 0, f.clock.now.Unix()-1)
	if baseline.SampleSequence != 1 {
		t.Fatal("failed final operation reserved the cursor")
	}
	if _, err := f.store.ApplyCommercialOrdered(ctx, baseline, f.policy, f.service); err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service)
	if err != nil || result.Decision.Interval == nil || result.Decision.Interval.BillableBytes != 42 {
		t.Fatalf("same final event could not settle after a proven rollback: %v", err)
	}
	accepted := finalStoreAccepted(t, f.db, f.final.ReceiptID)
	if accepted.Event.EventID != f.authorization.EventID() || accepted.Event.SampleSequence != 2 || accepted.FirstCumulative {
		t.Fatal("retry lost its stable event identity or reused the accepted normal sequence")
	}
	if _, err := store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err != nil {
		t.Fatal(err)
	}
	if finalStoreAcceptanceCount(t, f.db) != 1 || commercialMeteringCount(t, f.db, "whitelist_commercial_debit_outbox", f.policy.EntitlementID()) != 1 {
		t.Fatal("terminal retry created a second source binding or debit")
	}
}

func TestCommercialFinalStoreResolvesUnknownCommitWithExactAcceptedBinding(t *testing.T) {
	f := newCommercialFinalFixture(t)
	ctx := context.Background()
	unknown := &commercialFirstUnknownCommit{RQLite: f.db, fail: true}
	store, err := NewDurableStore(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err != nil || unknown.fail {
		t.Fatalf("unknown event commit did not resolve its atomic final binding: %v", err)
	}
	accepted := finalStoreAccepted(t, f.db, f.final.ReceiptID)
	restarted, err := NewDurableStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err != nil {
		t.Fatal(err)
	}
	replayed := finalStoreAccepted(t, f.db, f.final.ReceiptID)
	if accepted.Event != replayed.Event || accepted.FirstCumulative != replayed.FirstCumulative || accepted.Event.SampleSequence != 1 || !accepted.FirstCumulative {
		t.Fatal("unknown commit replay changed its accepted event, sequence, or arithmetic mode")
	}
	if finalStoreAcceptanceCount(t, f.db) != 1 || commercialMeteringCount(t, f.db, "whitelist_commercial_metering_sources", f.policy.EntitlementID()) != 1 ||
		commercialMeteringCount(t, f.db, "whitelist_commercial_debit_outbox", f.policy.EntitlementID()) != 1 {
		t.Fatal("unknown commit replay duplicated accounting rows")
	}
	balance, err := f.service.WhiteListBalanceSnapshot(ctx, f.clock.now.Unix(), f.policy.EntitlementID())
	if err != nil || balance.Projection.LifetimeConsumedBytes != 42 || balance.AvailableBytes != 20_000_000-42 {
		t.Fatalf("unknown commit replay did not debit exactly once: %v", err)
	}
}

func finalStoreEvent(t *testing.T, store *DurableStore, policy Policy, physical CommercialMeterSource, eventID string, up, down uint64, observed int64) CommercialOrderedUsageEvent {
	t.Helper()
	cursor, err := store.EnsureCommercialProducerCursor(context.Background(), physical, observed)
	if err != nil {
		t.Fatal(err)
	}
	event := commercialMeteringEvent(policy, cursor.Source, eventID, 1, cursor.NextSampleSequence, up, down, observed)
	event.MeterEpoch = cursor.MeterEpoch
	return event
}

func finalStoreAccepted(t *testing.T, db rqlite.RQLite, receiptID string) commercialFinalAccepted {
	t.Helper()
	results, err := db.QueryLinearizable(context.Background(), commercialFinalAcceptedRead(receiptID))
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("read accepted final binding: %v", err)
	}
	body, err := rowString(results[0].Rows[0], "response_json")
	var accepted commercialFinalAccepted
	if err != nil || json.Unmarshal([]byte(body), &accepted) != nil {
		t.Fatal("invalid accepted final binding")
	}
	return accepted
}

func TestCommercialFinalStoreRejectsUnprovenOrChangedOperationBeforeAcceptance(t *testing.T) {
	for _, mode := range []string{"missing authorization", "changed sequence", "changed arithmetic mode"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := newMeteringSQLite(t)
			if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
				t.Fatal(err)
			}
			store, policy, source := newCommercialMeteringFixture(t, db, "final-unproven")
			policy.SoftLimitBytes, policy.HardLimitBytes, policy.GraceBytes = 0, 0, 0
			policy.Prices = PriceOptions{Global: &Price{Mode: PriceFree}}
			event := commercialMeteringEvent(policy, source, "final:unproven", 1, 1, 11, 17, 2_000_010)
			proof := &commercialFinalProof{event: event, firstCumulative: true}
			switch mode {
			case "changed sequence":
				proof.event.SampleSequence++
			case "changed arithmetic mode":
				proof.firstCumulative = false
			}
			debiter := &commercialDebitRecorder{db: db}
			if _, err := store.applyCommercialFinalOrdered(ctx, event, policy, debiter, true, proof); err == nil {
				t.Fatal("unproven final operation was accepted")
			}
			for _, table := range []string{"whitelist_metering_events", "whitelist_commercial_metering_sources", "whitelist_commercial_debit_outbox"} {
				if commercialMeteringCount(t, db, table, policy.EntitlementID()) != 0 {
					t.Fatalf("rejected final operation wrote %s", table)
				}
			}
			if finalStoreAcceptanceCount(t, db) != 0 || len(debiter.attempts) != 0 {
				t.Fatal("unproven final operation created an acceptance or attempted a debit")
			}
		})
	}
}

func finalStoreAcceptanceCount(t *testing.T, db rqlite.RQLite) int {
	t.Helper()
	results, err := db.QueryLinearizable(context.Background(), rqlite.Statement{
		SQL: `SELECT idempotency_key FROM idempotency_requests
WHERE scope='whitelist-final-metering' AND command_type='accept-final-source'`,
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("read final acceptance rows: %v", err)
	}
	return len(results[0].Rows)
}
