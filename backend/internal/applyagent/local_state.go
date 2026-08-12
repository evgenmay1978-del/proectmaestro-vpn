package applyagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FileStateStore struct { path string }

func NewFileStateStore(path string) (*FileStateStore, error) {
	path = strings.TrimSpace(path)
	if path == "" { return nil, errors.New("applyagent: empty state path") }
	return &FileStateStore{path: path}, nil
}

func (s *FileStateStore) Load(ctx context.Context) (StateMarker, error) {
	if err := ctx.Err(); err != nil { return StateMarker{}, err }
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) { return StateMarker{Entries: map[string]EntryMarker{}}, nil }
	if err != nil { return StateMarker{}, errors.New("applyagent: read state marker") }
	var marker StateMarker
	decoder := json.NewDecoder(bytes.NewReader(data)); decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil { return StateMarker{}, errors.New("applyagent: decode state marker") }
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) { return StateMarker{}, errors.New("applyagent: trailing state marker data") }
	if err := validateStateMarker(marker); err != nil { return StateMarker{}, err }
	return marker, nil
}

func (s *FileStateStore) Store(ctx context.Context, marker StateMarker) error {
	if err := ctx.Err(); err != nil { return err }
	if err := validateStateMarker(marker); err != nil { return err }
	data, err := json.Marshal(marker); if err != nil { return errors.New("applyagent: encode state marker") }
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil { return errors.New("applyagent: create state directory") }
	tmp, err := os.CreateTemp(dir, ".marker-*"); if err != nil { return errors.New("applyagent: create state temp") }
	tmpPath := tmp.Name(); committed := false
	defer func(){ _ = tmp.Close(); if !committed { _ = os.Remove(tmpPath) } }()
	if err := tmp.Chmod(0o600); err != nil { return errors.New("applyagent: chmod state temp") }
	if _, err := tmp.Write(data); err != nil { return errors.New("applyagent: write state temp") }
	if err := tmp.Sync(); err != nil { return errors.New("applyagent: fsync state temp") }
	if err := tmp.Close(); err != nil { return errors.New("applyagent: close state temp") }
	if err := os.Rename(tmpPath, s.path); err != nil { return errors.New("applyagent: replace state marker") }
	directory, err := os.Open(dir); if err != nil { return errors.New("applyagent: open state directory") }
	defer directory.Close()
	if err := directory.Sync(); err != nil { return errors.New("applyagent: fsync state directory") }
	committed = true
	return nil
}

func validateStateMarker(marker StateMarker) error {
	if !validSHA256(marker.SnapshotSHA256) { return errors.New("applyagent: invalid state snapshot hash") }
	for customerID, entry := range marker.Entries {
		if strings.TrimSpace(customerID)=="" || entry.Generation<=0 || !validSHA256(entry.PayloadSHA256) { return errors.New("applyagent: invalid state entry") }
	}
	return nil
}
