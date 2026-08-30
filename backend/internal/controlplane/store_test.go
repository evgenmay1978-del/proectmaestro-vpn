package controlplane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type recordedCall struct {
	level       rqlite.Consistency
	transaction bool
	statements  []rqlite.Statement
}

type scriptedResult struct {
	results []rqlite.Result
	err     error
}

func rowsScript(rows ...map[string]any) scriptedResult {
	return scriptedResult{results: []rqlite.Result{{Rows: rows}}}
}

func authorizationRowsScript(secrets *SecretBox, csrf string, rows ...map[string]any) scriptedResult {
	csrfHMAC := secrets.LookupHMAC("web-csrf", []byte(csrf))
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		copyRow := make(map[string]any, len(row)+1)
		for key, value := range row {
			copyRow[key] = value
		}
		copyRow["csrf_hmac"] = csrfHMAC
		result[index] = copyRow
	}
	return rowsScript(result...)
}

func resultsScript(results ...rqlite.Result) scriptedResult {
	return scriptedResult{results: results}
}

type recordingRQLite struct {
	mu sync.Mutex

	linear   []scriptedResult
	strong   []scriptedResult
	requests []scriptedResult

	linearCalls  []recordedCall
	strongCalls  []recordedCall
	requestCalls []recordedCall
	requestFn    func([]rqlite.Statement) ([]rqlite.Result, error)
}

func (f *recordingRQLite) Request(
	_ context.Context,
	level rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestCalls = append(f.requestCalls, recordedCall{level: level, transaction: transaction, statements: cloneStatements(statements)})
	if f.requestFn != nil {
		return f.requestFn(statements)
	}
	return popScripted(&f.requests)
}

func (f *recordingRQLite) QueryLinearizable(
	_ context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linearCalls = append(f.linearCalls, recordedCall{level: rqlite.Linearizable, statements: cloneStatements(statements)})
	return popScripted(&f.linear)
}

func (f *recordingRQLite) QueryStrong(
	_ context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strongCalls = append(f.strongCalls, recordedCall{level: rqlite.Strong, statements: cloneStatements(statements)})
	return popScripted(&f.strong)
}

func (f *recordingRQLite) Backup(context.Context, io.Writer) error { return nil }

func popScripted(queue *[]scriptedResult) ([]rqlite.Result, error) {
	if len(*queue) == 0 {
		return nil, errors.New("unexpected database call")
	}
	item := (*queue)[0]
	*queue = (*queue)[1:]
	return item.results, item.err
}

func cloneStatements(statements []rqlite.Statement) []rqlite.Statement {
	out := make([]rqlite.Statement, len(statements))
	for i, statement := range statements {
		out[i] = rqlite.Statement{
			SQL:  statement.SQL,
			Args: append([]any(nil), statement.Args...),
		}
	}
	return out
}

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

type sequenceIDs struct {
	mu      sync.Mutex
	counter int
}

func (s *sequenceIDs) NewID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("%s_%032x", prefix, s.counter), nil
}

func testService(t *testing.T, db rqlite.RQLite) (*Service, *SecretBox) {
	t.Helper()
	encryptionKey := bytes.Repeat([]byte{0x61}, 32)
	hmacKey := bytes.Repeat([]byte{0x62}, 32)
	secrets, err := NewSecretBox(1, map[int][]byte{1: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	clock := fixedClock{value: time.Unix(2_000_000, 0)}
	store, err := NewStore(db, secrets, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := NewService(store, &sequenceIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, secrets
}

func TestTariffSnapshotIsImmutable(t *testing.T) {
	rows := []map[string]any{{
		"tariff_version_id": "tariff_1m_v1",
		"tariff_code":       "1m",
		"duration_days":     30,
		"amount_minor":      40000,
		"currency":          "RUB",
	}}
	db := &recordingRQLite{linear: []scriptedResult{
		{results: []rqlite.Result{{Rows: rows}}},
		{results: []rqlite.Result{{Rows: rows}}},
	}}
	service, _ := testService(t, db)

	first, err := service.Tariffs(context.Background())
	if err != nil {
		t.Fatalf("Tariffs: %v", err)
	}
	first[0].AmountMinor = 1
	second, err := service.Tariffs(context.Background())
	if err != nil {
		t.Fatalf("Tariffs second: %v", err)
	}
	if second[0].AmountMinor != 40000 {
		t.Fatalf("caller mutated tariff snapshot: %#v", second)
	}
	if len(db.linearCalls) != 2 {
		t.Fatalf("tariff reads = %d, want two linearizable reads", len(db.linearCalls))
	}
}

func TestMutableSettingsUseVersionCASAndAudit(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": 2}}},
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	result, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key:                "ota",
		ExpectedGeneration: 1,
		PublicValueJSON:    `{"version_code":155}`,
		Members:            []string{"owner-a", "owner-b"},
		Actor:              "owner",
	})
	if err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation = %d, want 2", result.Generation)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("setting update was not one transaction: %#v", db.requestCalls)
	}
	assertBackupDirtyImmediatelyAfter(t, db.requestCalls[0].statements, 0)
	joined := statementsText(db.requestCalls[0].statements)
	for _, required := range []string{"cluster_settings", "setting_members", "audit_events", "generation"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("transaction lacks %s: %s", required, joined)
		}
	}
	for _, statement := range db.requestCalls[0].statements {
		for _, arg := range statement.Args {
			if arg == "owner-a" || arg == "owner-b" {
				t.Fatalf("raw allowlist identity reached SQL args: %#v", statement.Args)
			}
		}
	}
}

func TestSettingSecretReferenceCASIsAtomic(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": 1}}},
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, secrets := testService(t, db)
	envelope, err := secrets.Seal(
		SecretScope{OwnerType: "setting", OwnerID: "telegram", Field: "token", Kind: "bot-token"},
		[]byte("secret"),
	)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	_, err = service.UpdateSetting(context.Background(), SettingUpdate{
		Key:                "telegram",
		ExpectedGeneration: 0,
		PublicValueJSON:    `{"enabled":true}`,
		Secret:             &envelope,
		Actor:              "owner",
	})
	if err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatal("setting public value and secret reference were not atomic")
	}
	assertBackupDirtyImmediatelyAfter(t, db.requestCalls[0].statements, 0)
	joined := statementsText(db.requestCalls[0].statements)
	if !strings.Contains(joined, "setting_secrets") || !strings.Contains(joined, "audit_events") {
		t.Fatalf("secret transaction incomplete: %s", joined)
	}
	for _, statement := range db.requestCalls[0].statements {
		for _, arg := range statement.Args {
			switch value := arg.(type) {
			case string:
				if value == "secret" {
					t.Fatal("SQL args contain plaintext secret material")
				}
			case []byte:
				if bytes.Equal(value, []byte("secret")) {
					t.Fatal("SQL args contain plaintext secret material")
				}
			}
		}
	}
}

func TestSessionCookieContract(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{rowsScript(map[string]any{
			"principal_id":     "principal-1",
			"status":           "active",
			"revocation_epoch": 4,
		})},
		requests: []scriptedResult{resultsScript(rqlite.Result{RowsAffected: 1})},
	}
	service, _ := testService(t, db)
	session, err := service.CreateSession(context.Background(), "principal-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cookie := session.Cookie
	if cookie.Name != "maestro_session" || cookie.Value == "" || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 1800 {
		t.Fatalf("cookie contract = %#v", cookie)
	}
	if session.CSRFToken == "" || !session.ExpiresAt.Equal(time.Unix(2_001_800, 0)) {
		t.Fatalf("session result = %#v", session)
	}
	args := fmt.Sprint(db.requestCalls[0].statements[0].Args)
	if strings.Contains(args, cookie.Value) || strings.Contains(args, session.CSRFToken) {
		t.Fatal("raw session or CSRF token reached durable SQL")
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if strings.Contains(sql, "backup_rpo_state") {
		t.Fatalf("CreateSession dirtied backup state: %s", sql)
	}
}

func TestRevocationEpochInvalidatesExistingSession(t *testing.T) {
	db := &recordingRQLite{
		requests: []scriptedResult{resultsScript(
			rqlite.Result{Rows: []map[string]any{{"revocation_epoch": 2}}},
			rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1},
		)},
	}
	service, secrets := testService(t, db)
	db.linear = []scriptedResult{
		authorizationRowsScript(secrets, "csrf-token", map[string]any{
			"principal_id": "principal-1",
			"role_name":    "owner",
		}),
		resultsScript(rqlite.Result{Rows: nil}),
	}
	if _, err := service.Authorize(context.Background(), "session-token", "csrf-token", PermissionPaymentDecide); err != nil {
		t.Fatalf("Authorize before revoke: %v", err)
	}
	if err := service.RevokeSessions(context.Background(), "principal-1", "owner"); err != nil {
		t.Fatalf("RevokeSessions: %v", err)
	}
	if _, err := service.Authorize(context.Background(), "session-token", "csrf-token", PermissionPaymentDecide); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authorize after revoke = %v, want ErrUnauthenticated", err)
	}
	joined := statementsText(db.requestCalls[0].statements)
	if !strings.Contains(joined, "revocation_epoch") || !strings.Contains(joined, "audit_events") {
		t.Fatalf("revocation transaction incomplete: %s", joined)
	}
	assertBackupDirtyImmediatelyAfter(t, db.requestCalls[0].statements, 0)
}

func TestRevokeSessionsMissingPrincipalCannotCommitAuditOnlySuccess(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	err := service.RevokeSessions(context.Background(), "missing-principal", "owner")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RevokeSessions error = %v, want ErrNotFound", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("revoke was not one transaction: %#v", db.requestCalls)
	}
	statements := db.requestCalls[0].statements
	assertBackupDirtyImmediatelyAfter(t, statements, 0)
	for index, statement := range statements[2:] {
		if !containsAll(statement.SQL, "WHERE", "principals", "principal_id") {
			t.Fatalf("dependent revoke statement %d is not principal-gated: %s", index+2, statement.SQL)
		}
	}
}

func TestPrincipalRolesAreNormalizedAndDefaultDeny(t *testing.T) {
	db := &recordingRQLite{}
	service, secrets := testService(t, db)
	db.linear = []scriptedResult{
		authorizationRowsScript(secrets, "c1", map[string]any{"principal_id": "p-owner", "role_name": "owner"}),
		authorizationRowsScript(secrets, "c2", map[string]any{"principal_id": "p-admin", "role_name": "admin"}),
		authorizationRowsScript(secrets, "c3", map[string]any{"principal_id": "p-other", "role_name": "unknown"}),
	}
	if _, err := service.Authorize(context.Background(), "s1", "c1", PermissionCriticalSettings); err != nil {
		t.Fatalf("owner critical authorization: %v", err)
	}
	if _, err := service.Authorize(context.Background(), "s2", "c2", PermissionProvision); err != nil {
		t.Fatalf("admin provision authorization: %v", err)
	}
	if _, err := service.Authorize(context.Background(), "s3", "c3", PermissionPaymentDecide); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown role authorization = %v, want deny", err)
	}
}

func statementsText(statements []rqlite.Statement) string {
	var builder strings.Builder
	for _, statement := range statements {
		builder.WriteString(statement.SQL)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func TestSettingCASGatesEveryDependentMutation(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": 2}}},
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	_, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "ota", ExpectedGeneration: 1, PublicValueJSON: `{"version_code":155}`,
		Members: []string{"owner-a"}, Actor: "owner",
	})
	if err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	statements := db.requestCalls[0].statements
	assertBackupDirtyImmediatelyAfter(t, statements, 0)
	mutationToken, ok := statements[0].Args[4].(string)
	if !ok || !strings.HasPrefix(mutationToken, "setting-mut_") {
		t.Fatalf("CAS mutation token = %#v", statements[0].Args[4])
	}
	if !strings.Contains(statements[0].SQL, "last_mutation_token") {
		t.Fatalf("CAS does not persist mutation token: %s", statements[0].SQL)
	}
	if !containsAll(statements[1].SQL, "changes() > 0", "last_mutation_token = ?") ||
		!statementHasStringArg(statements[1], mutationToken) {
		t.Fatalf("dirty statement lacks exact CAS token proof: %#v", statements[1])
	}
	for index, statement := range statements[2:] {
		if !containsAll(statement.SQL, "SELECT 1 FROM cluster_settings", "generation = ?", "last_mutation_token = ?") ||
			!statementHasStringArg(statement, mutationToken) {
			t.Fatalf("dependent statement %d is not gated by successful CAS token: %s", index+2, statement.SQL)
		}
	}
}

func statementHasStringArg(statement rqlite.Statement, want string) bool {
	for _, arg := range statement.Args {
		if value, ok := arg.(string); ok && value == want {
			return true
		}
	}
	return false
}

func TestSettingFailedCASCannotMarkBackupDirty(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	_, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "ota", ExpectedGeneration: 7, PublicValueJSON: `{"version_code":155}`, Actor: "owner",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateSetting error = %v, want ErrConflict", err)
	}
	assertBackupDirtyImmediatelyAfter(t, db.requestCalls[0].statements, 0)
}

func TestReadOnlyCallsNeverOpenDirtyMutationTransaction(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(map[string]any{
			"customer_id": "customer-1", "status": "active", "expires_at_unix": int64(2_100_000), "generation": int64(7),
		}),
		rowsScript(map[string]any{"customer_id": "account-a", "entitlement_id": task7PersistedEntitlementID}),
	}}
	service, _ := testService(t, db)
	if _, err := service.CustomerByToken(context.Background(), "token"); err != nil {
		t.Fatalf("CustomerByToken: %v", err)
	}
	if _, err := service.WhiteListEntitlementByID(context.Background(), task7PersistedEntitlementID); err != nil {
		t.Fatalf("WhiteListEntitlementByID: %v", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("read-only calls opened mutation transactions: %#v", db.requestCalls)
	}
}

func TestAuthorizeUsesAnyExplicitRole(t *testing.T) {
	db := &recordingRQLite{}
	service, secrets := testService(t, db)
	db.linear = []scriptedResult{authorizationRowsScript(
		secrets,
		"csrf",
		map[string]any{"principal_id": "p-owner", "role_name": "admin"},
		map[string]any{"principal_id": "p-owner", "role_name": "owner"},
	)}
	if _, err := service.Authorize(context.Background(), "session", "csrf", PermissionCriticalSettings); err != nil {
		t.Fatalf("owner role was ignored when another explicit role sorted first: %v", err)
	}
}
