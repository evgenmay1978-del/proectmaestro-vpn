package release

import (
	"os"
	"path/filepath"
	"strings"
)

func ValidateReleaseDirectory(root string) error {
	if strings.TrimSpace(root) == "" {
		return ErrInvalidRelease
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidRelease
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != len(allowedArtifactPaths())+1 {
		return ErrInvalidRelease
	}
	expected := map[string]struct{}{"manifest.json": {}}
	for _, name := range allowedArtifactPaths() {
		expected[name] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return ErrInvalidRelease
		}
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidRelease
		}
	}
	manifestBytes, err := boundedRead(filepath.Join(root, "manifest.json"), maxManifestBytes)
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
		limit := int64(1 << 20)
		if artifact.Path == "xray" {
			limit = maxBinaryBytes
		}
		value, err := boundedRead(filepath.Join(root, artifact.Path), limit)
		if err != nil {
			return err
		}
		artifacts[artifact.Path] = value
	}
	return release.VerifyArtifacts(artifacts)
}

func boundedRead(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, ErrInvalidRelease
	}
	value, err := os.ReadFile(path)
	if err != nil || int64(len(value)) != info.Size() {
		return nil, ErrInvalidRelease
	}
	return value, nil
}
