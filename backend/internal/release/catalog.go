package release

import (
	"bytes"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func bumpCatalogRevision(c *Catalog) error {
	if c.revision == ^uint64(0) {
		return invalid("catalog_revision_overflow")
	}
	c.revision++
	return validateCatalog(*c)
}

func validateCatalog(c Catalog) error {
	if c.revision == 0 || c.releases == nil || c.predecessors == nil {
		return invalid("catalog_state_invalid")
	}
	releaseCount := uint64(len(c.releases))
	publicationCount := uint64(len(c.predecessors))
	maxRevision := ^uint64(0)
	if releaseCount > maxRevision-1 || publicationCount > maxRevision-1-releaseCount ||
		c.revision < 1+releaseCount+publicationCount {
		return invalid("catalog_revision_invalid")
	}
	pairs := make(map[string]struct{}, len(c.releases))
	digests := make(map[string]struct{}, len(c.releases))
	type publicationProfileState struct {
		roots  int
		active int
	}
	publicationProfiles := make(map[string]publicationProfileState)
	for id, value := range c.releases {
		if err := validateCatalogRelease(id, value); err != nil {
			return err
		}
		pair := value.manifest.TransportProfileID + "\x00" + generationKey(value.manifest.Generation)
		if _, exists := pairs[pair]; exists {
			return invalid("catalog_generation_duplicate")
		}
		pairs[pair] = struct{}{}
		if _, exists := digests[value.manifestSHA256]; exists {
			return invalid("catalog_manifest_duplicate")
		}
		digests[value.manifestSHA256] = struct{}{}

		predecessorID, wasPublished := c.predecessors[id]
		switch value.state {
		case Draft, Candidate:
			if wasPublished {
				return invalid("catalog_unpublished_predecessor")
			}
		case Published, Retired:
			if !wasPublished {
				return invalid("catalog_predecessor_missing")
			}
		default:
			return invalid("catalog_state_invalid")
		}
		if wasPublished {
			profileID := value.manifest.TransportProfileID
			profileState := publicationProfiles[profileID]
			if predecessorID == "" {
				profileState.roots++
			}
			if value.state == Published {
				profileState.active++
			}
			publicationProfiles[profileID] = profileState
		}
		if predecessorID != "" {
			predecessor, exists := c.releases[predecessorID]
			if !exists || predecessorID == id || predecessor.manifest.TransportProfileID != value.manifest.TransportProfileID ||
				predecessor.manifest.Generation >= value.manifest.Generation {
				return invalid("catalog_predecessor_invalid")
			}
			if _, published := c.predecessors[predecessorID]; !published {
				return invalid("catalog_predecessor_unpublished")
			}
		}
	}
	for _, profileState := range publicationProfiles {
		if profileState.roots != 1 {
			return invalid("catalog_publication_root_invalid")
		}
		if profileState.active != 1 {
			return invalid("catalog_active_release_invalid")
		}
	}
	for id := range c.predecessors {
		if _, exists := c.releases[id]; !exists {
			return invalid("catalog_predecessor_orphan")
		}
	}
	for start := range c.predecessors {
		seen := make(map[string]struct{})
		for current := start; current != ""; current = c.predecessors[current] {
			if _, exists := seen[current]; exists {
				return invalid("catalog_predecessor_cycle")
			}
			seen[current] = struct{}{}
		}
	}
	return nil
}

func validateCatalogRelease(id string, value Release) error {
	if !validID(id) || value.manifest.ReleaseID != id || validateManifest(value.manifest) != nil ||
		value.transport.ID() != id || value.transport.TransportProfileID() != value.manifest.TransportProfileID ||
		value.transport.CompatibilityPresetID() != value.manifest.CompatibilityPresetID {
		return invalid("catalog_release_invalid")
	}
	canonical, err := marshalCanonical(value.manifest)
	if err != nil || !bytes.Equal(canonical, value.canonical) || !equalDigest(digestBytes(canonical), value.manifestSHA256) {
		return invalid("catalog_release_binding_invalid")
	}
	expected, ok := transportStateForLifecycle(value.state)
	if !ok || value.transport.State() != expected {
		return invalid("catalog_transport_state_invalid")
	}
	if value.state == Published || value.state == Retired {
		if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
			ID: value.transport.ID(), Profile: value.transport.Profile(), Preset: value.transport.Preset(),
			State: controlplane.TransportReleasePublished, ApprovedEdges: value.transport.ApprovedEdges(),
		}); err != nil {
			return invalid("catalog_publication_eligibility_invalid")
		}
	}
	return nil
}

func transportStateForLifecycle(state State) (controlplane.TransportReleaseState, bool) {
	switch state {
	case Draft:
		return controlplane.TransportReleaseDraft, true
	case Candidate:
		return controlplane.TransportReleaseCandidate, true
	case Published:
		return controlplane.TransportReleasePublished, true
	case Retired:
		return controlplane.TransportReleaseRetired, true
	default:
		return "", false
	}
}

func generationKey(value uint64) string {
	var encoded [8]byte
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = byte(value)
		value >>= 8
	}
	return string(encoded[:])
}
