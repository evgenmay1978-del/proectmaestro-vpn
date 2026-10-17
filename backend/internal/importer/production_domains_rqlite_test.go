//go:build rqlite_integration

package importer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type productionDomainReaderIDs struct{ next int }

func (ids *productionDomainReaderIDs) NewID(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("domain-%s-%d", prefix, ids.next), nil
}

type productionDomainReaderClock struct{ now time.Time }

func (clock productionDomainReaderClock) Now() time.Time { return clock.now }

func TestProductionDomainsRoundTripActualSettingReaderAndPasswordCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for the isolated production-reader fixture")
	}
	db := &productionDomainSQLite{python: python, path: filepath.Join(t.TempDir(), "domains.sqlite")}
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, box, settingRaw, verifier, password := productionDomainsFixture(t)
	snapshot.CapturedAt = time.Now().UTC()
	snapshot.Settings = append(snapshot.Settings, LegacySetting{Key: "ota", PublicValueJSON: json.RawMessage(`{"versionCode":154,"versionName":"1.5.4","sha256":"` + strings.Repeat("5", 64) + `","size":12345678}`), Generation: 1})
	clock := productionDomainReaderClock{now: snapshot.CapturedAt}
	validated, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(db, clock.Now, validated, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("production domain plan blocked")
	}
	options := ApplyOptions{RunID: "production-domains-readers-v1", BatchSize: 64}
	applied, err := Apply(ctx, store, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	readerStore, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(readerStore, &productionDomainReaderIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.ReadSettingSecret(ctx, snapshot.Settings[0].Key)
	if err != nil || !bytes.Equal(got, settingRaw) {
		t.Fatal("actual setting reader cannot recover original secret")
	}
	zeroBytes(got)
	session, err := service.AuthenticatePassword(ctx, password)
	if err != nil || session.Cookie.Value == "" || session.CSRFToken == "" {
		t.Fatal("original bcrypt no longer authenticates with actual production permission checks")
	}
	if _, err := service.AuthenticatePassword(ctx, password+"-wrong"); !errors.Is(err, controlplane.ErrUnauthenticated) {
		t.Fatal("wrong password was not rejected by actual reader")
	}
	authorized, err := service.Authorize(ctx, session.Cookie.Value, session.CSRFToken, controlplane.PermissionCriticalSettings)
	if err != nil || authorized.PrincipalID != plan.Principals[0].InternalID || authorized.Role != "owner" {
		t.Fatal("actual owner permission check failed")
	}
	if _, err := service.Authorize(ctx, session.Cookie.Value, session.CSRFToken+"-wrong", controlplane.PermissionCriticalSettings); !errors.Is(err, controlplane.ErrForbidden) {
		t.Fatal("actual CSRF gate was weakened")
	}
	legacy, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := ShadowFromCandidate(ctx, store, plan.SourceDigest, validShadowShapes())
	if err != nil || !reflect.DeepEqual(legacy, candidate) {
		t.Fatal("full production export differs from original source")
	}
	projection, err := store.ReadShadowProjection(ctx, plan.SourceDigest)
	if err != nil || len(projection.Principals) != 1 || projection.Principals[0].VerifierKeyVersion != 6 {
		t.Fatal("production projection did not preserve real principal version")
	}
	evidence, err := store.ReadAppliedRunEvidence(ctx, options.RunID)
	if err != nil || evidence.TargetDigest != applied.TargetDigest || evidence.SourceDigest != plan.SourceDigest || evidence.BatchCount != 1 {
		t.Fatal("complete apply evidence unavailable")
	}
	versions, err := store.ReadReferencedKeyVersions(ctx)
	if err != nil || !reflect.DeepEqual(versions, []int{6}) {
		t.Fatal("complete apply key inventory differs from stored envelopes")
	}
	reads := []rqlite.Statement{{SQL: `SELECT setting_key,secret_envelope,secret_sha256,key_version FROM setting_secrets ORDER BY setting_key`}, {SQL: `SELECT principal_id,verifier_envelope,verifier_sha256 FROM principal_credentials ORDER BY principal_id`}}
	before, err := db.QueryLinearizable(ctx, reads...)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Apply(ctx, store, plan, options)
	if err != nil || replay.TargetDigest != applied.TargetDigest || replay.AppliedBatches != applied.AppliedBatches {
		t.Fatal("exact full apply did not use durable receipt")
	}
	after, err := db.QueryLinearizable(ctx, reads...)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("exact replay changed encrypted secret or verifier")
	}
	for _, rows := range after {
		for _, row := range rows.Rows {
			raw, _ := json.Marshal(row)
			if bytes.Contains(raw, settingRaw) || bytes.Contains(raw, []byte(password)) {
				t.Fatal("plaintext reached stored SQL rows")
			}
		}
	}
	// A subsequent genuine principal key rotation must appear independently of
	// setting key 6. The old source key is not a substitute for durable inventory.
	scope := controlplane.SecretScope{OwnerType: "principal", OwnerID: plan.Principals[0].InternalID, Field: "password", Kind: "bcrypt"}
	rotated, err := box.Seal(scope, verifier)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rotated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE principal_credentials SET verifier_envelope=? WHERE principal_id=?`, Args: []any{base64.StdEncoding.EncodeToString(raw), scope.OwnerID}}); err != nil {
		t.Fatal(err)
	}
	versions, err = store.ReadReferencedKeyVersions(ctx)
	if err != nil || !reflect.DeepEqual(versions, []int{6, 7}) {
		t.Fatal("principal-only current key missing from durable inventory")
	}
	wrong := scope
	wrong.OwnerID += "-wrong"
	misbound, err := box.Seal(wrong, verifier)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(misbound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE principal_credentials SET verifier_envelope=? WHERE principal_id=?`, Args: []any{base64.StdEncoding.EncodeToString(raw), scope.OwnerID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadReferencedKeyVersions(ctx); err == nil {
		t.Fatal("principal inventory accepted wrong runtime AAD")
	}
}

// This adapter executes unchanged production SQL and all real migrations in a
// fresh SQLite database. It isolates AuthenticatePassword from unrelated test
// principals; no authentication/permission query is filtered or substituted.
// Each child is context-bounded and exits before the temporary directory closes.
type productionDomainSQLite struct{ python, path string }

func (db *productionDomainSQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.Request(ctx, rqlite.Linearizable, false, statements...)
}
func (db *productionDomainSQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.Request(ctx, rqlite.Strong, false, statements...)
}
func (*productionDomainSQLite) Backup(context.Context, io.Writer) error {
	return errors.New("domain fixture has no backup transport")
}
func (db *productionDomainSQLite) Request(ctx context.Context, _ rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	input, err := json.Marshal(struct {
		Transaction bool               `json:"transaction"`
		Statements  []rqlite.Statement `json:"statements"`
	}{transaction, statements})
	if err != nil {
		return nil, errors.New("cannot encode domain fixture request")
	}
	command := exec.CommandContext(ctx, db.python, "-c", productionDomainSQLiteProgram, db.path)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		return nil, errors.New("domain fixture SQL execution failed")
	}
	var wire struct {
		Results []rqlite.Result `json:"results"`
		Failed  bool            `json:"failed"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.UseNumber()
	if decoder.Decode(&wire) != nil || wire.Failed || len(wire.Results) != len(statements) {
		return nil, errors.New("domain fixture SQL rejected")
	}
	return wire.Results, nil
}

const productionDomainSQLiteProgram = `
import base64,json,sqlite3,sys
request=json.load(sys.stdin)
db=sqlite3.connect(sys.argv[1],isolation_level=None)
db.row_factory=sqlite3.Row
db.execute("PRAGMA foreign_keys=ON")
results=[]
def value(v):
    return base64.b64encode(v).decode("ascii") if isinstance(v,bytes) else v
try:
    if request["transaction"]: db.execute("BEGIN IMMEDIATE")
    for statement in request["statements"]:
        cursor=db.execute(statement["SQL"],statement.get("Args") or [])
        rows=[{k:value(row[k]) for k in row.keys()} for row in cursor.fetchall()] if cursor.description else []
        results.append({"Rows":rows,"RowsAffected":max(cursor.rowcount,0),"LastInsertID":cursor.lastrowid or 0})
    if request["transaction"]: db.execute("COMMIT")
    print(json.dumps({"results":results},separators=(",",":")))
except Exception:
    if db.in_transaction: db.execute("ROLLBACK")
    print('{"failed":true}')
finally:
    db.close()
`
