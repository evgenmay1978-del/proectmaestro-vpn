package controlplane

// WhiteListActivationSource is the durable commercial reason that permits a
// white-list entitlement to be considered for publication. Unknown values are
// closed; a generic payment or ordinary VPN renewal is never sufficient.
type WhiteListActivationSource string

const (
	WhiteListActivationDisabled            WhiteListActivationSource = "DISABLED"
	WhiteListActivationConfirmedGBPurchase WhiteListActivationSource = "CONFIRMED_GB_PURCHASE"
	WhiteListActivationAdminEnable         WhiteListActivationSource = "ADMIN_ENABLE"
)

type WhiteListPublicationVerdict string

const (
	WhiteListPublicationPublishable        WhiteListPublicationVerdict = "PUBLISHABLE"
	WhiteListPublicationNoEntitlement      WhiteListPublicationVerdict = "NO_ENTITLEMENT"
	WhiteListPublicationPrimaryExpired     WhiteListPublicationVerdict = "PRIMARY_EXPIRED"
	WhiteListPublicationProjectionPending  WhiteListPublicationVerdict = "PROJECTION_PENDING"
	WhiteListPublicationProjectionStale    WhiteListPublicationVerdict = "PROJECTION_STALE"
	WhiteListPublicationNoBalance          WhiteListPublicationVerdict = "NO_BALANCE"
	WhiteListPublicationReleaseMismatch    WhiteListPublicationVerdict = "RELEASE_MISMATCH"
	WhiteListPublicationSidecarUnavailable WhiteListPublicationVerdict = "SIDECAR_UNAVAILABLE"
)

const whiteListObservationFreshnessSeconds int64 = 5

// WhiteListPublicationFacts is a resolved, side-effect-free input. Durable
// activation controls and sidecar receipt loading are intentionally separate
// later adapters; this evaluator cannot publish or revoke anything itself.
type WhiteListPublicationFacts struct {
	NowUnix int64

	ActivationSource        WhiteListActivationSource
	ActivationEntitlementID string

	EntitlementID    string
	EntitlementState EntitlementState

	PrimaryStatus        string
	PrimaryExpiresAtUnix int64

	ProjectionVersion   int64
	ProjectionPending   bool
	AvailableBytes      int64
	ObservedThroughUnix int64

	ReleaseBindingExact bool

	CredentialUsable       bool
	DesiredGeneration      int64
	ReceiptSetReady        bool
	ReceiptsFreshUntilUnix int64
	ApprovedNodeCount      int
}

type WhiteListPublicationDecision struct {
	Verdict           WhiteListPublicationVerdict
	ProjectionVersion int64
	DesiredGeneration int64
	FreshUntilUnix    int64
}

// EvaluateWhiteListPublication is fail-closed and deliberately leaves all
// metadata zero on a closed verdict so callers cannot accidentally publish a
// stale partial decision.
func EvaluateWhiteListPublication(facts WhiteListPublicationFacts) WhiteListPublicationDecision {
	if !whiteListActivationPermitted(facts.ActivationSource) ||
		facts.EntitlementID == "" ||
		facts.ActivationEntitlementID != facts.EntitlementID ||
		facts.EntitlementState != EntitlementActive {
		return closedWhiteListPublication(WhiteListPublicationNoEntitlement)
	}
	if facts.PrimaryStatus != "active" || facts.PrimaryExpiresAtUnix <= facts.NowUnix {
		return closedWhiteListPublication(WhiteListPublicationPrimaryExpired)
	}
	if facts.ProjectionVersion <= 0 || facts.ProjectionPending {
		return closedWhiteListPublication(WhiteListPublicationProjectionPending)
	}
	if facts.NowUnix <= 0 || facts.ObservedThroughUnix <= 0 ||
		facts.ObservedThroughUnix > facts.NowUnix ||
		facts.ObservedThroughUnix > 9223372036854775806-whiteListObservationFreshnessSeconds {
		return closedWhiteListPublication(WhiteListPublicationProjectionStale)
	}
	observationFreshUntil := facts.ObservedThroughUnix + whiteListObservationFreshnessSeconds
	if observationFreshUntil <= facts.NowUnix {
		return closedWhiteListPublication(WhiteListPublicationProjectionStale)
	}
	if facts.AvailableBytes <= 0 {
		return closedWhiteListPublication(WhiteListPublicationNoBalance)
	}
	if !facts.ReleaseBindingExact {
		return closedWhiteListPublication(WhiteListPublicationReleaseMismatch)
	}
	if !facts.CredentialUsable || facts.DesiredGeneration <= 0 ||
		!facts.ReceiptSetReady || facts.ReceiptsFreshUntilUnix <= facts.NowUnix ||
		facts.ApprovedNodeCount <= 0 {
		return closedWhiteListPublication(WhiteListPublicationSidecarUnavailable)
	}

	freshUntil := facts.PrimaryExpiresAtUnix
	if observationFreshUntil < freshUntil {
		freshUntil = observationFreshUntil
	}
	if facts.ReceiptsFreshUntilUnix < freshUntil {
		freshUntil = facts.ReceiptsFreshUntilUnix
	}
	return WhiteListPublicationDecision{
		Verdict:           WhiteListPublicationPublishable,
		ProjectionVersion: facts.ProjectionVersion,
		DesiredGeneration: facts.DesiredGeneration,
		FreshUntilUnix:    freshUntil,
	}
}

func whiteListActivationPermitted(source WhiteListActivationSource) bool {
	return source == WhiteListActivationConfirmedGBPurchase ||
		source == WhiteListActivationAdminEnable
}

func closedWhiteListPublication(verdict WhiteListPublicationVerdict) WhiteListPublicationDecision {
	return WhiteListPublicationDecision{Verdict: verdict}
}
