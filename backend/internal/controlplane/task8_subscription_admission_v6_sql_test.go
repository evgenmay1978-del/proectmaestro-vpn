package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSubscriptionZeroCredentialAdmissionPolicySQLite(t *testing.T) {
	for _, test := range []struct {
		name       string
		suffix     string
		wantStatus int
		wantWrite  bool
	}{
		{name: "base", wantStatus: http.StatusForbidden},
		{name: "links", suffix: "?format=links", wantStatus: http.StatusForbidden},
		{name: "helpers", suffix: "/helpers", wantStatus: http.StatusOK, wantWrite: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newF5SubscriptionFixture(t, nil)
			fixture.sqlite.must(t, rqlite.Statement{SQL: `DELETE FROM credentials WHERE customer_id=?`, Args: []any{fixture.customerID}})
			before := f5ReadAdmissionDurableState(t, fixture)

			got := f5SubscriptionGET(t, fixture.handler, fixture.path(test.suffix), "credentialless-device", f5CacheUserAgent)
			if got.status != test.wantStatus {
				t.Fatalf("status=%d body=%q, want %d", got.status, got.body, test.wantStatus)
			}
			after := f5ReadAdmissionDurableState(t, fixture)
			if !test.wantWrite {
				if after != before {
					t.Fatalf("credential-required endpoint admitted device: before=%+v after=%+v", before, after)
				}
				return
			}
			if after.Devices != before.Devices+1 || after.Audits != before.Audits+1 || after.DirtyGeneration != before.DirtyGeneration+1 {
				t.Fatalf("credential-optional helpers durable delta: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestSubscriptionAdmissionUsesDatabaseExpiryBoundarySQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	databaseNow := f5DatabaseNow(t, fixture)
	fixture.clock.set(time.Unix(databaseNow-1, 0).UTC())
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE customers SET expires_at_unix=? WHERE customer_id=?`, Args: []any{databaseNow, fixture.customerID},
	})
	command := f5SubscriptionClaimCommand(t, fixture, "database-boundary-device", true, 5)
	before := f5ReadAdmissionDurableState(t, fixture)

	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); !errors.Is(err, controlplane.ErrSubscriptionChanged) {
		t.Fatalf("expiry equal to database time error=%v, want ErrSubscriptionChanged", err)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after != before {
		t.Fatalf("database expiry boundary wrote durable state: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionHTTPUsesDatabaseTimeAtExpiryBoundarySQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	databaseNow := f5DatabaseNow(t, fixture)
	fixture.clock.set(time.Unix(databaseNow-60, 0).UTC())
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE customers SET expires_at_unix=? WHERE customer_id=?`, Args: []any{databaseNow, fixture.customerID},
	})
	before := f5ReadAdmissionDurableState(t, fixture)

	got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "database-time-http-device", f5CacheUserAgent)
	if got.status != http.StatusPaymentRequired {
		t.Fatalf("app clock behind database expiry status=%d body=%q, want 402", got.status, got.body)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after != before {
		t.Fatalf("database expiry HTTP boundary wrote durable state: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionHTTPPostVerifyExpiryBoundaryUsesAdmissionTimeSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	databaseNow := f5DatabaseNow(t, fixture)
	expiresAt := databaseNow + 60
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE customers SET expires_at_unix=? WHERE customer_id=?`, Args: []any{expiresAt, fixture.customerID},
	})
	fixture.database.strongCalls = 0
	fixture.database.rewriteStrong = func(call int, results []rqlite.Result) []rqlite.Result {
		if call < 2 {
			return results
		}
		for resultIndex := range results {
			for rowIndex := range results[resultIndex].Rows {
				results[resultIndex].Rows[rowIndex]["database_now_unix"] = expiresAt
			}
		}
		return results
	}
	claimCalls := 0
	fixture.database.rewriteRequest = func(statements []rqlite.Statement) []rqlite.Statement {
		if len(statements) == 4 && strings.Contains(statements[0].SQL, "INSERT INTO devices") {
			claimCalls++
		}
		return statements
	}
	before := f5ReadAdmissionDurableState(t, fixture)

	got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "post-verify-boundary-device", f5CacheUserAgent)
	if got.status != http.StatusOK || !f5HasOutboundType(t, got.body, "naive") {
		t.Fatalf("post-verify expiry boundary status=%d body=%q, want current 200 document", got.status, got.body)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after.Devices != before.Devices+1 || after.Audits != before.Audits+1 || after.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("post-verify boundary durable delta: before=%+v after=%+v", before, after)
	}
	if claimCalls != 1 {
		t.Fatalf("post-verify boundary claim calls=%d, want 1", claimCalls)
	}

	next := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "post-verify-boundary-device", f5CacheUserAgent)
	if next.status != http.StatusPaymentRequired {
		t.Fatalf("next request at DB expiry status=%d body=%q, want 402", next.status, next.body)
	}
	if final := f5ReadAdmissionDurableState(t, fixture); final != after {
		t.Fatalf("expired next request changed durable state: after=%+v final=%+v", after, final)
	}
	if claimCalls != 1 {
		t.Fatalf("expired next request added claim: calls=%d", claimCalls)
	}
}

func TestSubscriptionAdmissionMutationResultSurvivesFinalClockCrossingSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	fixture.database.rewriteRequest = func(statements []rqlite.Statement) []rqlite.Statement {
		clone := append([]rqlite.Statement(nil), statements...)
		if len(clone) == 4 && strings.Contains(clone[0].SQL, "INSERT INTO devices") {
			clone[3] = rqlite.Statement{SQL: `SELECT 1 AS state_changed,0 AS admitted,NULL AS device_id,0 AS at_limit,unixepoch()+10 AS database_now_unix`}
		}
		return clone
	}
	command := f5SubscriptionClaimCommand(t, fixture, "crossing-device", true, 5)
	before := f5ReadAdmissionDurableState(t, fixture)

	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); err != nil {
		t.Fatalf("admission committed before final clock crossing was reported as failure: %v", err)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after.Devices != before.Devices+1 || after.Audits != before.Audits+1 || after.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("crossing admission durable delta: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionUnlimitedAdmissionStillRecordsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 0 })
	before := f5ReadAdmissionDurableState(t, fixture)
	got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "unlimited-device", f5CacheUserAgent)
	if got.status != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", got.status, got.body)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after.Devices != before.Devices+1 || after.Audits != before.Audits+1 || after.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("unlimited admission durable delta: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionAdmissionUnexpectedNoInsertFailsInvalidStateSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	fixture.database.rewriteRequest = func(statements []rqlite.Statement) []rqlite.Statement {
		clone := append([]rqlite.Statement(nil), statements...)
		if len(clone) == 4 && strings.Contains(clone[0].SQL, "INSERT INTO devices") {
			clone[0].SQL = strings.Replace(clone[0].SQL, "SELECT ?,?,?,?,?,0,? WHERE ", "SELECT ?,?,?,?,?,0,? WHERE 0 AND ", 1)
		}
		return clone
	}
	command := f5SubscriptionClaimCommand(t, fixture, "forced-no-insert-device", true, 0)
	before := f5ReadAdmissionDurableState(t, fixture)

	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); !errors.Is(err, controlplane.ErrInvalidState) {
		t.Fatalf("eligible unexpected no-insert error=%v, want ErrInvalidState", err)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after != before {
		t.Fatalf("unexpected no-insert changed durable state: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionSameSecondDeviceMutationsHaveDistinctAuditSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	command := f5SubscriptionClaimCommand(t, fixture, "same-second-device", true, 5)
	before := f5ReadAdmissionDurableState(t, fixture)
	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	first := f5ReadAdmissionDurableState(t, fixture)
	if first.Devices != before.Devices+1 || first.Audits != before.Audits+1 || first.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("first mutation durable delta: before=%+v after=%+v", before, first)
	}

	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE devices SET revoked=1 WHERE customer_id=?`, Args: []any{fixture.customerID},
	})
	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); err != nil {
		t.Fatalf("same-second reactivation: %v", err)
	}
	second := f5ReadAdmissionDurableState(t, fixture)
	if second.Devices != first.Devices || second.Audits != first.Audits+1 || second.DirtyGeneration != first.DirtyGeneration+1 {
		t.Fatalf("second mutation durable delta: first=%+v second=%+v", first, second)
	}

	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); err != nil {
		t.Fatalf("same-second no-op replay: %v", err)
	}
	third := f5ReadAdmissionDurableState(t, fixture)
	if third != second {
		t.Fatalf("no-op replay emitted durable mutation: second=%+v third=%+v", second, third)
	}
}

func TestSubscriptionAuditCollisionRollsBackDeviceAndDirtyMutationSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixtureWithIDs(t, nil, f5RepeatingIDs{})
	command := f5SubscriptionClaimCommand(t, fixture, "audit-collision-device", true, 5)
	before := f5ReadAdmissionDurableState(t, fixture)
	firstClaim, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command)
	if err != nil || firstClaim.AdmittedAtUnix <= 0 {
		t.Fatalf("first claim=%+v err=%v", firstClaim, err)
	}
	first := f5ReadAdmissionDurableState(t, fixture)
	if first.Devices != before.Devices+1 || first.Audits != before.Audits+1 || first.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("first collision fixture delta: before=%+v first=%+v", before, first)
	}

	noOpClaim, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command)
	if err != nil || noOpClaim.AdmittedAtUnix <= 0 {
		t.Fatalf("no-op claim=%+v err=%v", noOpClaim, err)
	}
	if noOp := f5ReadAdmissionDurableState(t, fixture); noOp != first {
		t.Fatalf("no-op claim changed durable state: first=%+v no_op=%+v", first, noOp)
	}

	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE devices SET revoked=1 WHERE customer_id=?`, Args: []any{fixture.customerID},
	})
	if revoked := f5ReadDeviceRevoked(t, fixture); revoked != 1 {
		t.Fatalf("revoked fixture value=%d, want 1", revoked)
	}
	preCollision := f5ReadAdmissionDurableState(t, fixture)
	if _, err := fixture.service.ClaimSubscriptionDevice(context.Background(), command); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("audit collision error=%v, want ErrUnavailable", err)
	}
	postCollision := f5ReadAdmissionDurableState(t, fixture)
	if postCollision != preCollision || f5ReadDeviceRevoked(t, fixture) != 1 {
		t.Fatalf("audit collision did not roll back: before=%+v after=%+v revoked=%d", preCollision, postCollision, f5ReadDeviceRevoked(t, fixture))
	}
}

func TestSubscriptionRestoreEpochRollbackReplacesCachedSecretsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE customers SET generation=5 WHERE customer_id=?`, Args: []any{fixture.customerID}})
	warm := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "restore-device", f5CacheUserAgent)
	if warm.status != http.StatusOK {
		t.Fatalf("warm status=%d body=%q", warm.status, warm.body)
	}

	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE backup_rpo_state SET restore_epoch=2 WHERE singleton_id=1`})
	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE cluster_restore_state SET restore_epoch=2 WHERE singleton_id=1`})
	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE customers SET generation=1 WHERE customer_id=?`, Args: []any{fixture.customerID}})
	f5ReplaceCredential(t, fixture, "naive", "f5-restored-naive-password")

	restored := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "restore-device", f5CacheUserAgent)
	if restored.status != http.StatusOK || bytes.Equal(restored.body, warm.body) {
		t.Fatalf("restored status=%d changed=%v body=%q", restored.status, !bytes.Equal(restored.body, warm.body), restored.body)
	}
	fixture.database.setUnavailable(true)
	cached := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "restore-device", f5CacheUserAgent)
	if cached.status != http.StatusOK || !bytes.Equal(cached.body, restored.body) {
		t.Fatalf("restore cache status=%d restored_equal=%v", cached.status, bytes.Equal(cached.body, restored.body))
	}
}

func f5SubscriptionClaimCommand(t *testing.T, fixture *f5SubscriptionFixture, device string, requireCredentials bool, limit int) controlplane.SubscriptionDeviceClaimCommand {
	t.Helper()
	state, err := fixture.service.BusinessSubscriptionSnapshot(context.Background(), fixture.token, device)
	if err != nil {
		t.Fatalf("strong subscription snapshot: %v", err)
	}
	return controlplane.SubscriptionDeviceClaimCommand{
		CustomerID: state.Customer.ID, TokenHMAC: state.TokenKeyHMAC, RawDeviceIdentity: device,
		Platform: "maestro", Limit: limit, RequireCredentials: requireCredentials,
		ExpectedCustomerGeneration: state.Customer.Generation, ExpectedTokenGeneration: state.TokenGeneration,
		ExpectedExpiresAtUnix: state.Customer.ExpiresAtUnix, ExpectedRestoreEpoch: state.RestoreEpoch,
	}
}

func f5DatabaseNow(t *testing.T, fixture *f5SubscriptionFixture) int64 {
	t.Helper()
	results := fixture.sqlite.must(t, rqlite.Statement{SQL: `SELECT unixepoch() AS database_now_unix`})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("database now rows=%#v", results)
	}
	value, ok := f5Int64(results[0].Rows[0]["database_now_unix"])
	if !ok || value <= 0 {
		t.Fatalf("database now value=%#v", results[0].Rows[0]["database_now_unix"])
	}
	return value
}

func f5ReplaceCredential(t *testing.T, fixture *f5SubscriptionFixture, protocol, plaintext string) {
	t.Helper()
	envelope, err := fixture.box.Seal(controlplane.SecretScope{
		OwnerType: "customer", OwnerID: fixture.customerID, Field: "credential", Kind: protocol,
	}, []byte(plaintext))
	if err != nil {
		t.Fatalf("seal replacement credential: %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal replacement credential: %v", err)
	}
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `UPDATE credentials SET secret_envelope=?,secret_sha256=?,generation=1 WHERE customer_id=? AND protocol=?`,
		Args: []any{encoded, strings.Repeat("c", 64), fixture.customerID, protocol},
	})
}

type f5RepeatingIDs struct{}

func (f5RepeatingIDs) NewID(prefix string) (string, error) {
	return prefix + "-" + strings.Repeat("f", 32), nil
}

func f5ReadDeviceRevoked(t *testing.T, fixture *f5SubscriptionFixture) int64 {
	t.Helper()
	results := fixture.sqlite.must(t, rqlite.Statement{
		SQL: `SELECT revoked FROM devices WHERE customer_id=? LIMIT 1`, Args: []any{fixture.customerID},
	})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("device revoked rows=%#v", results)
	}
	revoked, ok := f5Int64(results[0].Rows[0]["revoked"])
	if !ok {
		t.Fatalf("device revoked value=%#v", results[0].Rows[0]["revoked"])
	}
	return revoked
}
