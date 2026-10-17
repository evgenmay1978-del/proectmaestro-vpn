package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

const maxReceiptFileSize = int64(1 << 20)

var errReceiptFile = errors.New("invalid import receipt file")

func writeReceiptAtomic(path string, receipt importer.ImportReceipt) error {
	if strings.TrimSpace(path) == "" || filepath.Base(path) == "." {
		return errReceiptFile
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || len(encoded) == 0 || int64(len(encoded)) > maxReceiptFileSize {
		return errReceiptFile
	}
	if exact, err := existingReceiptExact(path, encoded); err != nil {
		return errReceiptFile
	} else if exact {
		return nil
	}

	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".maestro-import-receipt-*")
	if err != nil {
		return errReceiptFile
	}
	tempPath := temp.Name()
	renamed := false
	defer func() {
		_ = temp.Close()
		if !renamed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return errReceiptFile
	}
	if _, err := temp.Write(encoded); err != nil {
		return errReceiptFile
	}
	if err := temp.Sync(); err != nil {
		return errReceiptFile
	}
	if err := temp.Close(); err != nil {
		return errReceiptFile
	}

	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return errReceiptFile
	}
	if err := renameReceiptNoReplace(tempPath, path); err != nil {
		return errReceiptFile
	}
	renamed = true

	parent, err := os.Open(directory)
	if err != nil {
		return errReceiptFile
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil || closeErr != nil {
		return errReceiptFile
	}
	return nil
}

func existingReceiptExact(path string, expected []byte) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() < 0 || info.Size() > maxReceiptFileSize {
		return false, errReceiptFile
	}
	current, err := readBounded(path, maxReceiptFileSize)
	if err != nil {
		return false, errReceiptFile
	}
	return bytes.Equal(current, expected), func() error {
		if bytes.Equal(current, expected) {
			return nil
		}
		return errReceiptFile
	}()
}
