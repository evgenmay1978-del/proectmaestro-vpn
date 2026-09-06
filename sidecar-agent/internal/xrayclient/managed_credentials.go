package xrayclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

// ManagedCredentials writes outside immutable release trees. Existing release
// credentials remain authoritative for their identities and are never replaced.
type ManagedCredentials struct {
	immutable DirectoryCredentials
	root      *os.Root
}

func NewManagedCredentials(immutable DirectoryCredentials, directory string) (*ManagedCredentials, error) {
	if directory != "/var/lib/maestro-xray-cdn-commercial-agent/credentials" || !filepath.IsAbs(immutable.Directory) {
		return nil, errors.New("xray client: invalid commercial credential directory")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("xray client: commercial credential directory unavailable")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("xray client: commercial credential directory is not protected")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("xray client: commercial credential directory unavailable")
	}
	return &ManagedCredentials{immutable: immutable, root: root}, nil
}

func (source *ManagedCredentials) Close() error {
	if source == nil || source.root == nil {
		return nil
	}
	return source.root.Close()
}

func credentialFileName(email string) (string, error) {
	if len(email) > 256 || !managedEmail.MatchString(email) || strings.ContainsAny(email, "\x00\r\n\t /\\") {
		return "", agent.ErrConflict
	}
	digest := sha256.Sum256([]byte(email))
	return hex.EncodeToString(digest[:]) + ".credential", nil
}

func (source *ManagedCredentials) immutableCredential(ctx context.Context, email, name string) (string, error) {
	_, err := os.Lstat(filepath.Join(source.immutable.Directory, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", agent.ErrLeaseUnavailable
	}
	return source.immutable.Credential(ctx, email)
}

func (source *ManagedCredentials) Credential(ctx context.Context, email string) (string, error) {
	if source == nil || source.root == nil {
		return "", agent.ErrLeaseUnavailable
	}
	name, err := credentialFileName(email)
	if err != nil {
		return "", err
	}
	if immutable, err := source.immutableCredential(ctx, email, name); !errors.Is(err, os.ErrNotExist) {
		return immutable, err
	}
	return source.read(name)
}

func (source *ManagedCredentials) read(name string) (string, error) {
	before, err := source.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() != 37 {
		return "", agent.ErrLeaseUnavailable
	}
	file, err := source.root.Open(name)
	if err != nil {
		return "", agent.ErrLeaseUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", agent.ErrLeaseUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(file, 38))
	after, statErr := source.root.Lstat(name)
	if err != nil || statErr != nil || len(raw) != 37 || raw[36] != '\n' ||
		!os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) ||
		!canonicalUUID.Match(raw[:36]) {
		return "", agent.ErrLeaseUnavailable
	}
	return string(raw[:36]), nil
}

func (source *ManagedCredentials) InstallCredential(ctx context.Context, email, clientID string) error {
	if source == nil || source.root == nil || ctx == nil || !canonicalUUID.MatchString(clientID) {
		return agent.ErrConflict
	}
	name, err := credentialFileName(email)
	if err != nil {
		return err
	}
	current, err := source.Credential(ctx, email)
	if err == nil {
		if subtle.ConstantTimeCompare([]byte(current), []byte(clientID)) == 1 {
			return nil
		}
		return agent.ErrConflict
	}
	if !errors.Is(err, os.ErrNotExist) {
		return agent.ErrLeaseUnavailable
	}
	if ctx.Err() != nil {
		return agent.ErrLeaseUnavailable
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return agent.ErrLeaseUnavailable
	}
	temporary := ".credential-" + hex.EncodeToString(suffix[:])
	file, err := source.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return agent.ErrLeaseUnavailable
	}
	defer source.root.Remove(temporary)
	_, writeErr := io.WriteString(file, clientID+"\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return agent.ErrLeaseUnavailable
	}
	// Link publishes a complete file without overwriting any concurrent winner.
	if err := source.root.Link(temporary, name); err != nil && !errors.Is(err, os.ErrExist) {
		return agent.ErrLeaseUnavailable
	}
	if err := source.root.Remove(temporary); err != nil {
		return agent.ErrLeaseUnavailable
	}
	directory, err := source.root.Open(".")
	if err != nil {
		return agent.ErrLeaseUnavailable
	}
	syncErr = directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil || closeErr != nil {
		return agent.ErrLeaseUnavailable
	}
	current, err = source.Credential(ctx, email)
	if err != nil {
		return agent.ErrLeaseUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(current), []byte(clientID)) != 1 {
		return agent.ErrConflict
	}
	return nil
}

func (client *Client) InstallCredential(ctx context.Context, email, clientID string) error {
	if client == nil || client.credentials == nil {
		return agent.ErrLeaseUnavailable
	}
	installer, ok := client.credentials.(interface {
		InstallCredential(context.Context, string, string) error
	})
	if !ok {
		return agent.ErrLeaseUnavailable
	}
	return installer.InstallCredential(ctx, email, clientID)
}
