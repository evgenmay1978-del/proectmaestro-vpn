package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

var errNormalizeInput = errors.New("invalid protected normalization input or output")

// An inventory is required even for customer preparation. Existing non-customer
// sources are hashed and declared unconverted, never reported as migrated.
type normalizeInventory struct {
	SchemaVersion    int                              `json:"schema_version"`
	Scope            string                           `json:"scope"`
	Sources          map[string]normalizeSourceInput  `json:"sources"`
	ProtocolBindings []importer.LegacyProtocolBinding `json:"protocol_bindings"`
}

type normalizeSourceInput struct {
	State string `json:"state"`
	Path  string `json:"path"`
}

type normalizeFileStamp struct {
	path, sha string
	absent    bool
}

func runNormalize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("normalize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var customersPath, capturePath, inventoryPath, keyPath, parentPath, outputPath string
	var maxCaptureAge time.Duration
	flags.StringVar(&customersPath, "customers", "", "protected raw customers JSON")
	flags.StringVar(&capturePath, "xui-capture", "", "protected capture-xui output")
	flags.StringVar(&inventoryPath, "inventory", "", "protected source inventory and protocol bindings")
	flags.StringVar(&keyPath, "key-file", "", "existing protected import key bundle")
	flags.StringVar(&parentPath, "parent-snapshot", "", "authenticated initial full snapshot for final delta")
	flags.StringVar(&outputPath, "output", "", "new protected Snapshot v2 file")
	flags.DurationVar(&maxCaptureAge, "max-capture-age", 0, "explicit maximum age of the XUI/source capture")
	fail := func() int {
		writeError(stderr, "customer normalization input or output is invalid")
		return exitInputSystem
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 || maxCaptureAge <= 0 ||
		customersPath == "" || capturePath == "" || inventoryPath == "" || keyPath == "" || outputPath == "" {
		return fail()
	}
	stamps := []normalizeFileStamp{}
	read := func(path string) ([]byte, error) {
		data, err := readRuntimeFile(path, maxSnapshotSize, true)
		if err != nil {
			return nil, errNormalizeInput
		}
		stamps = append(stamps, normalizeFileStamp{path: path, sha: runtimeSHA256Hex(data)})
		return data, nil
	}
	raw, err := read(customersPath)
	if err != nil {
		return fail()
	}
	defer zero(raw)
	captureBytes, err := read(capturePath)
	if err != nil {
		return fail()
	}
	defer zero(captureBytes)
	var capture importer.LegacyXUICapture
	if strictRuntimeJSON(captureBytes, &capture) != nil {
		return fail()
	}
	inventoryBytes, err := read(inventoryPath)
	if err != nil {
		return fail()
	}
	defer zero(inventoryBytes)
	var inventory normalizeInventory
	if strictRuntimeJSON(inventoryBytes, &inventory) != nil || inventory.SchemaVersion != 1 ||
		inventory.Scope != importer.LegacyCustomerPreparationScope || len(inventory.Sources) != 4 {
		return fail()
	}
	sources := map[string]importer.LegacySourcePresence{}
	for _, domain := range []string{"orders", "trials", "settings", "principals"} {
		input, exists := inventory.Sources[domain]
		if !exists || input.Path == "" {
			return fail()
		}
		switch input.State {
		case "absent":
			if _, err := os.Lstat(input.Path); !os.IsNotExist(err) {
				return fail()
			}
			stamps = append(stamps, normalizeFileStamp{path: input.Path, absent: true})
			sources[domain] = importer.LegacySourcePresence{State: "absent"}
		case "present":
			data, err := read(input.Path)
			if err != nil {
				return fail()
			}
			sources[domain] = importer.LegacySourcePresence{State: "present", SHA256: runtimeSHA256Hex(data)}
			zero(data)
		default:
			return fail()
		}
	}
	// Capture the key file fingerprint too; never expose its contents or name in
	// errors. loadKeyBundle remains the single parser/key-policy implementation.
	keyBytes, err := read(keyPath)
	if err != nil {
		return fail()
	}
	zero(keyBytes)
	keys, err := loadKeyBundle(keyPath)
	if err != nil {
		return fail()
	}
	defer keys.zero()
	box, err := controlplane.NewSecretBox(keys.CurrentKeyVersion, keys.EncryptionKeys, keys.HMACKey)
	if err != nil {
		return fail()
	}
	var parent *importer.Snapshot
	if parentPath != "" {
		data, err := read(parentPath)
		if err != nil {
			return fail()
		}
		decoded, decodeErr := importer.DecodeSnapshot(data)
		zero(data)
		if decodeErr != nil {
			return fail()
		}
		parent = &decoded
	}
	for _, stamp := range stamps {
		if normalizeSamePath(stamp.path, outputPath) {
			return fail()
		}
	}
	snapshot, err := importer.NormalizeLegacyCustomers(raw, capture, box, keys.HMACKey, importer.LegacyNormalizeOptions{
		Now: time.Now().UTC(), MaxCaptureAge: maxCaptureAge, Sources: sources,
		ProtocolBindings: inventory.ProtocolBindings, Parent: parent, PlanOptions: defaultPlanOptions(),
	})
	if err != nil {
		return fail()
	}
	// Inventory bytes are retained by digest as well as the normalized presence
	// facts. Source path strings themselves do not enter ordinary identity rows.
	snapshot.SourceHashes["source_inventory"] = runtimeSHA256Hex(inventoryBytes)
	if verifyNormalizeFiles(stamps) != nil {
		return fail()
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fail()
	}
	defer zero(encoded)
	// The final exact output (including the inventory hash) passes both the real
	// planner and production protection validation immediately before publication.
	planOptions := defaultPlanOptions()
	planOptions.ParentSnapshot = parent
	planOptions.AppliedParentDigest = snapshot.ParentSourceDigest
	_, report := importer.Plan(snapshot, planOptions)
	protection := importer.ProtectionFromSnapshot(snapshot, parent)
	if len(report.Blockers) != 0 {
		return fail()
	}
	if _, err := importer.ValidateSnapshotProtection(protection, box, keys.HMACKey, nil); err != nil {
		return fail()
	}
	if _, err := importer.ValidateProductionCustomerIdentities(protection, box); err != nil {
		return fail()
	}
	if writeNormalizeOutput(outputPath, encoded) != nil {
		return fail()
	}
	_, _ = fmt.Fprintln(stdout, "customer preparation snapshot written; cutover_ready=false")
	for _, domain := range []string{"orders", "trials", "settings", "principals"} {
		_, _ = fmt.Fprintf(stdout, "%s=%s; conversion not performed\n", domain, sources[domain].State)
	}
	return exitClean
}

func verifyNormalizeFiles(stamps []normalizeFileStamp) error {
	for _, stamp := range stamps {
		if stamp.absent {
			if _, err := os.Lstat(stamp.path); !os.IsNotExist(err) {
				return errNormalizeInput
			}
			continue
		}
		data, err := readRuntimeFile(stamp.path, maxSnapshotSize, true)
		if err != nil {
			return errNormalizeInput
		}
		matches := runtimeSHA256Hex(data) == stamp.sha
		zero(data)
		if !matches {
			return errNormalizeInput
		}
	}
	return nil
}

func normalizeSamePath(left, right string) bool {
	l, le := filepath.Abs(left)
	r, re := filepath.Abs(right)
	return le != nil || re != nil || strings.EqualFold(filepath.Clean(l), filepath.Clean(r))
}

// No-replace publication reuses the import receipt's platform-specific atomic
// primitive. A pre-existing destination (including a symlink/input) always fails.
func writeNormalizeOutput(path string, data []byte) error {
	if strings.TrimSpace(path) == "" || len(data) == 0 || len(data) > maxSnapshotSize {
		return errNormalizeInput
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errNormalizeInput
	}
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".maestro-import-capture-*")
	if err != nil {
		return errNormalizeInput
	}
	tempPath := temp.Name()
	defer func() { _ = temp.Close(); _ = os.Remove(tempPath) }()
	if temp.Chmod(0o600) != nil {
		return errNormalizeInput
	}
	if _, err := temp.Write(data); err != nil {
		return errNormalizeInput
	}
	if temp.Sync() != nil || temp.Close() != nil {
		return errNormalizeInput
	}
	if renameReceiptNoReplace(tempPath, path) != nil {
		return errNormalizeInput
	}
	parent, err := os.Open(directory)
	if err != nil {
		return errNormalizeInput
	}
	syncErr, closeErr := parent.Sync(), parent.Close()
	if syncErr != nil || closeErr != nil {
		return errNormalizeInput
	}
	return nil
}
