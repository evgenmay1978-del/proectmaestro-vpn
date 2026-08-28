package controlplane

import (
	"context"
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const externalActionLeaseTTLSeconds = int64(30)

func (s *Service) ExecuteExternalAction(ctx context.Context, command ExternalActionCommand, workerID string, sender ExternalActionSender) (ExternalActionResult, error) {
	if s == nil || s.store == nil || s.ids == nil || sender == nil || strings.TrimSpace(command.Type) == "" ||
		strings.TrimSpace(command.ResourceID) == "" || strings.TrimSpace(command.ActionKey) == "" || strings.TrimSpace(workerID) == "" {
		return ExternalActionResult{}, errors.New("controlplane: invalid external action")
	}
	leaseToken, err := s.ids.NewID("lease")
	if err != nil {
		return ExternalActionResult{}, errors.New("controlplane: generate external action lease")
	}
	jobName := "external-action:" + command.Type
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO cluster_job_leases(job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,lease_fence)
VALUES(?,?,?,unixepoch(),unixepoch()+?,1)
ON CONFLICT(job_name) DO UPDATE SET
 holder_id=excluded.holder_id,lease_token=excluded.lease_token,acquired_at_unix=unixepoch(),
 expires_at_unix=unixepoch()+?,lease_fence=cluster_job_leases.lease_fence+1
WHERE cluster_job_leases.expires_at_unix<=unixepoch() OR cluster_job_leases.holder_id=excluded.holder_id`, Args: []any{
			jobName, workerID, leaseToken, externalActionLeaseTTLSeconds, externalActionLeaseTTLSeconds,
		}},
		rqlite.Statement{SQL: `SELECT holder_id,lease_token,lease_fence,expires_at_unix FROM cluster_job_leases WHERE job_name=?`, Args: []any{jobName}},
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	fence, err := externalActionLeaseFence(results, workerID, leaseToken)
	if err != nil {
		return ExternalActionResult{}, err
	}
	command.WorkerID = workerID
	command.LeaseToken = leaseToken
	command.LeaseFence = fence
	persistence, err := NewRQLiteExternalActions(s)
	if err != nil {
		return ExternalActionResult{}, err
	}
	return NewExternalActionExecutor(persistence, sender).Execute(ctx, command, nil)
}

func externalActionLeaseFence(results []rqlite.Result, workerID, leaseToken string) (int64, error) {
	for i := len(results) - 1; i >= 0; i-- {
		for _, row := range results[i].Rows {
			holder, holderOK := rowString(row, "holder_id")
			token, tokenOK := rowString(row, "lease_token")
			fence, fenceOK := rowInt64(row, "lease_fence")
			expires, expiresOK := rowInt64(row, "expires_at_unix")
			if holderOK && tokenOK && fenceOK && expiresOK {
				if holder != workerID || token != leaseToken || fence <= 0 || expires <= 0 {
					return 0, ErrLeaseHeld
				}
				return fence, nil
			}
		}
	}
	return 0, ErrLeaseHeld
}
