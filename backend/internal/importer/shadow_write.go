package importer

import (
	"errors"
	"os"
	"path/filepath"
)

func WriteShadowExport(path string, value ShadowExport) error {
	encoded, err := EncodeShadowExport(value)
	if err != nil {
		return ErrShadowExportInvalid
	}
	if path == "" {
		return ErrShadowExportUnavailable
	}
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return ErrShadowExportUnavailable
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".shadow-export-*")
	if err != nil {
		return ErrShadowExportUnavailable
	}
	temporaryPath := temporary.Name()
	published := false
	destinationCreated := false
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		if destinationCreated && !published {
			_ = os.Remove(path)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return ErrShadowExportUnavailable
	}
	if _, err := temporary.Write(encoded); err != nil {
		return ErrShadowExportUnavailable
	}
	if err := temporary.Sync(); err != nil {
		return ErrShadowExportUnavailable
	}
	if err := temporary.Close(); err != nil {
		return ErrShadowExportUnavailable
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return ErrShadowExportUnavailable
	}
	destinationCreated = true
	if err := os.Remove(temporaryPath); err != nil {
		return ErrShadowExportUnavailable
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return ErrShadowExportUnavailable
	}
	if err := directoryHandle.Sync(); err != nil {
		_ = directoryHandle.Close()
		return ErrShadowExportUnavailable
	}
	if err := directoryHandle.Close(); err != nil {
		return ErrShadowExportUnavailable
	}
	published = true
	return nil
}
