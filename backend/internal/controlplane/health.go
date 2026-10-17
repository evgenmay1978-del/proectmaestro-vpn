package controlplane

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type SchemaVerifier interface {
	Verify(context.Context) error
}

type DiskSignal interface {
	Writable() bool
}

type ReadinessConfig struct {
	Store            *Store
	Schema           SchemaVerifier
	Disk             DiskSignal
	IDs              IDSource
	NodeID           string
	RequiredSettings []string
	MaxCommitAge     time.Duration
}

type Readiness struct {
	store            *Store
	schema           SchemaVerifier
	disk             DiskSignal
	ids              IDSource
	nodeID           string
	requiredSettings []string
	maxCommitAge     time.Duration
}

func NewReadiness(config ReadinessConfig) *Readiness {
	return &Readiness{
		store: config.Store, schema: config.Schema, disk: config.Disk, ids: config.IDs,
		nodeID: config.NodeID, requiredSettings: append([]string(nil), config.RequiredSettings...),
		maxCommitAge: config.MaxCommitAge,
	}
}

func (r *Readiness) Read(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := r.schema.Verify(ctx); err != nil {
		return errors.New("controlplane: schema readiness failed")
	}
	settingSQL := `SELECT COUNT(*) AS count_value,
COALESCE(MAX(updated_at_unix), 0) AS updated_at_unix FROM cluster_settings`
	settingArgs := make([]any, 0, len(r.requiredSettings))
	if len(r.requiredSettings) > 0 {
		placeholders := make([]string, len(r.requiredSettings))
		for index, setting := range r.requiredSettings {
			if strings.TrimSpace(setting) == "" {
				return errors.New("controlplane: invalid required setting")
			}
			placeholders[index] = "?"
			settingArgs = append(settingArgs, setting)
		}
		settingSQL += ` WHERE setting_key IN (` + strings.Join(placeholders, ",") + `)`
	}
	results, err := r.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT DISTINCT key_version FROM setting_secrets ORDER BY key_version`},
		rqlite.Statement{SQL: `SELECT COUNT(*) AS count_value FROM tariff_versions WHERE active = 1`},
		rqlite.Statement{SQL: settingSQL, Args: settingArgs},
	)
	if err != nil || len(results) != 3 {
		return errors.New("controlplane: linearizable readiness read failed")
	}
	versions := make([]int, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		version, ok := rowInt64(row, "key_version")
		if !ok || version <= 0 {
			return errors.New("controlplane: invalid referenced key version")
		}
		versions = append(versions, int(version))
	}
	if err := r.store.secrets.ReadyForVersions(versions...); err != nil {
		return err
	}
	tariffRow, ok := firstRow(results[1:2])
	if !ok {
		return errors.New("controlplane: tariff readiness row missing")
	}
	tariffCount, ok := rowInt64(tariffRow, "count_value")
	if !ok || tariffCount <= 0 {
		return errors.New("controlplane: tariff catalog is empty")
	}
	commitRow, ok := firstRow(results[2:3])
	if !ok {
		return errors.New("controlplane: commit readiness row missing")
	}
	settingCount, countOK := rowInt64(commitRow, "count_value")
	if len(r.requiredSettings) > 0 && (!countOK || settingCount != int64(len(r.requiredSettings))) {
		return errors.New("controlplane: required settings are absent")
	}
	updatedAt, ok := rowInt64(commitRow, "updated_at_unix")
	if !ok || updatedAt <= 0 {
		return errors.New("controlplane: required settings are absent")
	}
	if r.maxCommitAge > 0 && r.store.clock.Now().Sub(time.Unix(updatedAt, 0)) > r.maxCommitAge {
		return errors.New("controlplane: last verified commit is stale")
	}
	return nil
}

func (r *Readiness) Write(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := r.schema.Verify(ctx); err != nil {
		return errors.New("controlplane: schema readiness failed")
	}
	if !r.disk.Writable() {
		return errors.New("controlplane: local durable storage is not writable")
	}
	nonce, err := r.ids.NewID("canary")
	if err != nil {
		return errors.New("controlplane: generate readiness nonce")
	}
	nonceHMAC := r.store.secrets.LookupHMAC("health-canary", []byte(nonce))
	now := r.store.clock.Now().Unix()
	_, err = r.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `INSERT INTO health_write_canary(node_id, generation, nonce_hmac, written_at_unix, observed_at_unix)
VALUES (?, 1, ?, ?, ?) ON CONFLICT(node_id) DO UPDATE SET generation = generation + 1,
nonce_hmac = excluded.nonce_hmac, written_at_unix = excluded.written_at_unix,
observed_at_unix = excluded.observed_at_unix`,
		Args: []any{r.nodeID, nonceHMAC, now, now},
	})
	if err != nil {
		return errors.New("controlplane: write quorum unavailable")
	}
	results, err := r.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT nonce_hmac FROM health_write_canary WHERE node_id = ?`, Args: []any{r.nodeID},
	})
	if err != nil {
		return errors.New("controlplane: committed canary verification unavailable")
	}
	row, ok := firstRow(results)
	committed, valueOK := rowString(row, "nonce_hmac")
	if !ok || !valueOK || committed != nonceHMAC {
		return errors.New("controlplane: committed canary mismatch")
	}
	return nil
}

func (r *Readiness) validate() error {
	if r == nil || r.store == nil || r.schema == nil || r.disk == nil || r.ids == nil || strings.TrimSpace(r.nodeID) == "" {
		return errors.New("controlplane: incomplete readiness configuration")
	}
	return nil
}
