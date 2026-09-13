package controlplane

import (
	"context"
	"errors"
	"sort"
)

// WhiteListMeteringAdmissionCandidate identifies a potential admission only.
// Discovery exposes no credential material and grants no runtime permission.
type WhiteListMeteringAdmissionCandidate struct {
	EntitlementID string
	ExitID        string
}

// WhiteListMeteringAdmissionCandidates does not depend on already-managed
// plan.Routes: a paid customer's first admission must precede its desired user.
// The caller still needs a verified reserve and AuthorizeWhiteListMeteringAdmission.
func (s *Service) WhiteListMeteringAdmissionCandidates(ctx context.Context) ([]WhiteListMeteringAdmissionCandidate, error) {
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]WhiteListMeteringAdmissionCandidate, 0)
	for entitlementID := range state.publications {
		baseReady := false
		for exitID := range state.credentials[entitlementID] {
			if !state.exits[exitID].Healthy {
				continue
			}
			// Period and balance are shared by this account's four exits.
			// Discovery grants nothing; each admission rechecks its paid base.
			if !baseReady {
				if _, _, _, err := s.whiteListAdmissionBaseFromState(ctx, entitlementID, exitID, state); err != nil {
					if contextErr := ctx.Err(); contextErr != nil {
						return nil, contextErr
					}
					if !errors.Is(err, ErrUnavailable) {
						return nil, err
					}
					break
				}
				baseReady = true
			}
			candidates = append(candidates, WhiteListMeteringAdmissionCandidate{
				EntitlementID: entitlementID, ExitID: exitID,
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].EntitlementID == candidates[j].EntitlementID {
			return candidates[i].ExitID < candidates[j].ExitID
		}
		return candidates[i].EntitlementID < candidates[j].EntitlementID
	})
	return candidates, nil
}
