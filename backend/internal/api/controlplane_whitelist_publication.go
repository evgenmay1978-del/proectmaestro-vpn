package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type WhiteListPublicationVerdict string

const (
	WhiteListPublishable        WhiteListPublicationVerdict = "PUBLISHABLE"
	WhiteListNoEntitlement      WhiteListPublicationVerdict = "NO_ENTITLEMENT"
	WhiteListPrimaryExpired     WhiteListPublicationVerdict = "PRIMARY_EXPIRED"
	WhiteListNoBalance          WhiteListPublicationVerdict = "NO_BALANCE"
	WhiteListProjectionStale    WhiteListPublicationVerdict = "PROJECTION_STALE"
	WhiteListProjectionPending  WhiteListPublicationVerdict = "PROJECTION_PENDING"
	WhiteListReleaseMismatch    WhiteListPublicationVerdict = "RELEASE_MISMATCH"
	WhiteListSidecarUnavailable WhiteListPublicationVerdict = "SIDECAR_UNAVAILABLE"
)

type WhiteListPublicationSnapshot struct {
	Verdict           WhiteListPublicationVerdict
	Nodes             []subgen.WhiteListNode
	ProjectionVersion int64
	DesiredGeneration int64
	FreshThrough      time.Time
}
type WhiteListPublicationSource interface {
	WhiteListPublication(context.Context, string, time.Time) (WhiteListPublicationSnapshot, error)
}

func (b *ServiceBusiness) applyWhiteListPublication(ctx context.Context, token string, options subscriptionRenderOptions, ordinary SubscriptionSnapshot) (SubscriptionSnapshot, error) {
	if !options.Links || b == nil || b.cfg.WhiteListPublicationSource == nil || ordinary.ContentType != "text/plain; charset=utf-8" {
		return ordinary, nil
	}
	now := b.requestNow()
	timed, cancel := context.WithTimeout(ctx, b.cfg.WhiteListPublicationTimeout)
	defer cancel()
	publication, err := b.cfg.WhiteListPublicationSource.WhiteListPublication(timed, token, now)
	if err != nil {
		return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
	}
	switch publication.Verdict {
	case WhiteListNoEntitlement, WhiteListNoBalance:
		return ordinary, nil
	case WhiteListPublishable:
		// Continue and validate the publishable projection below.
	default:
		return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
	}
	if publication.ProjectionVersion <= 0 || publication.DesiredGeneration <= 0 || publication.FreshThrough.IsZero() || !publication.FreshThrough.After(now) || len(publication.Nodes) == 0 {
		return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
	}
	augmented, err := subgen.AppendWhiteListShareLinks(string(ordinary.Document), publication.Nodes)
	if err != nil {
		return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
	}
	ordinary.Document = []byte(augmented)
	sum := sha256.Sum256(ordinary.Document)
	ordinary.ETag = fmt.Sprintf("\"%x\"", sum)
	ordinary.ContentLength = len(ordinary.Document)
	return ordinary, nil
}
