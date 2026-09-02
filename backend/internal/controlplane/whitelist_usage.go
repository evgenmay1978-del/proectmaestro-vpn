package controlplane

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

// DebitCommercialInterval implements the narrow shadow-metering callback. It
// delegates byte calculation to the immutable database interval and uses the
// service clock only for the application time and primary-access decision.
func (s *Service) DebitCommercialInterval(
	ctx context.Context,
	request whitelistmetering.CommercialDebit,
) error {
	if s == nil || s.clock == nil || ctx == nil {
		return ErrUnavailable
	}
	_, err := s.ApplyWhiteListUsage(ctx, s.clock.Now().Unix(), ApplyWhiteListUsageCommand{
		EntitlementID: request.EntitlementID, PeriodID: request.BillingPeriodID,
		MeterEpoch: request.MeterEpoch, IntervalID: request.IntervalID,
		Basis: request.Basis, IntervalEndUnix: request.IntervalEndUnix,
		SourceSHA256: request.SourceSHA256,
	})
	return err
}
