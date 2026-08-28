package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestProvisionMintsEncryptedAccessAtomicallyAndReplaysIt(t *testing.T) {
	db := canonicalMutationDB(false)
	service, _ := testService(t, db)
	command := ProvisionCustomerCommand{Login: "Alice", Days: 30, IdempotencyKey: "mint-access-1"}

	first, err := service.ProvisionCustomer(context.Background(), command)
	if err != nil {
		t.Fatalf("ProvisionCustomer: %v", err)
	}
	assertCompleteCustomerAccess(t, first.Access)
	assertAccessMintTransaction(t, db, first.Access)
	appendAccessReplay(t, service, db, command, first)

	replayed, err := service.ProvisionCustomer(context.Background(), command)
	if err != nil {
		t.Fatalf("replay ProvisionCustomer: %v", err)
	}
	if !reflect.DeepEqual(replayed.Access, first.Access) {
		t.Fatalf("replayed access = %#v, want %#v", replayed.Access, first.Access)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("replay issued a second write transaction: %#v", db.requestCalls)
	}
}

func TestProvisionQuorumFailureCannotPartiallyMintAccess(t *testing.T) {
	db := canonicalMutationDB(false)
	db.requestFn = func([]rqlite.Statement) ([]rqlite.Result, error) {
		return nil, errors.New("quorum unavailable")
	}
	service, _ := testService(t, db)
	_, err := service.ProvisionCustomer(context.Background(), ProvisionCustomerCommand{
		Login: "Alice", Days: 30, IdempotencyKey: "mint-access-fail",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ProvisionCustomer error = %v, want ErrUnavailable", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("mint writes were not submitted as one transaction: %#v", db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, table := range []string{"customers", "subscription_tokens", "credentials", "desired_node_state", "outbox_events"} {
		if !strings.Contains(sql, table) {
			t.Fatalf("failed mint transaction does not include %s: %s", table, sql)
		}
	}
}

func assertCompleteCustomerAccess(t *testing.T, access CustomerAccess) {
	t.Helper()
	if access.SubscriptionToken == "" {
		t.Fatal("provision returned an empty subscription token")
	}
	wantProtocols := []string{"anytls", "hysteria2", "naive", "vless"}
	if len(access.Credentials) != len(wantProtocols) {
		t.Fatalf("credentials = %#v, want protocols %v", access.Credentials, wantProtocols)
	}
	for _, protocol := range wantProtocols {
		if access.Credentials[protocol] == "" {
			t.Fatalf("credential %q is empty: %#v", protocol, access.Credentials)
		}
	}
}

func assertAccessMintTransaction(t *testing.T, db *recordingRQLite, access CustomerAccess) {
	t.Helper()
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("mint calls = %#v, want one transaction", db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, table := range []string{"subscription_tokens", "credentials"} {
		if !strings.Contains(sql, table) {
			t.Fatalf("mint transaction does not touch %s: %s", table, sql)
		}
	}
	private := []string{access.SubscriptionToken}
	for _, credential := range access.Credentials {
		private = append(private, credential)
	}
	for _, call := range db.requestCalls {
		for _, statement := range call.statements {
			for _, argument := range statement.Args {
				text := ""
				switch value := argument.(type) {
				case string:
					text = value
				case []byte:
					text = string(value)
				}
				for _, secret := range private {
					if secret != "" && strings.Contains(text, secret) {
						t.Fatalf("plaintext access secret leaked into SQL argument")
					}
				}
			}
		}
	}
}

func appendAccessReplay(t *testing.T, service *Service, db *recordingRQLite, command ProvisionCustomerCommand, customer Customer) {
	t.Helper()
	requestHash, err := customerMutationHash(customerMutation{
		commandType: "customer.provision", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active", allowCreate: true, requireNew: true,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	responseJSON, err := json.Marshal(storedCustomerResponse{
		CustomerID: customer.ID, Status: customer.Status,
		ExpiresAtUnix: customer.ExpiresAtUnix, Generation: customer.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 0, len(customer.Access.Credentials))
	for protocol, raw := range customer.Access.Credentials {
		credentialEnvelope, sealErr := service.store.secrets.Seal(SecretScope{
			OwnerType: "customer", OwnerID: customer.ID, Field: "credential", Kind: protocol,
		}, []byte(raw))
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		tokenEnvelope, sealErr := service.store.secrets.Seal(SecretScope{
			OwnerType: "customer", OwnerID: customer.ID, Field: "token", Kind: "subscription",
		}, []byte(customer.Access.SubscriptionToken))
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		rows = append(rows, map[string]any{
			"customer_id": customer.ID, "token_envelope": encodedEnvelope(t, tokenEnvelope),
			"protocol": protocol, "secret_envelope": encodedEnvelope(t, credentialEnvelope),
		})
	}
	db.linear = append(db.linear,
		rowsScript(map[string]any{
			"request_hash": requestHash, "status": "applied", "response_json": string(responseJSON),
		}),
		rowsScript(rows...),
	)
}

func encodedEnvelope(t *testing.T, envelope Envelope) string {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
