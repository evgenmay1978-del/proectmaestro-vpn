package controlplane

import (
	"strings"
	"time"
)

const LegacyXUIAbsenceKind = "legacy-xui-absence-v1"
const LegacyXUIAbsenceOwner = "legacy_node_binding"
const LegacyXUIAbsenceMarker = "legacy_xui_observed_absent"
const LegacyXUIPayloadKind = "xui-user-v1"

// LegacyXUIAbsenceEvidence is an operator-produced observation of the live
// database, not an interpretation of a failed/negative HTTP lookup. The capture
// authenticates these facts together with the exact customer/node/UUID tuple.
type LegacyXUIAbsenceEvidence struct {
	SchemaVersion          int       `json:"schema_version"`
	Method                 string    `json:"method"`
	ObservedAt             time.Time `json:"observed_at"`
	CustomersSHA256        string    `json:"customers_sha256"`
	DatabaseSnapshotSHA256 string    `json:"database_snapshot_sha256"`
	DatabaseDevice         uint64    `json:"database_device"`
	DatabaseInode          uint64    `json:"database_inode"`
	XUIPID                 int64     `json:"xui_pid"`
	XUIStartTicks          uint64    `json:"xui_start_ticks"`
	LiveDatabaseFDMatched  bool      `json:"live_database_fd_matched"`
	InboundsLoginAbsent    bool      `json:"inbounds_login_absent"`
	InboundsUUIDAbsent     bool      `json:"inbounds_uuid_absent"`
	ClientsLoginAbsent     bool      `json:"clients_login_absent"`
	ClientsUUIDAbsent      bool      `json:"clients_uuid_absent"`
}

func (e LegacyXUIAbsenceEvidence) Valid() bool {
	return e.SchemaVersion == 1 && e.Method == "xui-sqlite-live-fd-wal-snapshot-v1" &&
		!e.ObservedAt.IsZero() && e.ObservedAt.Unix() > 0 && legacyBindingSHA(e.CustomersSHA256) &&
		legacyBindingSHA(e.DatabaseSnapshotSHA256) && e.DatabaseDevice > 0 && e.DatabaseInode > 0 &&
		e.XUIPID > 1 && e.XUIStartTicks > 0 && e.LiveDatabaseFDMatched && e.InboundsLoginAbsent &&
		e.InboundsUUIDAbsent && e.ClientsLoginAbsent && e.ClientsUUIDAbsent
}

// LegacyXUIAbsentBinding occupies one immutable imported_secrets slot. A later
// capture cannot silently replace it with a present binding or mint a SubID.
type LegacyXUIAbsentBinding struct {
	SchemaVersion     int                      `json:"schema_version"`
	CustomerID        string                   `json:"customer_id"`
	CustomerSourceKey string                   `json:"customer_source_key"`
	NodeID            string                   `json:"node_id"`
	Server            string                   `json:"server"`
	LoginKeyHMAC      string                   `json:"login_key_hmac"`
	UUIDHMAC          string                   `json:"uuid_hmac"`
	Observation       LegacyXUIAbsenceEvidence `json:"observation"`
}

func LegacyXUIAbsenceID(customerID, nodeID string) string {
	return LegacyXUIAbsenceKind + ":" + customerID + ":" + nodeID
}

func LegacyXUIAbsenceOwnerKey(customerID, nodeID string) string {
	return customerID + ":" + nodeID + ":xui"
}

func (b LegacyXUIAbsentBinding) Valid() bool {
	return b.SchemaVersion == 1 && legacyBindingSHA(b.CustomerID) && b.CustomerSourceKey != "" &&
		(b.NodeID == "S1" || b.NodeID == "S3" || b.NodeID == "S4") && b.Server != "" &&
		len(b.Server) <= 4096 && !strings.ContainsRune(b.Server, 0) && legacyBindingSHA(b.LoginKeyHMAC) &&
		legacyBindingSHA(b.UUIDHMAC) && b.Observation.Valid()
}

func legacyBindingSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
