package agent

// Readiness may advance its immutable action generation without changing the
// runtime identity. Every other desired field, including static identities,
// remains part of the equality boundary; changed configuration still fences.
func readinessGenerationOnly(previous, desired Desired) bool {
	return previous.Generation > 0 && desired.Generation > previous.Generation &&
		previous.Version == desired.Version && previous.OriginID == desired.OriginID &&
		previous.NodeID == desired.NodeID && previous.ReleaseID == desired.ReleaseID &&
		previous.ProfileID == desired.ProfileID && previous.PresetID == desired.PresetID &&
		previous.ExitID == desired.ExitID && previous.ConfigDigest == desired.ConfigDigest &&
		previous.ManagedUserSetDigest == desired.ManagedUserSetDigest &&
		sameReadinessUsers(previous.StaticUsers, desired.StaticUsers) &&
		sameReadinessUsers(previous.ManagedUsers, desired.ManagedUsers)
}

func sameReadinessUsers(previous, desired []string) bool {
	if len(previous) != len(desired) {
		return false
	}
	for i, email := range previous {
		if email != desired[i] {
			return false
		}
	}
	return true
}

func receiptMatchesDesired(receipt Receipt, desired Desired, boot string) bool {
	return receipt.ActionKey == desired.ActionKey() && receipt.OriginID == desired.OriginID &&
		receipt.ReleaseID == desired.ReleaseID && receipt.XrayProcessBootID == boot &&
		receipt.ConfigDigest == desired.ConfigDigest && receipt.DesiredGeneration == desired.Generation &&
		receipt.ManagedUserSetDigest == desired.ManagedUserSetDigest
}
