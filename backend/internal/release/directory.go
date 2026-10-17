package release

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type sealedReleaseIdentity struct {
	Manifest       Manifest
	ManifestSHA256 string
}

type filesystemIdentity struct {
	device uint64
	inode  uint64
}

func ValidateReleaseDirectory(_ string) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	return invalid("evidence_trust_required")
}

func ValidateReleaseDirectoryWithTrust(root string, trust EvidenceTrust) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	trustedOwnerUID, err := currentTrustedOwnerUID()
	if err != nil {
		return err
	}
	_, err = inspectSealedReleaseWithTrustAt(root, trust, nil, trustedOwnerUID)
	return err
}

func ValidateReleaseDirectoryForPromotionWithTrust(root string, trust EvidenceTrust, now time.Time) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	if now.IsZero() {
		return invalid("promotion_time_invalid")
	}
	trustedOwnerUID, err := currentTrustedOwnerUID()
	if err != nil {
		return err
	}
	now = now.UTC()
	_, err = inspectSealedReleaseWithTrustAt(root, trust, &now, trustedOwnerUID)
	return err
}

func validateReleaseDirectoryWithTrustAt(root string, trust EvidenceTrust, admissionTime *time.Time) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	trustedOwnerUID, err := currentTrustedOwnerUID()
	if err != nil {
		return err
	}
	_, err = inspectSealedReleaseWithTrustAt(root, trust, admissionTime, trustedOwnerUID)
	return err
}

func inspectSealedReleaseWithTrustAt(root string, trust EvidenceTrust, admissionTime *time.Time, trustedOwnerUID uint32) (sealedReleaseIdentity, error) {
	if err := requireSealedPlatform(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if err := trust.validate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if strings.TrimSpace(root) == "" {
		return sealedReleaseIdentity{}, invalid("release_root_empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil || hasSymlinkAncestor(absRoot) {
		return sealedReleaseIdentity{}, invalid("release_root_symlink")
	}
	beforeIdentity, err := captureTrustedReleaseDirectory(absRoot, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 ||
		!safeReleaseDirectoryMode(rootInfo.Mode()) || !platformFileOwnedBy(rootInfo, trustedOwnerUID) {
		return sealedReleaseIdentity{}, invalid("release_root_unsealed")
	}
	entries, err := os.ReadDir(absRoot)
	if err != nil || len(entries) != len(allowedArtifactPaths())+1 {
		return sealedReleaseIdentity{}, invalid("release_entry_set_invalid")
	}
	expected := map[string]struct{}{"manifest.json": {}}
	for _, name := range allowedArtifactPaths() {
		expected[name] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok || entry.Type()&os.ModeSymlink != 0 {
			return sealedReleaseIdentity{}, invalid("release_entry_invalid")
		}
		info, err := os.Lstat(filepath.Join(absRoot, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			!safeReleaseFileMode(entry.Name(), info.Mode()) || !singleLink(info) ||
			!platformFileOwnedBy(info, trustedOwnerUID) {
			return sealedReleaseIdentity{}, invalid("release_artifact_unsealed")
		}
	}
	manifestBytes, err := boundedRead(filepath.Join(absRoot, "manifest.json"), maxManifestBytes, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	value := Release{manifest: manifest}
	artifacts := make(map[string][]byte, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		data, err := boundedRead(filepath.Join(absRoot, artifact.Path), artifactSizeLimit(artifact.Path), trustedOwnerUID)
		if err != nil {
			return sealedReleaseIdentity{}, err
		}
		artifacts[artifact.Path] = data
	}
	if err := value.verifyArtifactsWithTrustAt(artifacts, trust, admissionTime); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if hasSymlinkAncestor(absRoot) {
		return sealedReleaseIdentity{}, invalid("release_root_changed")
	}
	afterIdentity, err := captureTrustedReleaseDirectory(absRoot, trustedOwnerUID)
	if err != nil || afterIdentity != beforeIdentity {
		return sealedReleaseIdentity{}, invalid("release_root_changed")
	}
	return sealedReleaseIdentity{Manifest: cloneManifest(manifest), ManifestSHA256: digestBytes(manifestBytes)}, nil
}

func PromoteSealedDirectory(_, _ string) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	return invalid("evidence_trust_required")
}

func PromoteSealedDirectoryWithTrust(staging, published string, trust EvidenceTrust) error {
	if err := requireSealedPlatform(); err != nil {
		return err
	}
	trustedOwnerUID, err := currentTrustedOwnerUID()
	if err != nil {
		return err
	}
	_, err = promoteSealedDirectoryWithTrustAt(staging, published, trust, time.Now().UTC(), trustedOwnerUID, nil)
	return err
}

func promoteSealedDirectoryWithTrustAt(staging, published string, trust EvidenceTrust, admissionTime time.Time, trustedOwnerUID uint32, beforeRename func() error) (sealedReleaseIdentity, error) {
	if err := requireSealedPlatform(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if admissionTime.IsZero() {
		return sealedReleaseIdentity{}, invalid("promotion_time_invalid")
	}
	stagingAbs, err := filepath.Abs(staging)
	if err != nil {
		return sealedReleaseIdentity{}, invalid("promotion_path_invalid")
	}
	publishedAbs, err := filepath.Abs(published)
	if err != nil || stagingAbs == publishedAbs || filepath.Dir(stagingAbs) != filepath.Dir(publishedAbs) ||
		filepath.Base(stagingAbs) == "." || filepath.Base(publishedAbs) == "." {
		return sealedReleaseIdentity{}, invalid("promotion_path_invalid")
	}
	parent := filepath.Dir(stagingAbs)
	if hasSymlinkAncestor(parent) {
		return sealedReleaseIdentity{}, invalid("promotion_parent_untrusted")
	}
	parentIdentity, err := captureTrustedParentDirectory(parent, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	stagingIdentity, err := captureTrustedReleaseDirectory(stagingAbs, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, invalid("promotion_source_changed")
	}
	admissionTime = admissionTime.UTC()
	inspected, err := inspectSealedReleaseWithTrustAt(stagingAbs, trust, &admissionTime, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	if err := recheckPromotionIdentities(parent, stagingAbs, parentIdentity, stagingIdentity, trustedOwnerUID); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return sealedReleaseIdentity{}, err
		}
	}
	if hasSymlinkAncestor(parent) || hasSymlinkAncestor(stagingAbs) {
		return sealedReleaseIdentity{}, invalid("promotion_source_changed")
	}
	if err := recheckPromotionIdentities(parent, stagingAbs, parentIdentity, stagingIdentity, trustedOwnerUID); err != nil {
		return sealedReleaseIdentity{}, err
	}
	promotedIdentity, err := platformRenameNoReplace(stagingAbs, publishedAbs, parentIdentity, stagingIdentity, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	if promotedIdentity != stagingIdentity || hasSymlinkAncestor(publishedAbs) {
		return sealedReleaseIdentity{}, invalid("promotion_inode_changed")
	}
	promoted, err := inspectSealedReleaseWithTrustAt(publishedAbs, trust, &admissionTime, trustedOwnerUID)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	if promoted.Manifest.ReleaseID != inspected.Manifest.ReleaseID || !equalDigest(promoted.ManifestSHA256, inspected.ManifestSHA256) {
		return sealedReleaseIdentity{}, invalid("promotion_release_changed")
	}
	if err := platformSyncSealedRelease(publishedAbs, append([]string{"manifest.json"}, allowedArtifactPaths()...), parentIdentity, trustedOwnerUID); err != nil {
		return sealedReleaseIdentity{}, err
	}
	afterIdentity, err := captureTrustedReleaseDirectory(publishedAbs, trustedOwnerUID)
	if err != nil || afterIdentity != stagingIdentity {
		return sealedReleaseIdentity{}, invalid("promotion_inode_changed")
	}
	afterParent, err := captureTrustedParentDirectory(parent, trustedOwnerUID)
	if err != nil || afterParent != parentIdentity {
		return sealedReleaseIdentity{}, invalid("promotion_parent_changed")
	}
	return promoted, nil
}

func recheckPromotionIdentities(parent, staging string, expectedParent, expectedStaging filesystemIdentity, trustedOwnerUID uint32) error {
	currentParent, err := captureTrustedParentDirectory(parent, trustedOwnerUID)
	if err != nil || currentParent != expectedParent {
		return invalid("promotion_parent_changed")
	}
	currentStaging, err := captureTrustedReleaseDirectory(staging, trustedOwnerUID)
	if err != nil || currentStaging != expectedStaging {
		return invalid("promotion_source_changed")
	}
	return nil
}

func boundedRead(path string, limit int64, trustedOwnerUID uint32) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid("artifact_open_failed")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || limit <= 0 || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		!safeReleaseFileMode(filepath.Base(path), before.Mode()) || !singleLink(before) ||
		!platformFileOwnedBy(before, trustedOwnerUID) || before.Size() <= 0 || before.Size() > limit {
		return nil, invalid("artifact_stat_invalid")
	}
	value, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(value)) != before.Size() || int64(len(value)) > limit {
		return nil, invalid("artifact_read_invalid")
	}
	afterHandle, err := file.Stat()
	afterPath, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(before, afterHandle) || !os.SameFile(before, afterPath) ||
		afterPath.Mode()&os.ModeSymlink != 0 || !singleLink(afterPath) ||
		!platformFileOwnedBy(afterHandle, trustedOwnerUID) || !platformFileOwnedBy(afterPath, trustedOwnerUID) {
		return nil, invalid("artifact_inode_changed")
	}
	return value, nil
}

func hasSymlinkAncestor(path string) bool {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func safeReleaseDirectoryMode(mode os.FileMode) bool {
	if hasUnsafeSpecialMode(mode) {
		return false
	}
	permissions := mode.Perm()
	return permissions&0o100 != 0 && permissions&0o222 == 0
}

func safeReleaseFileMode(name string, mode os.FileMode) bool {
	if hasUnsafeSpecialMode(mode) {
		return false
	}
	permissions := mode.Perm()
	if permissions&0o400 == 0 || permissions&0o222 != 0 {
		return false
	}
	if name == "xray" {
		return permissions&0o100 != 0
	}
	return permissions&0o111 == 0
}

func hasUnsafeSpecialMode(mode os.FileMode) bool {
	return mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0
}
