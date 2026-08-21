package release

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func ValidateReleaseDirectory(_ string) error {
	return invalid("evidence_trust_required")
}

func ValidateReleaseDirectoryWithTrust(root string, trust EvidenceTrust) error {
	return validateReleaseDirectoryWithTrustAt(root, trust, nil)
}

func ValidateReleaseDirectoryForPromotionWithTrust(root string, trust EvidenceTrust, now time.Time) error {
	if now.IsZero() {
		return invalid("promotion_time_invalid")
	}
	now = now.UTC()
	return validateReleaseDirectoryWithTrustAt(root, trust, &now)
}

func validateReleaseDirectoryWithTrustAt(root string, trust EvidenceTrust, admissionTime *time.Time) error {
	if err := trust.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return invalid("release_root_empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil || hasSymlinkAncestor(absRoot) {
		return invalid("release_root_symlink")
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || !safeReleaseDirectoryMode(rootInfo.Mode()) {
		return invalid("release_root_unsealed")
	}
	entries, err := os.ReadDir(absRoot)
	if err != nil || len(entries) != len(allowedArtifactPaths())+1 {
		return invalid("release_entry_set_invalid")
	}
	expected := map[string]struct{}{"manifest.json": {}}
	for _, name := range allowedArtifactPaths() {
		expected[name] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok || entry.Type()&os.ModeSymlink != 0 {
			return invalid("release_entry_invalid")
		}
		info, err := os.Lstat(filepath.Join(absRoot, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			!safeReleaseFileMode(entry.Name(), info.Mode()) || !singleLink(info) {
			return invalid("release_artifact_unsealed")
		}
	}
	manifestBytes, err := boundedRead(filepath.Join(absRoot, "manifest.json"), maxManifestBytes)
	if err != nil {
		return err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return err
	}
	release := Release{manifest: manifest}
	artifacts := make(map[string][]byte, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		value, err := boundedRead(filepath.Join(absRoot, artifact.Path), artifactSizeLimit(artifact.Path))
		if err != nil {
			return err
		}
		artifacts[artifact.Path] = value
	}
	return release.verifyArtifactsWithTrustAt(artifacts, trust, admissionTime)
}

// PromoteSealedDirectory atomically moves a validated, immutable staging
// directory into its final sibling path. The caller owns fsync of the parent
// when crash durability beyond the journal contract is required.
func PromoteSealedDirectory(_, _ string) error {
	return invalid("evidence_trust_required")
}

func PromoteSealedDirectoryWithTrust(staging, published string, trust EvidenceTrust) error {
	stagingAbs, err := filepath.Abs(staging)
	if err != nil {
		return invalid("promotion_path_invalid")
	}
	publishedAbs, err := filepath.Abs(published)
	if err != nil || filepath.Dir(stagingAbs) != filepath.Dir(publishedAbs) || stagingAbs == publishedAbs {
		return invalid("promotion_path_invalid")
	}
	if _, err := os.Lstat(publishedAbs); !os.IsNotExist(err) {
		return invalid("promotion_destination_exists")
	}
	if err := ValidateReleaseDirectoryForPromotionWithTrust(stagingAbs, trust, time.Now().UTC()); err != nil {
		return err
	}
	before, err := os.Lstat(stagingAbs)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return invalid("promotion_source_changed")
	}
	if err := os.Rename(stagingAbs, publishedAbs); err != nil {
		return invalid("promotion_rename_failed")
	}
	after, err := os.Lstat(publishedAbs)
	if err != nil || !os.SameFile(before, after) {
		return invalid("promotion_inode_changed")
	}
	return nil
}

func boundedRead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid("artifact_open_failed")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || limit <= 0 || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		!safeReleaseFileMode(filepath.Base(path), before.Mode()) || !singleLink(before) || before.Size() <= 0 || before.Size() > limit {
		return nil, invalid("artifact_stat_invalid")
	}
	value, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(value)) != before.Size() || int64(len(value)) > limit {
		return nil, invalid("artifact_read_invalid")
	}
	afterHandle, err := file.Stat()
	afterPath, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(before, afterHandle) || !os.SameFile(before, afterPath) ||
		afterPath.Mode()&os.ModeSymlink != 0 || !singleLink(afterPath) {
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
	if runtime.GOOS == "windows" {
		return true
	}
	if hasUnsafeSpecialMode(mode) {
		return false
	}
	permissions := mode.Perm()
	return permissions&0o100 != 0 && permissions&0o222 == 0
}

func safeReleaseFileMode(name string, mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
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
