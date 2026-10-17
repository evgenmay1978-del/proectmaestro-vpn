package controlplane

// WhiteListPublicationAction is the entitlement-scoped sidecar direction
// emitted only when the public CDN usability state changes. Task 11 assigns a
// durable desired generation and applies the corresponding managed wl: users.
type WhiteListPublicationAction string

const (
	WhiteListPublicationEnable WhiteListPublicationAction = "ENABLE"
	WhiteListPublicationRevoke WhiteListPublicationAction = "REVOKE"
)

// WhiteListPublicationIntent contains no customer, credential, node, or
// ordinary-access material. A later durable adapter may use only the stable
// entitlement identity to change managed CDN users.
type WhiteListPublicationIntent struct {
	EntitlementID string
	Action        WhiteListPublicationAction
}

// DeriveWhiteListPublicationIntent converts a publication verdict into one
// edge-triggered desired-state intent. Replaying the same usable or unusable
// state is a no-op. A malformed verdict is closed so an already published
// entitlement is revoked instead of being left enabled.
func DeriveWhiteListPublicationIntent(
	entitlementID string,
	previouslyPublishable bool,
	decision WhiteListPublicationDecision,
) (WhiteListPublicationIntent, bool, error) {
	if !validWhiteListID(entitlementID) {
		return WhiteListPublicationIntent{}, false, ErrConflict
	}
	publishable := validWhiteListPublicationDecision(decision) &&
		decision.Verdict == WhiteListPublicationPublishable
	if publishable == previouslyPublishable {
		return WhiteListPublicationIntent{}, false, nil
	}
	action := WhiteListPublicationRevoke
	if publishable {
		action = WhiteListPublicationEnable
	}
	return WhiteListPublicationIntent{EntitlementID: entitlementID, Action: action}, true, nil
}

func validWhiteListPublicationDecision(decision WhiteListPublicationDecision) bool {
	if decision.Verdict == WhiteListPublicationPublishable {
		return decision.ProjectionVersion > 0 && decision.DesiredGeneration > 0 &&
			decision.FreshUntilUnix > 0
	}
	switch decision.Verdict {
	case WhiteListPublicationNoEntitlement,
		WhiteListPublicationPrimaryExpired,
		WhiteListPublicationProjectionPending,
		WhiteListPublicationProjectionStale,
		WhiteListPublicationNoBalance,
		WhiteListPublicationReleaseMismatch,
		WhiteListPublicationSidecarUnavailable:
		return decision.ProjectionVersion == 0 && decision.DesiredGeneration == 0 &&
			decision.FreshUntilUnix == 0
	default:
		return false
	}
}
