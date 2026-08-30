package controlplane

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type BusinessOperationalStatus struct {
	Ready          bool
	Quorum         bool
	ReadReady      bool
	WriteReadiness string
	DataComplete   bool
	Replication    BusinessReplicationStatus
	Nodes          BusinessNodeStatus
	Apply          BusinessApplyStatus
	Outbox         BusinessOutboxStatus
	Telegram       BusinessTelegramStatus
	DNSTLS         BusinessProbeStatus
	Backup         BusinessBackupStatus
	Restore        BusinessRestoreStatus
	Failures       []BusinessFailureSummary
}

type BusinessReplicationStatus struct {
	State          string
	DataComplete   bool
	LeaderID       string
	ReachableNodes int64
	MaxLagEntries  int64
}

type BusinessNodeStatus struct {
	Voters               int64
	EnabledVoters        int64
	ActiveServiceTargets int64
	FencedServiceTargets int64
	StaleReceipts        int64
}

type BusinessApplyStatus struct {
	Pending          int64
	Failed           int64
	FailedReceipts   int64
	MaxGenerationLag int64
}

type BusinessOutboxStatus struct {
	Pending                 int64
	Failed                  int64
	OldestPendingAgeSeconds int64
}

type BusinessTelegramStatus struct {
	Routes         int64
	ActivePollers  int64
	InboxRejected  int64
	DeliveryFailed int64
	DataComplete   bool
}

type BusinessProbeStatus struct {
	State        string
	Targets      int64
	DataComplete bool
}

type BusinessBackupStatus struct {
	State              string
	DirtyGeneration    int64
	VerifiedGeneration int64
	GenerationGap      int64
}

type BusinessRestoreStatus struct {
	State        string
	Epoch        int64
	DataComplete bool
}

type BusinessFailureSummary struct {
	Component string
	Count     int64
}

func (s *Service) BusinessOperationalStatus(ctx context.Context) (BusinessOperationalStatus, error) {
	results, err := s.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `
SELECT
  (SELECT COUNT(*) FROM nodes WHERE is_voter=1) AS voters,
  (SELECT COUNT(*) FROM nodes WHERE is_voter=1 AND enabled=1) AS enabled_voters,
  (SELECT COUNT(*) FROM node_services AS ns
     JOIN nodes AS n ON n.node_id=ns.node_id
    WHERE ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0
      AND ns.retired=0 AND n.enabled=1) AS active_service_targets,
  (SELECT COUNT(*) FROM node_services AS ns
     JOIN nodes AS n ON n.node_id=ns.node_id
    WHERE ns.desired_target=1 AND ns.retired=0
      AND (ns.apply_enabled=0 OR ns.fenced=1 OR n.enabled=0)) AS fenced_service_targets,
  (SELECT COUNT(*) FROM node_apply_receipts AS r
     JOIN desired_node_state AS d
       ON d.customer_id=r.customer_id AND d.node_id=r.node_id
      AND d.service_name=r.service_name AND d.generation=r.generation
     JOIN node_services AS ns ON ns.node_id=r.node_id AND ns.service_name=r.service_name
     JOIN nodes AS n ON n.node_id=r.node_id
     JOIN cluster_restore_state AS cr ON cr.singleton_id=1
     LEFT JOIN node_leases AS l ON l.node_id=r.node_id AND l.service_name=r.service_name
    WHERE ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0
      AND ns.retired=0 AND n.enabled=1 AND (
        r.status<>'applied' OR r.desired_sha256<>d.desired_sha256
        OR r.cluster_epoch<>cr.restore_epoch OR r.node_incarnation<>n.node_incarnation
        OR (l.node_id IS NOT NULL AND (
          r.cluster_epoch<>l.cluster_epoch OR r.node_incarnation<>l.node_incarnation
          OR r.lease_fence<>l.lease_fence
        ))
      )) AS stale_receipts`},
		rqlite.Statement{SQL: `
WITH active_desired AS (
  SELECT d.customer_id,d.node_id,d.service_name,d.generation,d.status
    FROM desired_node_state AS d
    JOIN node_services AS ns ON ns.node_id=d.node_id AND ns.service_name=d.service_name
    JOIN nodes AS n ON n.node_id=d.node_id
   WHERE ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0
     AND ns.retired=0 AND n.enabled=1
),
latest_applied AS (
  SELECT customer_id,node_id,service_name,MAX(generation) AS generation
    FROM node_apply_receipts WHERE status='applied'
   GROUP BY customer_id,node_id,service_name
)
SELECT
  COALESCE(SUM(CASE WHEN d.status IN ('pending','applying') THEN 1 ELSE 0 END),0) AS pending,
  COALESCE(SUM(CASE WHEN d.status='failed' THEN 1 ELSE 0 END),0) AS failed,
  (SELECT COUNT(*) FROM node_apply_receipts AS r
     JOIN active_desired AS x
       ON x.customer_id=r.customer_id AND x.node_id=r.node_id
      AND x.service_name=r.service_name AND x.generation=r.generation
    WHERE r.status='failed') AS failed_receipts,
  COALESCE(MAX(CASE WHEN d.generation>COALESCE(a.generation,0)
    THEN d.generation-COALESCE(a.generation,0) ELSE 0 END),0) AS max_generation_lag
FROM active_desired AS d
LEFT JOIN latest_applied AS a
  ON a.customer_id=d.customer_id AND a.node_id=d.node_id AND a.service_name=d.service_name`},
		rqlite.Statement{SQL: `
SELECT
  COALESCE(SUM(CASE WHEN e.status='pending' THEN 1 ELSE 0 END),0) AS pending,
  COALESCE(SUM(CASE WHEN e.status='failed' THEN 1 ELSE 0 END),0) AS failed,
  COALESCE(MAX(CASE WHEN e.status='pending' THEN
    CASE WHEN unixepoch()>e.created_at_unix THEN unixepoch()-e.created_at_unix ELSE 0 END
    ELSE 0 END),0) AS oldest_pending_age_seconds
FROM outbox_events AS e
LEFT JOIN node_services AS ns ON ns.node_id=e.node_id AND ns.service_name=e.service_name
LEFT JOIN nodes AS n ON n.node_id=e.node_id
WHERE e.node_id IS NULL OR ns.node_id IS NULL OR (
  ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0 AND ns.retired=0 AND n.enabled=1
)`},
		rqlite.Statement{SQL: `
SELECT
  (SELECT COUNT(*) FROM telegram_bot_routes) AS routes,
  (SELECT COUNT(*) FROM telegram_pollers
    WHERE node_id IS NOT NULL AND lease_token IS NOT NULL
      AND lease_expires_at_unix>unixepoch()) AS active_pollers,
  (SELECT COUNT(*) FROM telegram_inbox WHERE status='rejected') AS inbox_rejected,
  (SELECT COUNT(*) FROM telegram_delivery_outbox WHERE status='failed') AS delivery_failed`},
		rqlite.Statement{SQL: `
SELECT
  (SELECT COUNT(*) FROM cluster_settings
    WHERE setting_key LIKE 'service_endpoint.%') AS dns_tls_targets,
  b.phase AS backup_state,b.dirty_generation,b.verified_generation,
  b.dirty_generation-b.verified_generation AS generation_gap,
  cr.restore_epoch,cr.activated,
  (SELECT COUNT(*) FROM operations WHERE status='failed') AS operations_failed,
  (SELECT COUNT(*) FROM external_actions
    WHERE status IN ('failed','unknown')) AS external_actions_failed,
  (SELECT COUNT(*) FROM backup_rpo_attempts AS a
    WHERE a.restore_epoch=cr.restore_epoch AND a.phase='failed') AS backup_failed
FROM backup_rpo_state AS b
JOIN cluster_restore_state AS cr
  ON cr.singleton_id=1 AND cr.restore_epoch=b.restore_epoch
WHERE b.singleton_id=1`},
	)
	if err != nil || len(results) != 5 {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	status := BusinessOperationalStatus{
		ReadReady:      true,
		WriteReadiness: "unknown",
		Replication:    BusinessReplicationStatus{State: "unknown"},
		DNSTLS:         BusinessProbeStatus{State: "unknown"},
		Failures:       make([]BusinessFailureSummary, 0, 6),
	}
	nodes, ok := operationalStatusRow(results, 0)
	if !ok || !readOperationalCounts(nodes, map[string]*int64{
		"voters": &status.Nodes.Voters, "enabled_voters": &status.Nodes.EnabledVoters,
		"active_service_targets": &status.Nodes.ActiveServiceTargets,
		"fenced_service_targets": &status.Nodes.FencedServiceTargets,
		"stale_receipts":         &status.Nodes.StaleReceipts,
	}) || status.Nodes.Voters == 0 || status.Nodes.EnabledVoters > status.Nodes.Voters {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	status.Quorum = status.Nodes.EnabledVoters >= status.Nodes.Voters/2+1
	apply, ok := operationalStatusRow(results, 1)
	if !ok || !readOperationalCounts(apply, map[string]*int64{
		"pending": &status.Apply.Pending, "failed": &status.Apply.Failed,
		"failed_receipts":    &status.Apply.FailedReceipts,
		"max_generation_lag": &status.Apply.MaxGenerationLag,
	}) {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	outbox, ok := operationalStatusRow(results, 2)
	if !ok || !readOperationalCounts(outbox, map[string]*int64{
		"pending": &status.Outbox.Pending, "failed": &status.Outbox.Failed,
		"oldest_pending_age_seconds": &status.Outbox.OldestPendingAgeSeconds,
	}) {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	telegram, ok := operationalStatusRow(results, 3)
	if !ok || !readOperationalCounts(telegram, map[string]*int64{
		"routes": &status.Telegram.Routes, "active_pollers": &status.Telegram.ActivePollers,
		"inbox_rejected":  &status.Telegram.InboxRejected,
		"delivery_failed": &status.Telegram.DeliveryFailed,
	}) {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	state, ok := operationalStatusRow(results, 4)
	if !ok || !readOperationalCounts(state, map[string]*int64{
		"dns_tls_targets":     &status.DNSTLS.Targets,
		"dirty_generation":    &status.Backup.DirtyGeneration,
		"verified_generation": &status.Backup.VerifiedGeneration,
		"generation_gap":      &status.Backup.GenerationGap,
	}) {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	backupState, backupStateOK := rowString(state, "backup_state")
	restoreEpoch, restoreEpochOK := operationalCount(state, "restore_epoch")
	activated, activatedOK := operationalCount(state, "activated")
	operationsFailed, operationsOK := operationalCount(state, "operations_failed")
	externalActionsFailed, externalOK := operationalCount(state, "external_actions_failed")
	backupFailed, backupOK := operationalCount(state, "backup_failed")
	if !backupStateOK || (backupState != "dirty" && backupState != "verified") ||
		!restoreEpochOK || restoreEpoch == 0 || !activatedOK || activated > 1 ||
		!operationsOK || !externalOK || !backupOK ||
		status.Backup.DirtyGeneration < status.Backup.VerifiedGeneration ||
		status.Backup.GenerationGap != status.Backup.DirtyGeneration-status.Backup.VerifiedGeneration {
		return BusinessOperationalStatus{}, ErrUnavailable
	}
	status.Backup.State = backupState
	status.Restore.Epoch = restoreEpoch
	if activated == 1 {
		status.Restore.State = "activated"
	} else {
		status.Restore.State = "inactive"
	}
	appendOperationalFailure := func(component string, count int64) {
		if count > 0 {
			status.Failures = append(status.Failures, BusinessFailureSummary{Component: component, Count: count})
		}
	}
	appendOperationalFailure("outbox", status.Outbox.Failed)
	appendOperationalFailure("apply", status.Apply.Failed+status.Apply.FailedReceipts)
	appendOperationalFailure("telegram", status.Telegram.InboxRejected+status.Telegram.DeliveryFailed)
	appendOperationalFailure("operations", operationsFailed)
	appendOperationalFailure("external_actions", externalActionsFailed)
	appendOperationalFailure("backup", backupFailed)
	status.DataComplete = false
	status.Ready = false
	return status, nil
}

func operationalStatusRow(results []rqlite.Result, index int) (map[string]any, bool) {
	if index < 0 || index >= len(results) || len(results[index].Rows) != 1 {
		return nil, false
	}
	return results[index].Rows[0], true
}

func operationalCount(row map[string]any, key string) (int64, bool) {
	value, ok := rowInt64(row, key)
	return value, ok && value >= 0
}

func readOperationalCounts(row map[string]any, fields map[string]*int64) bool {
	for key, destination := range fields {
		value, ok := operationalCount(row, key)
		if !ok {
			return false
		}
		*destination = value
	}
	return true
}
