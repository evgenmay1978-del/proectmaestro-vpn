package release

const maxActivationEvidenceTrustBundles = 64

type activationEvidenceTrustRegistry struct {
	activeSHA256 string
	bySHA256     map[string]EvidenceTrust
}

func newActivationEvidenceTrustRegistry(active EvidenceTrust, historical []EvidenceTrust) (activationEvidenceTrustRegistry, error) {
	if len(historical) >= maxActivationEvidenceTrustBundles {
		return activationEvidenceTrustRegistry{}, invalid("evidence_trust_registry_invalid")
	}
	activeSHA256, err := active.SHA256()
	if err != nil {
		return activationEvidenceTrustRegistry{}, err
	}
	registry := activationEvidenceTrustRegistry{
		activeSHA256: activeSHA256,
		bySHA256:     make(map[string]EvidenceTrust, 1+len(historical)),
	}
	registry.bySHA256[activeSHA256] = cloneActivationEvidenceTrust(active)
	for _, trust := range historical {
		sha256, err := trust.SHA256()
		if err != nil {
			return activationEvidenceTrustRegistry{}, err
		}
		if _, duplicate := registry.bySHA256[sha256]; duplicate {
			return activationEvidenceTrustRegistry{}, invalid("evidence_trust_registry_duplicate")
		}
		registry.bySHA256[sha256] = cloneActivationEvidenceTrust(trust)
	}
	return registry, nil
}

func (registry activationEvidenceTrustRegistry) admission(manifestSHA256 string) (EvidenceTrust, error) {
	if !equalDigest(manifestSHA256, registry.activeSHA256) {
		return EvidenceTrust{}, invalid("evidence_trust_mismatch")
	}
	return registry.resolve(manifestSHA256)
}

func (registry activationEvidenceTrustRegistry) resolve(sha256 string) (EvidenceTrust, error) {
	if !validSHA256(sha256) {
		return EvidenceTrust{}, invalid("evidence_trust_mismatch")
	}
	trust, ok := registry.bySHA256[sha256]
	if !ok {
		return EvidenceTrust{}, invalid("evidence_trust_mismatch")
	}
	return cloneActivationEvidenceTrust(trust), nil
}

func cloneActivationEvidenceTrust(trust EvidenceTrust) EvidenceTrust {
	return EvidenceTrust{
		SchemaVersion: trust.SchemaVersion,
		Keys:          cloneTrustKeys(trust.Keys),
	}
}
