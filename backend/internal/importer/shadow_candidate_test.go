package importer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type fakeShadowCandidateSource struct {
	projection ShadowProjection
	err        error
}

func (source fakeShadowCandidateSource) ReadShadowProjection(context.Context, string) (ShadowProjection, error) {
	return source.projection, source.err
}

func projectionFromPlanForTest(t *testing.T, plan ImportPlan) ShadowProjection {
	t.Helper()
	secrets, err := shadowSecretIndex(plan.EncryptedSecrets)
	if err != nil {
		t.Fatalf("shadowSecretIndex: %v", err)
	}
	projection := ShadowProjection{
		SourceDigest: plan.SourceDigest, TargetDigest: strings.Repeat("9", 64),
		RunApplied: true, BatchCount: 2, AppliedBatchCount: 2,
	}
	for _, customer := range plan.Customers {
		projection.Customers = append(projection.Customers, ShadowProjectionCustomer{
			InternalID: customer.InternalID, LoginKeyHMAC: customer.LoginKeyHMAC,
			Status: customer.Status, ExpiresAtUnix: customer.ExpiresAtUnix,
			Generation: customer.Generation, CredentialEnabled: true, TokenRevoked: false,
			Nodes:        append([]string(nil), customer.NodeIDs...),
			ProtocolTags: append([]string(nil), customer.ProtocolTags...),
		})
	}
	for _, order := range plan.Orders {
		payment := order.PaymentState
		if payment == "created" {
			payment = "pending"
		} else if payment == "paid" {
			payment = "confirmed"
		}
		provisioning := order.ProvisioningState
		if provisioning == "paid" {
			provisioning = "applied"
		}
		projection.Orders = append(projection.Orders, ShadowProjectionOrder{
			InternalID: order.InternalID, PaymentState: payment,
			ProvisioningState: provisioning, ResultExpiresAtUnix: order.ResultExpiresAtUnix,
		})
	}
	for _, setting := range plan.Settings {
		row := ShadowProjectionSetting{Key: setting.Key, PublicValueJSON: setting.PublicValueJSON, Generation: setting.Generation}
		if setting.SecretRef != "" {
			secret := secrets[setting.SecretRef]
			row.SecretSHA256, row.SecretKeyVersion = secret.SHA256, secret.KeyVersion
		}
		projection.Settings = append(projection.Settings, row)
	}
	for _, principal := range plan.Principals {
		secret := secrets[principal.CredentialSecretRef]
		projection.Principals = append(projection.Principals, ShadowProjectionPrincipal{
			InternalID: principal.InternalID, LoginKeyHMAC: principal.LoginKeyHMAC,
			Status: principal.Status, Roles: append([]string(nil), principal.Roles...),
			VerifierSHA256: secret.SHA256, VerifierKeyVersion: secret.KeyVersion,
			CredentialActive: true,
		})
	}
	return projection
}

func validShadowProjection(t *testing.T) (ImportPlan, ShadowProjection) {
	t.Helper()
	plan := fullShadowPlan(t)
	return plan, projectionFromPlanForTest(t, plan)
}

func TestShadowFromCandidateMatchesLegacyExport(t *testing.T) {
	plan, projection := validShadowProjection(t)
	want, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatalf("ShadowFromPlan: %v", err)
	}
	got, err := ShadowFromCandidate(context.Background(), fakeShadowCandidateSource{projection: projection}, plan.SourceDigest, validShadowShapes())
	if err != nil {
		t.Fatalf("ShadowFromCandidate: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate export differs from legacy export:\n got %#v\nwant %#v", got, want)
	}
}

func TestShadowCandidateCanonicalOrderStatesPreserveLegacyExport(t *testing.T) {
	plan, projection := validShadowProjection(t)
	if len(projection.Orders) < 2 {
		t.Fatalf("projection orders=%d, want at least 2", len(projection.Orders))
	}
	wantStates := map[string]string{
		projection.Orders[0].InternalID: "pending:" + projection.Orders[0].ProvisioningState,
		projection.Orders[1].InternalID: "claimed:" + projection.Orders[1].ProvisioningState,
	}
	projection.Orders[0].PaymentState = "created"
	projection.Orders[1].PaymentState = "payment_claimed"

	got, err := ShadowFromCandidate(
		context.Background(), fakeShadowCandidateSource{projection: projection},
		plan.SourceDigest, validShadowShapes(),
	)
	if err != nil {
		t.Fatalf("ShadowFromCandidate canonical states: %v", err)
	}
	gotStates := make(map[string]string, len(got.Orders))
	for _, order := range got.Orders {
		gotStates[order.IdentityDigest] = order.State
	}
	if !reflect.DeepEqual(gotStates, wantStates) {
		t.Fatalf("canonical candidate states=%#v, want legacy export states %#v", gotStates, wantStates)
	}
}

func TestShadowCandidateRejectsDigestMismatchWithoutLeakingRows(t *testing.T) {
	plan, projection := validShadowProjection(t)
	privateMarker := strings.Repeat("a", 64)
	projection.SourceDigest = strings.Repeat("b", 64)
	projection.Customers[0].InternalID = privateMarker
	_, err := ShadowFromCandidate(context.Background(), fakeShadowCandidateSource{projection: projection}, plan.SourceDigest, validShadowShapes())
	if !errors.Is(err, ErrShadowExportInvalid) {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("error leaked input: %v", err)
	}
}

func TestShadowCandidateRejectsUnavailableSourceWithoutLeakingError(t *testing.T) {
	privateMarker := "private-rqlite-row"
	_, err := ShadowFromCandidate(context.Background(), fakeShadowCandidateSource{err: errors.New(privateMarker)}, strings.Repeat("a", 64), validShadowShapes())
	if !errors.Is(err, ErrShadowExportUnavailable) {
		t.Fatalf("source error = %v", err)
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("error leaked source detail: %v", err)
	}
}

func TestShadowCandidateRejectsIncompleteOrAmbiguousProjection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ShadowProjection)
	}{
		{"unapplied run", func(value *ShadowProjection) { value.RunApplied = false }},
		{"missing receipt", func(value *ShadowProjection) { value.AppliedBatchCount-- }},
		{"deleted customer", func(value *ShadowProjection) { value.Customers[0].Status = "deleted" }},
		{"duplicate customer", func(value *ShadowProjection) { value.Customers = append(value.Customers, value.Customers[0]) }},
		{"disabled credential", func(value *ShadowProjection) { value.Customers[0].CredentialEnabled = false }},
		{"revoked token", func(value *ShadowProjection) { value.Customers[0].TokenRevoked = true }},
		{"missing desired node", func(value *ShadowProjection) { value.Customers[0].Nodes = nil }},
		{"missing protocol tag", func(value *ShadowProjection) { value.Customers[0].ProtocolTags = nil }},
		{"duplicate order", func(value *ShadowProjection) { value.Orders = append(value.Orders, value.Orders[0]) }},
		{"malformed ota", func(value *ShadowProjection) {
			for index := range value.Settings {
				if value.Settings[index].Key == "ota" {
					value.Settings[index].PublicValueJSON = json.RawMessage(`{"versionCode":154}`)
				}
			}
		}},
		{"malformed principal", func(value *ShadowProjection) { value.Principals[0].Roles = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, projection := validShadowProjection(t)
			test.mutate(&projection)
			_, err := ShadowFromCandidate(context.Background(), fakeShadowCandidateSource{projection: projection}, plan.SourceDigest, validShadowShapes())
			if !errors.Is(err, ErrShadowExportInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRQLiteShadowProjectionUsesOneLinearizableReadOnlyBatch(t *testing.T) {
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) <= len(businessDigestQueries) {
			t.Fatalf("shadow projection statement count = %d", len(statements))
		}
		joined := strings.ToLower(statementSQL(statements))
		for _, fragment := range []string{
			"from import_runs", "from import_batches", "from customers", "from credentials",
			"from subscription_tokens", "from desired_node_state", "from desired_protocol_tags",
			"from orders", "from cluster_settings", "from setting_secrets", "from principals",
			"from principal_roles", "from principal_credentials",
		} {
			if !strings.Contains(joined, fragment) {
				t.Fatalf("shadow projection missing %q", fragment)
			}
		}
		return nil, errors.New("synthetic recorder stop")
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	_, err = store.ReadShadowProjection(context.Background(), strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("recorder error was ignored")
	}
	if db.queryCalls != 1 || len(db.queries) != 1 {
		t.Fatalf("linearizable query calls = %d / %#v", db.queryCalls, db.queries)
	}
	if len(db.requests) != 0 {
		t.Fatalf("shadow projection issued writes: %#v", db.requests)
	}
}

func statementSQL(statements []rqlite.Statement) string {
	var builder strings.Builder
	for _, statement := range statements {
		builder.WriteString(statement.SQL)
		builder.WriteByte('\n')
	}
	return builder.String()
}
