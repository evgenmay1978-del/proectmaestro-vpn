package vkturnconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func validStoreCfg() *Config {
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32-byte base64
	clients := map[string]Client{}
	for i, l := range []string{"wapmix", "wapmixx", "wapmix2"} {
		clients[l] = Client{Password: "pass-" + l, WG: subgen.VKTurnCreds{
			PrivateKey: key, PeerPublicKey: key, LocalAddress: "10.66.66." + string(rune('2'+i)) + "/32",
		}}
	}
	return &Config{Enabled: true, MinVersionCode: 90181, Server: "wdtt.example:56000", VKHashes: []string{"hash-room-a"}, Clients: clients}
}

// task7BaseConfig mirrors the strict on-disk reader from the Task 7 base
// revision c374426. Keeping this decoder in the test proves that a rollback
// binary can still read every state written by the repaired store.
type task7BaseConfig struct {
	Enabled               bool              `json:"enabled"`
	MinVersionCode        int               `json:"min_version_code"`
	Server                string            `json:"server"`
	VKHashes              []string          `json:"vk_hashes"`
	CandidateVKHashes     []string          `json:"candidate_vk_hashes,omitempty"`
	LastKnownGoodVKHashes []string          `json:"last_known_good_vk_hashes,omitempty"`
	ProbeStatus           ProbeStatus       `json:"probe_status,omitempty"`
	ProbeStartedAt        time.Time         `json:"probe_started_at,omitempty"`
	ProbeCheckedAt        time.Time         `json:"probe_checked_at,omitempty"`
	ProbeErrorCode        string            `json:"probe_error_code,omitempty"`
	Clients               map[string]Client `json:"clients"`
}

func readTask7BaseConfig(path string) (*task7BaseConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var cfg task7BaseConfig
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("trailing rollback JSON")
	}
	if len(cfg.VKHashes) < 1 || len(cfg.VKHashes) > 4 {
		return nil, errors.New("Task 7 base requires 1..4 persisted vk_hashes")
	}
	return &cfg, nil
}

func TestStoreSetPersistsAtomicallyAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vkturn.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != nil {
		t.Fatal("a fresh store over a missing file must be OFF (nil) until Set")
	}
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("persisted file missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("persisted file mode = %v, want 0600 (secrets)", fi.Mode().Perm())
	}
	// A fresh OpenStore re-parses+validates the persisted file — proving a real round trip.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := s2.Get()
	if got == nil || !got.Enabled || got.Server != "wdtt.example:56000" || len(got.Clients) != 3 {
		t.Fatalf("reloaded config wrong: %+v", got)
	}
}

func TestStoreGetIsIsolatedSnapshot(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	snap := s.Get()
	snap.Enabled = false
	snap.VKHashes[0] = "mutated"
	snap.Clients["wapmix"] = Client{}
	live := s.Get()
	if !live.Enabled || live.VKHashes[0] != "hash-room-a" || live.Clients["wapmix"].Password == "" {
		t.Fatal("mutating a Get() snapshot leaked into the live store")
	}
}

func TestStoreSetRejectsInvalidAndKeepsPrevious(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	bad := validStoreCfg()
	bad.MinVersionCode = 0 // invalid → Validate must reject
	if err := s.Set(bad); err == nil {
		t.Fatal("Set accepted an invalid config")
	}
	if s.Get().MinVersionCode != 90181 {
		t.Fatal("a rejected edit clobbered the live config (must be fail-closed)")
	}
}

func TestOpenStoreEmptyPathIsNil(t *testing.T) {
	// An unset MAESTRO_VKTURN_FILE (empty path) must yield a nil store so the caller
	// treats WDTT as OFF and the panel guards can fire — never an editable, ephemeral
	// in-memory store that would silently drop config on restart.
	s, err := OpenStore("")
	if err != nil || s != nil {
		t.Fatalf(`OpenStore("") must return (nil, nil): store=%v err=%v`, s, err)
	}
}

func TestOpenStoreMissingIsOffExistingInvalidFailsClosed(t *testing.T) {
	dir := t.TempDir()
	// configured path, missing file → OFF, no error (panel populates it later)
	s, err := OpenStore(filepath.Join(dir, "absent.json"))
	if err != nil || s.Get() != nil {
		t.Fatalf("missing file should start OFF: err=%v cfg=%v", err, s.Get())
	}
	// existing but incomplete file → startup error (fail-closed)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(bad); err == nil {
		t.Fatal("an existing but invalid file must fail startup closed")
	}
}

func TestCandidateIsIsolatedPromotedAtomicallyAndRejectedSafely(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	lease, err := s.StageCandidate([]string{"hash-room-b"}, started)
	if err != nil {
		t.Fatalf("StageCandidate: %v", err)
	}
	checking := s.Get()
	if !reflect.DeepEqual(checking.VKHashes, []string{"hash-room-a"}) {
		t.Fatalf("candidate changed active hashes: %#v", checking.VKHashes)
	}
	if !reflect.DeepEqual(checking.CandidateVKHashes, []string{"hash-room-b"}) ||
		!reflect.DeepEqual(checking.LastKnownGoodVKHashes, []string{"hash-room-a"}) ||
		checking.ProbeStatus != ProbeStatusChecking || !checking.ProbeStartedAt.Equal(started) {
		t.Fatalf("checking state wrong: %+v", checking)
	}

	checked := started.Add(5 * time.Second)
	if err := s.PromoteCandidate(lease, checked); err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}
	promoted := s.Get()
	if !reflect.DeepEqual(promoted.VKHashes, []string{"hash-room-b"}) ||
		len(promoted.CandidateVKHashes) != 0 ||
		!reflect.DeepEqual(promoted.LastKnownGoodVKHashes, []string{"hash-room-b"}) ||
		promoted.ProbeStatus != ProbeStatusActive || !promoted.ProbeCheckedAt.Equal(checked) ||
		promoted.ProbeErrorCode != "" {
		t.Fatalf("promoted state wrong: %+v", promoted)
	}

	lease, err = s.StageCandidate([]string{"hash-room-c"}, checked.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rejectedAt := checked.Add(2 * time.Second)
	if err := s.RejectCandidate(lease, "TURN_ALLOCATION_FAILED", rejectedAt); err != nil {
		t.Fatalf("RejectCandidate: %v", err)
	}
	rejected := s.Get()
	if !reflect.DeepEqual(rejected.VKHashes, []string{"hash-room-b"}) ||
		!reflect.DeepEqual(rejected.LastKnownGoodVKHashes, []string{"hash-room-b"}) ||
		len(rejected.CandidateVKHashes) != 0 || rejected.ProbeStatus != ProbeStatusFailed ||
		rejected.ProbeErrorCode != "TURN_ALLOCATION_FAILED" || !rejected.ProbeCheckedAt.Equal(rejectedAt) {
		t.Fatalf("rejected state wrong: %+v", rejected)
	}
}

func TestDisabledBaseMayOmitRoomsButEnabledMayNot(t *testing.T) {
	cfg := validStoreCfg()
	cfg.Enabled = false
	cfg.VKHashes = nil
	if err := NewInMemory().Set(cfg); err != nil {
		t.Fatalf("complete disabled base rejected: %v", err)
	}

	cfg.Enabled = true
	if err := NewInMemory().Set(cfg); err == nil {
		t.Fatal("enabled configuration accepted without an active room")
	}
}

func TestCandidateValidationAndFailureLeaveActiveUntouched(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	for _, hashes := range [][]string{
		{},
		{"duplicate-room", "duplicate-room"},
		{"room-one", "room-two", "room-three", "room-four", "room-five"},
		{"bad\nvalue"},
	} {
		if _, err := s.StageCandidate(hashes, time.Now().UTC()); err == nil {
			t.Fatalf("StageCandidate accepted invalid hashes: %#v", hashes)
		}
		if got := s.Get(); !reflect.DeepEqual(got.VKHashes, []string{"hash-room-a"}) || got.ProbeStatus != ProbeStatusActive {
			t.Fatalf("invalid candidate changed active state: %+v", got)
		}
	}

	lease, err := s.StageCandidate([]string{"hash-room-b"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RejectCandidate(lease, "bad\nsecret", time.Now().UTC()); err == nil {
		t.Fatal("RejectCandidate accepted an unsafe error code")
	}
	if got := s.Get(); got.ProbeStatus != ProbeStatusChecking || !reflect.DeepEqual(got.VKHashes, []string{"hash-room-a"}) {
		t.Fatalf("invalid rejection changed checking/active state: %+v", got)
	}
}

func TestCandidateStatePersistsAndLegacyJSONDefaultsToActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vkturn.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	if _, err := s.StageCandidate([]string{"hash-room-b"}, started); err != nil {
		t.Fatal(err)
	}
	reloaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Get()
	if got.ProbeStatus != ProbeStatusChecking || !reflect.DeepEqual(got.CandidateVKHashes, []string{"hash-room-b"}) ||
		!reflect.DeepEqual(got.VKHashes, []string{"hash-room-a"}) || !got.ProbeStartedAt.Equal(started) {
		t.Fatalf("persisted checking state wrong: %+v", got)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	legacy, err := json.Marshal(validStoreCfg())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	legacyStore, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("legacy reload: %v", err)
	}
	legacyGot := legacyStore.Get()
	if legacyGot.ProbeStatus != ProbeStatusActive ||
		!reflect.DeepEqual(legacyGot.LastKnownGoodVKHashes, legacyGot.VKHashes) {
		t.Fatalf("legacy state was not normalized to active: %+v", legacyGot)
	}
}

func TestConcurrentCandidateLeaseSerializesStageAndPromotion(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	leases := make(chan uint64, 1)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, hash := range []string{"hash-room-b", "hash-room-c", "hash-room-d", "hash-room-e"} {
		hash := hash
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if lease, err := s.StageCandidate([]string{hash}, time.Now().UTC()); err == nil {
				successes.Add(1)
				leases <- lease
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent candidate leases succeeded %d times, want exactly 1", successes.Load())
	}
	checking := s.Get()
	if checking.ProbeStatus != ProbeStatusChecking || len(checking.CandidateVKHashes) != 1 ||
		!reflect.DeepEqual(checking.VKHashes, []string{"hash-room-a"}) {
		t.Fatalf("serialized checking state wrong: %+v", checking)
	}
	lease := <-leases
	if err := s.PromoteCandidate(lease, time.Now().UTC()); err != nil {
		t.Fatalf("promotion after serialized stage: %v", err)
	}
	if got := s.Get(); got.ProbeStatus != ProbeStatusActive || !reflect.DeepEqual(got.VKHashes, checking.CandidateVKHashes) {
		t.Fatalf("serialized promotion wrong: %+v", got)
	}
}

func TestStalePersistedCandidateLeaseCanBeReplacedWithoutOldPromotion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vkturn.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	oldLease, err := s.StageCandidate([]string{"hash-room-b"}, started)
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	newLease, err := restarted.StageCandidate([]string{"hash-room-c"}, started.Add(time.Second))
	if err != nil {
		t.Fatalf("foreign persisted lease was not immediately recoverable after restart: %v", err)
	}
	if oldLease == newLease {
		t.Fatalf("replacement reused stale lease %d", oldLease)
	}
	if err := s.PromoteCandidate(oldLease, started.Add(2*time.Second)); !errors.Is(err, ErrCandidateStale) {
		t.Fatal("stale probe result promoted the replacement candidate")
	}
	checking := restarted.Get()
	if checking.ProbeStatus != ProbeStatusChecking || !reflect.DeepEqual(checking.CandidateVKHashes, []string{"hash-room-c"}) || !reflect.DeepEqual(checking.VKHashes, []string{"hash-room-a"}) {
		t.Fatalf("stale result changed replacement state: %+v", checking)
	}
	if err := restarted.PromoteCandidate(newLease, started.Add(3*time.Second)); err != nil {
		t.Fatalf("replacement lease could not promote: %v", err)
	}
	if got := restarted.Get(); got.ProbeStatus != ProbeStatusActive || !reflect.DeepEqual(got.VKHashes, []string{"hash-room-c"}) {
		t.Fatalf("replacement candidate was not promoted: %+v", got)
	}
}

func TestCandidateLeaseUsesMonotonicRuntimeClock(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	var monotonicNow time.Duration
	s.leaseNow = func() time.Duration { return monotonicNow }
	wall := time.Date(2026, 8, 15, 11, 0, 0, 0, time.UTC)
	oldLease, err := s.StageCandidate([]string{"hash-room-b"}, wall)
	if err != nil {
		t.Fatal(err)
	}
	for _, jumpedWall := range []time.Time{wall.Add(24 * time.Hour), wall.Add(-24 * time.Hour)} {
		if _, err := s.StageCandidate([]string{"hash-room-c"}, jumpedWall); !errors.Is(err, ErrCandidateBusy) {
			t.Fatalf("wall-clock jump changed live monotonic lease: %v", err)
		}
	}
	monotonicNow = candidateProbeLease + time.Second
	newLease, err := s.StageCandidate([]string{"hash-room-c"}, wall.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("expired monotonic lease was not replaceable: %v", err)
	}
	if oldLease == newLease {
		t.Fatalf("replacement reused runtime generation %d", oldLease)
	}
	if err := s.PromoteCandidate(oldLease, wall.Add(time.Second)); !errors.Is(err, ErrCandidateStale) {
		t.Fatalf("old runtime result was not fenced: %v", err)
	}
}

func TestPersistedCandidateStatesRemainReadableByTask7BaseRollback(t *testing.T) {
	for _, state := range []string{"active", "checking", "failed", "disabled"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "vkturn.json")
			s, err := OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg := validStoreCfg()
			if state == "disabled" {
				cfg.Enabled = false
				cfg.VKHashes = nil
			}
			if err := s.Set(cfg); err != nil {
				t.Fatal(err)
			}
			if state == "checking" || state == "failed" {
				lease, err := s.StageCandidate([]string{"hash-room-b"}, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				if state == "failed" {
					if err := s.RejectCandidate(lease, "TLS_TRUST_FAILED", time.Date(2026, 8, 15, 12, 0, 1, 0, time.UTC)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := readTask7BaseConfig(path); err != nil {
				t.Fatalf("Task 7 base rollback cannot read %s state: %v", state, err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"probe_generation"`) {
				t.Fatal("runtime generation leaked into rollback-sensitive JSON schema")
			}
			if state == "disabled" {
				reloaded, err := OpenStore(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := reloaded.Get(); got == nil || len(got.VKHashes) != 0 {
					t.Fatalf("rollback placeholder leaked into live disabled config: %+v", got)
				}
			}
		})
	}
}

func TestStageCandidateReturnsTypedErrors(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageCandidate([]string{"short"}, time.Now().UTC()); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("invalid candidate error = %v", err)
	}
	if _, err := s.StageCandidate([]string{"hash-room-b"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageCandidate([]string{"hash-room-c"}, time.Now().UTC()); !errors.Is(err, ErrCandidateBusy) {
		t.Fatalf("busy candidate error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "vkturn.json")
	fileStore, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileStore.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.StageCandidate([]string{"hash-room-b"}, time.Now().UTC()); !errors.Is(err, ErrCandidatePersistence) {
		t.Fatalf("persistence error = %v", err)
	}
}
