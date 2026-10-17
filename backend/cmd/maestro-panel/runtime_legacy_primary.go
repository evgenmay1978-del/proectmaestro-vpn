package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

var errLegacyPrimaryFile = errors.New("legacy primary registry unavailable")

type runtimeLegacyPrimaryFile struct{ path string }

func newRuntimeLegacyPrimaryFile(path string) (controlplane.LegacyPrimarySource, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errLegacyPrimaryFile
	}
	return runtimeLegacyPrimaryFile{path: path}, nil
}

func (source runtimeLegacyPrimaryFile) ReadLegacyPrimaries(ctx context.Context) (controlplane.LegacyPrimarySnapshot, error) {
	if ctx == nil || ctx.Err() != nil {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	// The registry is root-owned, private, and atomically replaced by the
	// ordinary writer. Reject writable/symlink ancestors before opening it.
	for parent := filepath.Dir(source.path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm()&0o022 != 0 || !legacyPrimaryRootOwned(info) {
			return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	info, err := os.Lstat(source.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !legacyPrimaryRootOwned(info) ||
		info.Size() <= 0 || info.Size() > 64<<20 {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	file, err := os.Open(source.path)
	if err != nil {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	defer func() {
		for index := range raw {
			raw[index] = 0
		}
	}()
	if err != nil || len(raw) != int(info.Size()) {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	current := func() bool {
		latest, err := os.Lstat(source.path)
		return ctx.Err() == nil && err == nil && latest.Mode().IsRegular() &&
			latest.Mode().Perm() == 0o600 && legacyPrimaryRootOwned(latest) &&
			os.SameFile(info, latest) && latest.Size() == info.Size() && latest.ModTime().Equal(info.ModTime())
	}
	if !current() {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	customers, err := importer.DecodeLegacyCustomers(raw)
	if err != nil {
		return controlplane.LegacyPrimarySnapshot{}, errLegacyPrimaryFile
	}
	snapshot := controlplane.LegacyPrimarySnapshot{
		Customers: make([]controlplane.LegacyPrimaryCustomer, 0, len(customers)), Current: current,
	}
	for _, customer := range customers {
		snapshot.Customers = append(snapshot.Customers, controlplane.LegacyPrimaryCustomer{
			Login: customer.Login, Token: customer.SubToken, ExpiresAtUnix: customer.Expires.Unix(), Disabled: customer.Disabled,
		})
	}
	return snapshot, nil
}
