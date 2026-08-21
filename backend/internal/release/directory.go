package release

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ValidateReleaseDirectory(root string) error {
	if strings.TrimSpace(root) == "" {
		return ErrInvalidRelease
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || !safeReleaseDirectoryMode(rootInfo.Mode()) {
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
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			!safeReleaseFileMode(entry.Name(), info.Mode()) {
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
		value, err := boundedRead(filepath.Join(root, artifact.Path), artifactSizeLimit(artifact.Path))
		if err != nil {
			return err
		}
		artifacts[artifact.Path] = value
	}
	return release.VerifyArtifacts(artifacts)
}

func boundedRead(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || limit <= 0 || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!safeReleaseFileMode(filepath.Base(path), info.Mode()) || info.Size() <= 0 || info.Size() > limit {
		return nil, ErrInvalidRelease
	}
	value, err := os.ReadFile(path)
	if err != nil || int64(len(value)) != info.Size() {
		return nil, ErrInvalidRelease
	}
	return value, nil
}

func safeReleaseDirectoryMode(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	if hasUnsafeSpecialMode(mode) {
		return false
	}
	permissions := mode.Perm()
	return permissions&0o100 != 0 && permissions&0o022 == 0
}

func safeReleaseFileMode(name string, mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	if hasUnsafeSpecialMode(mode) {
		return false
	}
	permissions := mode.Perm()
	if permissions&0o400 == 0 || permissions&0o022 != 0 {
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
