package controlplane_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type f10CompatibilityWBSender struct{ posts int }

func (sender *f10CompatibilityWBSender) Post(context.Context, []byte) ([]byte, error) {
	sender.posts++
	return []byte(`{"room":"new-room"}`), nil
}

func TestServiceBusinessReplaysNonCanonicalExternalActionBindingsSQLite(t *testing.T) {
	for _, generation := range []string{"raw-sha256", "raw-hmac"} {
		t.Run(generation, func(t *testing.T) {
			ctx := context.Background()
			db := newS4CanarySQLite(t)
			if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
				t.Fatalf("apply real migrations: %v", err)
			}
			box, err := controlplane.NewSecretBox(
				1,
				map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)},
				bytes.Repeat([]byte{0x52}, 32),
			)
			if err != nil {
				t.Fatalf("NewSecretBox: %v", err)
			}
			clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
			store, err := controlplane.NewStore(db, box, clock)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			service, err := controlplane.NewService(store, s4CanaryIDs{}, clock)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			const (
				rawLogin  = " Alice "
				actionKey = "legacy-noncanonical-action"
			)
			rawRequest, err := json.Marshal(map[string]string{"login": rawLogin})
			if err != nil || string(rawRequest) != `{"login":" Alice "}` {
				t.Fatalf("raw deterministic request=%q err=%v", rawRequest, err)
			}
			response := []byte(`{"room":"legacy-room"}`)
			requestEnvelope, err := box.Seal(controlplane.SecretScope{
				OwnerType: "external-action", OwnerID: actionKey, Field: "request", Kind: "wb.room",
			}, rawRequest)
			if err != nil {
				t.Fatalf("seal request: %v", err)
			}
			responseEnvelope, err := box.Seal(controlplane.SecretScope{
				OwnerType: "external-action", OwnerID: actionKey, Field: "response", Kind: "wb.room",
			}, response)
			if err != nil {
				t.Fatalf("seal response: %v", err)
			}
			requestBytes, _ := json.Marshal(requestEnvelope)
			responseBytes, _ := json.Marshal(responseEnvelope)
			resourceBinding := rawLogin
			requestDigest := sha256.Sum256(rawRequest)
			requestBinding := hex.EncodeToString(requestDigest[:])
			if generation == "raw-hmac" {
				resourceBinding = box.LookupHMAC("external-action-resource:wb.room", []byte(rawLogin))
				requestBinding = box.LookupHMAC("external-action-request:wb.room", rawRequest)
			}
			db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
response_envelope,created_at_unix,updated_at_unix)
VALUES('legacy-noncanonical-applied','wb.room',?,?,?,?,'applied',1,?,2000000,2000000)`, Args: []any{
				resourceBinding, actionKey, requestBytes, requestBinding, responseBytes,
			}})

			sender := &f10CompatibilityWBSender{}
			business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{WBRoomSender: sender, WorkerID: "panel-s2"})
			view, err := business.RequestWBRoom(ctx, api.RequestWBRoomCommand{
				Login: rawLogin, ActionKey: actionKey, IdempotencyKey: "assign-legacy-room",
			})
			if err != nil || view.ID != "legacy-noncanonical-applied" || view.State != "succeeded" || view.Room != "legacy-room" {
				t.Fatalf("ServiceBusiness replay=%#v err=%v", view, err)
			}
			if sender.posts != 0 {
				t.Fatalf("legacy applied replay sent %d provider POSTs", sender.posts)
			}
			results := db.must(t,
				rqlite.Statement{SQL: `SELECT public_value_json FROM cluster_settings WHERE setting_key='olcrtc'`},
				rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM external_actions WHERE action_type='wb.room' AND idempotency_key=?`, Args: []any{actionKey}},
			)
			if len(results[0].Rows) != 1 {
				t.Fatalf("olcrtc setting rows=%#v", results[0].Rows)
			}
			var assigned struct {
				Rooms map[string]struct {
					Room     string `json:"room"`
					Provider string `json:"provider"`
				} `json:"rooms"`
			}
			value, _ := results[0].Rows[0]["public_value_json"].(string)
			if err := json.Unmarshal([]byte(value), &assigned); err != nil {
				t.Fatalf("decode canonical assignment %q: %v", value, err)
			}
			if len(assigned.Rooms) != 1 || assigned.Rooms["alice"].Room != "legacy-room" || assigned.Rooms["alice"].Provider != "wbstream" {
				t.Fatalf("canonical assignment=%#v", assigned.Rooms)
			}
			count, ok := results[1].Rows[0]["n"]
			if !ok || count != float64(1) {
				t.Fatalf("external action count=%#v", results[1].Rows)
			}
		})
	}
}
