package api

import (
 "context"
 "time"
 "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type WhiteListPublicationVerdict string
const (
 WhiteListPublishable WhiteListPublicationVerdict = "PUBLISHABLE"
 WhiteListNoEntitlement WhiteListPublicationVerdict = "NO_ENTITLEMENT"
 WhiteListPrimaryExpired WhiteListPublicationVerdict = "PRIMARY_EXPIRED"
 WhiteListNoBalance WhiteListPublicationVerdict = "NO_BALANCE"
 WhiteListProjectionStale WhiteListPublicationVerdict = "PROJECTION_STALE"
 WhiteListProjectionPending WhiteListPublicationVerdict = "PROJECTION_PENDING"
 WhiteListReleaseMismatch WhiteListPublicationVerdict = "RELEASE_MISMATCH"
 WhiteListSidecarUnavailable WhiteListPublicationVerdict = "SIDECAR_UNAVAILABLE"
)
type WhiteListPublicationSnapshot struct { Verdict WhiteListPublicationVerdict; Nodes []subgen.WhiteListNode; ProjectionVersion int64; DesiredGeneration int64; FreshThrough time.Time }
type WhiteListPublicationSource interface { WhiteListPublication(context.Context, string, time.Time) (WhiteListPublicationSnapshot, error) }

func (b *ServiceBusiness) applyWhiteListPublication(ctx context.Context, token string, options subscriptionRenderOptions, ordinary SubscriptionSnapshot) (SubscriptionSnapshot, error) {
 if !options.Links || b == nil || b.cfg.WhiteListPublicationSource == nil || ordinary.ContentType != "text/plain; charset=utf-8" { return ordinary, nil }
 publication, err := b.cfg.WhiteListPublicationSource.WhiteListPublication(ctx, token, b.requestNow())
 if err != nil || publication.Verdict != WhiteListPublishable || publication.FreshThrough.IsZero() || publication.FreshThrough.Before(b.requestNow()) || len(publication.Nodes) == 0 { return ordinary, nil }
 augmented, err := subgen.AppendWhiteListShareLinks(string(ordinary.Document), publication.Nodes)
 if err != nil { return ordinary, nil }
 ordinary.Document = []byte(augmented)
 return ordinary, nil
}
