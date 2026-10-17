package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const currentDesiredFile = "current-desired.json"

type FileStore struct {
	directory string
	retention int
	mutex     sync.Mutex
}

func NewFileStore(directory string, retention int) (*FileStore, error) {
	if directory == "" || !filepath.IsAbs(directory) || retention < 1 || retention > 1024 {
		return nil, errors.New("sidecar agent: invalid state store configuration")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, errors.New("sidecar agent: create state directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, errors.New("sidecar agent: protect state directory")
	}
	return &FileStore{directory: directory, retention: retention}, nil
}

func (store *FileStore) SaveDesired(desired Desired) error {
	if store == nil || desired.ActionKey() == "" || len(desired.canonicalJSON) == 0 {
		return ErrInvalidDesired
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.atomicWrite(currentDesiredFile, desired.canonicalJSON)
}

func (store *FileStore) LoadDesired() (Desired, error) {
	if store == nil {
		return Desired{}, ErrNotFound
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	raw, err := readBounded(filepath.Join(store.directory, currentDesiredFile), MaxDesiredBytes)
	if err != nil {
		return Desired{}, err
	}
	return ParseDesired(raw)
}

func (store *FileStore) SaveReceipt(receipt Receipt) error {
	if store == nil || !receipt.ReadyAt(receipt.AppliedAt) {
		return errors.New("sidecar agent: invalid receipt")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return errors.New("sidecar agent: encode receipt")
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if err := store.atomicWrite(receiptFileName(receipt.ActionKey), raw); err != nil {
		return err
	}
	return store.pruneReceipts()
}

func (store *FileStore) LoadReceipt(actionKey string) (Receipt, error) {
	if store == nil || actionKey == "" {
		return Receipt{}, ErrNotFound
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.loadReceiptFile(receiptFileName(actionKey))
}

func (store *FileStore) InvalidateReceiptsExceptBoot(bootID string) error {
	if store == nil || bootID == "" {
		return errors.New("sidecar agent: invalid process boot identity")
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return errors.New("sidecar agent: read receipt directory")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "receipt-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		receipt, loadErr := store.loadReceiptFile(entry.Name())
		if loadErr == nil && receipt.XrayProcessBootID == bootID {
			continue
		}
		if err := os.Remove(filepath.Join(store.directory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("sidecar agent: invalidate stale receipt")
		}
	}
	return syncDirectory(store.directory)
}

func (store *FileStore) loadReceiptFile(name string) (Receipt, error) {
	raw, err := readBounded(filepath.Join(store.directory, name), 64<<10)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, errors.New("sidecar agent: invalid receipt file")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Receipt{}, errors.New("sidecar agent: invalid receipt file")
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, raw) || !receipt.ReadyAt(receipt.AppliedAt) {
		return Receipt{}, errors.New("sidecar agent: invalid receipt file")
	}
	return receipt, nil
}

func (store *FileStore) atomicWrite(name string, raw []byte) error {
	temporary, err := os.CreateTemp(store.directory, ".tmp-")
	if err != nil {
		return errors.New("sidecar agent: create temporary state")
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return errors.New("sidecar agent: protect temporary state")
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return errors.New("sidecar agent: write temporary state")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return errors.New("sidecar agent: sync temporary state")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("sidecar agent: close temporary state")
	}
	destination := filepath.Join(store.directory, name)
	if err := os.Rename(temporaryName, destination); err != nil {
		return errors.New("sidecar agent: atomically replace state")
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return errors.New("sidecar agent: protect durable state")
	}
	return syncDirectory(store.directory)
}

func (store *FileStore) pruneReceipts() error {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return errors.New("sidecar agent: read receipt directory")
	}
	type candidate struct {
		name    string
		modTime int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "receipt-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return errors.New("sidecar agent: inspect receipt")
		}
		candidates = append(candidates, candidate{name: entry.Name(), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime == candidates[j].modTime {
			return candidates[i].name > candidates[j].name
		}
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) <= store.retention {
		return nil
	}
	for _, stale := range candidates[store.retention:] {
		if err := os.Remove(filepath.Join(store.directory, stale.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("sidecar agent: prune receipt")
		}
	}
	return syncDirectory(store.directory)
}

func receiptFileName(actionKey string) string {
	digest := sha256.Sum256([]byte(actionKey))
	return "receipt-" + hex.EncodeToString(digest[:]) + ".json"
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("sidecar agent: open durable state")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("sidecar agent: read durable state")
	}
	return raw, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("sidecar agent: open state directory")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return errors.New("sidecar agent: sync state directory")
	}
	return nil
}
