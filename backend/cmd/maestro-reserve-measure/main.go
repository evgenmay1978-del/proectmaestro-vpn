// maestro-reserve-measure observes an explicitly isolated account. It never
// provisions users, generates traffic, resets counters, or installs its report.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

const (
	jsonLimit     = 64 << 10
	evidenceLimit = 64 << 20
	maximumRate   = uint64(9223372036854775806 / 5)
)

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type binding struct {
	ActionKey            string `json:"action_key"`
	OriginID             string `json:"origin_id"`
	ReleaseID            string `json:"release_id"`
	XrayProcessBootID    string `json:"xray_process_boot_id"`
	ConfigDigest         string `json:"config_digest"`
	DesiredGeneration    int64  `json:"desired_generation"`
	ManagedUserSetDigest string `json:"managed_user_set_digest"`
}

func receiptBinding(r sidecaragentclient.Receipt) binding {
	return binding{r.ActionKey, r.OriginID, r.ReleaseID, r.XrayProcessBootID,
		r.ConfigDigest, r.DesiredGeneration, r.ManagedUserSetDigest}
}

type originConfig struct {
	BaseURL    string  `json:"base_url"`
	ServerName string  `json:"server_name"`
	CAFile     string  `json:"ca_file"`
	CertFile   string  `json:"cert_file"`
	KeyFile    string  `json:"key_file"`
	Expected   binding `json:"expected_binding"`
}

type exitConfig struct {
	ExitID    string   `json:"exit_id"`
	OriginIDs []string `json:"origin_ids"`
}

type config struct {
	SchemaVersion        int            `json:"schema_version"`
	EntitlementID        string         `json:"entitlement_id"`
	WorkloadDescription  string         `json:"workload_description"`
	FleetDescription     string         `json:"fleet_description"`
	ValidityReason       string         `json:"validity_reason"`
	SampleCount          int            `json:"sample_count"`
	IntervalMillis       int            `json:"interval_millis"`
	RequestTimeoutMillis int            `json:"request_timeout_millis"`
	MaxClockSkewMillis   int            `json:"max_clock_skew_millis"`
	ValidUntilUnix       int64          `json:"valid_until_unix"`
	Origins              []originConfig `json:"origins"`
	Exits                []exitConfig   `json:"exits"`
}

func (c config) interval() time.Duration { return time.Duration(c.IntervalMillis) * time.Millisecond }
func (c config) timeout() time.Duration {
	return time.Duration(c.RequestTimeoutMillis) * time.Millisecond
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

func description(s string) bool {
	return len(s) >= 10 && len(s) <= 1024 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}

func (c config) validate(now time.Time) error {
	if c.SchemaVersion != 1 || !identifier.MatchString(c.EntitlementID) ||
		!strings.HasPrefix(c.EntitlementID, "isolated-") || len(c.EntitlementID) <= len("isolated-") ||
		!description(c.WorkloadDescription) || !description(c.FleetDescription) || !description(c.ValidityReason) ||
		c.SampleCount < 1000 || c.SampleCount > 10000 || c.IntervalMillis < 250 || c.IntervalMillis > 2000 ||
		c.RequestTimeoutMillis < 1 || c.RequestTimeoutMillis > c.IntervalMillis/4 ||
		c.MaxClockSkewMillis < 0 || c.MaxClockSkewMillis > 100 ||
		len(c.Origins) < 1 || len(c.Origins) > 8 || len(c.Exits) < 1 || len(c.Exits) > 8 {
		return errors.New("invalid explicit measurement configuration")
	}
	duration := time.Duration(c.SampleCount+1) * c.interval()
	if duration > time.Hour || c.ValidUntilUnix <= now.Add(duration).Unix() || c.ValidUntilUnix > now.Add(24*time.Hour).Unix() {
		return errors.New("invalid measurement duration or explicit report expiry")
	}
	origins := make(map[string]bool)
	for _, origin := range c.Origins {
		b := origin.Expected
		if !identifier.MatchString(b.OriginID) || origins[b.OriginID] || !identifier.MatchString(b.ReleaseID) ||
			!validDigest(b.XrayProcessBootID) || !validDigest(b.ConfigDigest) || !validDigest(b.ManagedUserSetDigest) ||
			b.DesiredGeneration < 1 || b.ActionKey == "" || len(b.ActionKey) > 512 ||
			strings.TrimSpace(b.ActionKey) != b.ActionKey || strings.ContainsAny(b.ActionKey, "\x00\r\n\t") {
			return errors.New("invalid or duplicate expected Origin binding")
		}
		origins[b.OriginID] = true
	}
	exits, covered := make(map[string]bool), make(map[string]bool)
	for _, exit := range c.Exits {
		if !identifier.MatchString(exit.ExitID) || exits[exit.ExitID] || len(exit.OriginIDs) == 0 || len(exit.OriginIDs) > len(origins) {
			return errors.New("invalid or duplicate exit mapping")
		}
		exits[exit.ExitID] = true
		seen := make(map[string]bool)
		for _, id := range exit.OriginIDs {
			if !origins[id] || seen[id] {
				return errors.New("unknown or duplicate Origin in exit mapping")
			}
			seen[id], covered[id] = true, true
		}
	}
	if len(covered) != len(origins) {
		return errors.New("unmapped configured Origin")
	}
	return nil
}

// Reject symlinks and writable ancestors before reading privileged inputs or
// creating output. Linux is required by the CLI's POSIX permission contract.
func protectedDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute protected directory required")
	}
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("unsafe directory permissions or symlink")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func protectedInput(path string, private bool) ([]byte, error) {
	if !filepath.IsAbs(path) || protectedDirectory(filepath.Dir(path)) != nil {
		return nil, errors.New("unsafe input directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > jsonLimit ||
		info.Mode().Perm()&0022 != 0 || (private && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("unsafe input file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("input open failed")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("input identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, jsonLimit+1))
	if err != nil || len(data) > jsonLimit {
		return nil, errors.New("input read failed or too large")
	}
	return data, nil
}

func readConfig(path string) (config, string, error) {
	var c config
	data, err := protectedInput(path, true)
	if err != nil {
		return c, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return c, "", errors.New("invalid configuration JSON")
	}
	digest := sha256.Sum256(data)
	return c, hex.EncodeToString(digest[:]), c.validate(time.Now())
}

type counters struct {
	Uplink   uint64 `json:"uplink_bytes"`
	Downlink uint64 `json:"downlink_bytes"`
}

type observation struct {
	Binding          binding             `json:"binding"`
	StartedAt        time.Time           `json:"request_started_at"`
	FinishedAt       time.Time           `json:"request_finished_at"`
	SampledAt        time.Time           `json:"sampled_at"`
	MinimumElapsedNS int64               `json:"minimum_elapsed_ns"`
	Counters         map[string]counters `json:"exit_counters"`
}

type round struct {
	Index       int               `json:"index"`
	ScheduledAt time.Time         `json:"scheduled_at"`
	Origins     []observation     `json:"origins"`
	Rates       map[string]uint64 `json:"conservative_bytes_per_second,omitempty"`
}

func selectObservation(c config, origin originConfig, snapshot sidecaragentclient.UsageSnapshot, started, finished time.Time) (observation, error) {
	value := observation{Binding: receiptBinding(snapshot.Receipt), StartedAt: started, FinishedAt: finished,
		SampledAt: snapshot.SampledAt, Counters: make(map[string]counters)}
	skew := time.Duration(c.MaxClockSkewMillis) * time.Millisecond
	if value.Binding != origin.Expected || finished.Before(started) || finished.Sub(started) > c.timeout() ||
		snapshot.SampledAt.Before(started.Add(-skew)) || snapshot.SampledAt.After(finished.Add(skew)) ||
		!snapshot.Receipt.ExpiresAt.After(finished) {
		return value, errors.New("usage binding or sample time invalid")
	}
	users := make(map[string]counters, len(snapshot.Users))
	for _, user := range snapshot.Users {
		users[user.Email] = counters{user.UplinkBytes, user.DownlinkBytes}
	}
	for _, exit := range c.Exits {
		for _, id := range exit.OriginIDs {
			if id != origin.Expected.OriginID {
				continue
			}
			user, ok := users["wl:"+c.EntitlementID+":"+exit.ExitID]
			if !ok {
				return value, errors.New("configured account counter pair unavailable")
			}
			value.Counters[exit.ExitID] = user
		}
	}
	return value, nil
}

func sampleRound(ctx context.Context, c config, clients []*sidecaragentclient.Client, index int, due time.Time) (round, error) {
	result := round{Index: index, ScheduledAt: due, Origins: make([]observation, len(c.Origins))}
	if len(clients) != len(c.Origins) || ctx.Err() != nil || time.Since(due) < 0 || time.Since(due) > c.interval()/4 {
		return result, errors.New("sampling schedule missed")
	}
	ctx, cancel := context.WithDeadline(ctx, due.Add(c.interval()/2))
	defer cancel()
	errs := make([]error, len(clients))
	var group sync.WaitGroup
	for i, client := range clients {
		group.Add(1)
		go func(i int, client *sidecaragentclient.Client) {
			defer group.Done()
			started := time.Now()
			snapshot, err := client.LookupUsage(ctx, c.Origins[i].Expected.ActionKey)
			finished := time.Now()
			if err != nil {
				errs[i] = errors.New("authenticated usage request failed")
				return
			}
			result.Origins[i], errs[i] = selectObservation(c, c.Origins[i], snapshot, started, finished)
		}(i, client)
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			return result, err
		}
	}
	if ctx.Err() != nil || time.Now().After(due.Add(c.interval()/2)) {
		return result, errors.New("complete sampling window exceeded")
	}
	return result, nil
}

func add(a, b uint64) (uint64, error) {
	if ^uint64(0)-a < b {
		return 0, errors.New("counter arithmetic overflow")
	}
	return a + b, nil
}

func ceilRate(delta uint64, elapsedNS int64) (uint64, error) {
	if elapsedNS <= 0 {
		return 0, errors.New("invalid inter-read duration")
	}
	high, low := bits.Mul64(delta, uint64(time.Second))
	if high >= uint64(elapsedNS) {
		return 0, errors.New("rate overflow")
	}
	rate, remainder := bits.Div64(high, low, uint64(elapsedNS))
	if remainder != 0 {
		var err error
		rate, err = add(rate, 1)
		if err != nil {
			return 0, err
		}
	}
	if rate > maximumRate {
		return 0, errors.New("rate exceeds admission reserve range")
	}
	return rate, nil
}

func reduce(c config, previous round, current *round) error {
	if current.Index != previous.Index+1 || current.ScheduledAt.Sub(previous.ScheduledAt) != c.interval() ||
		len(previous.Origins) != len(c.Origins) || len(current.Origins) != len(c.Origins) {
		return errors.New("incomplete or unequal scheduled windows")
	}
	deltas := make(map[string]map[string]uint64)
	for i := range current.Origins {
		before, after := previous.Origins[i], &current.Origins[i]
		elapsed := after.StartedAt.Sub(before.FinishedAt)
		wallElapsed := time.Unix(0, after.StartedAt.UnixNano()).Sub(time.Unix(0, before.FinishedAt.UnixNano()))
		clockDifference := wallElapsed - elapsed
		skew := time.Duration(c.MaxClockSkewMillis) * time.Millisecond
		if before.Binding != c.Origins[i].Expected || after.Binding != before.Binding ||
			!after.SampledAt.After(before.SampledAt) || elapsed <= 0 || elapsed > c.interval()*2 ||
			clockDifference > skew || clockDifference < -skew || len(after.Counters) != len(before.Counters) {
			return errors.New("sample binding, clock, or coverage drift")
		}
		after.MinimumElapsedNS = int64(elapsed)
		deltas[after.Binding.OriginID] = make(map[string]uint64)
		for exit, old := range before.Counters {
			next, ok := after.Counters[exit]
			if !ok || next.Uplink < old.Uplink || next.Downlink < old.Downlink {
				return errors.New("missing or falling cumulative counter")
			}
			delta, err := add(next.Uplink-old.Uplink, next.Downlink-old.Downlink)
			if err != nil {
				return err
			}
			deltas[after.Binding.OriginID][exit] = delta
		}
	}
	current.Rates = make(map[string]uint64)
	for _, exit := range c.Exits {
		var total uint64
		minimum := int64(c.interval() * 2)
		for _, id := range exit.OriginIDs {
			delta, ok := deltas[id][exit.ExitID]
			if !ok {
				return errors.New("incomplete account Origin coverage")
			}
			var err error
			total, err = add(total, delta)
			if err != nil {
				return err
			}
			for _, observed := range current.Origins {
				if observed.Binding.OriginID == id && observed.MinimumElapsedNS < minimum {
					minimum = observed.MinimumElapsedNS
				}
			}
		}
		rate, err := ceilRate(total, minimum)
		if err != nil {
			return err
		}
		current.Rates[exit.ExitID] = rate
	}
	return nil
}

type measurement struct {
	ExitID     string `json:"exit_id"`
	Rate       uint64 `json:"measured_p999_bytes_per_second"`
	MeasuredAt int64  `json:"measured_at_unix"`
	ValidUntil int64  `json:"valid_until_unix"`
}

type report struct {
	SchemaVersion int           `json:"schema_version"`
	Unit          string        `json:"unit"`
	Basis         string        `json:"basis"`
	Measurements  []measurement `json:"measurements"`
}

func buildReport(c config, baseline, final round, rates map[string][]uint64, now time.Time) (report, error) {
	result := report{SchemaVersion: 1, Unit: "BYTES_PER_SECOND", Basis: "UPLINK_PLUS_DOWNLINK"}
	if final.Index != c.SampleCount || len(baseline.Origins) != len(c.Origins) || len(final.Origins) != len(c.Origins) || c.ValidUntilUnix <= now.Unix() {
		return result, errors.New("incomplete series or expired report")
	}
	for i, origin := range final.Origins {
		for exit, first := range baseline.Origins[i].Counters {
			last, ok := origin.Counters[exit]
			if !ok || (last.Uplink == first.Uplink && last.Downlink == first.Downlink) {
				return result, errors.New("configured Origin route carried no measured traffic")
			}
		}
	}
	for _, exit := range c.Exits {
		values := append([]uint64(nil), rates[exit.ExitID]...)
		if len(values) != c.SampleCount {
			return result, errors.New("missing exit rate samples")
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		rate := values[(999*len(values)+999)/1000-1]
		if rate == 0 || rate > maximumRate {
			return result, errors.New("inactive or invalid measured p999")
		}
		result.Measurements = append(result.Measurements, measurement{exit.ExitID, rate, now.Unix(), c.ValidUntilUnix})
	}
	return result, nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) >= jsonLimit {
		return errors.New("evidence JSON exceeds bound")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("exclusive evidence file creation failed")
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("evidence write failed")
	}
	return nil
}

func collect(ctx context.Context, c config, digest, output string, clients []*sidecaragentclient.Client) error {
	if !filepath.IsAbs(output) || protectedDirectory(filepath.Dir(output)) != nil || os.Mkdir(output, 0700) != nil {
		return errors.New("new protected output directory required")
	}
	// Do not retain endpoint URLs, certificate/key paths, or other users' counters.
	metadata := struct {
		ConfigSHA256    string       `json:"config_sha256"`
		EntitlementID   string       `json:"entitlement_id"`
		Workload        string       `json:"workload"`
		Fleet           string       `json:"fleet"`
		ValidityReason  string       `json:"validity_reason"`
		Samples         int          `json:"sample_count"`
		IntervalMillis  int          `json:"interval_millis"`
		TimeoutMillis   int          `json:"request_timeout_millis"`
		ClockSkewMillis int          `json:"max_clock_skew_millis"`
		ValidUntilUnix  int64        `json:"valid_until_unix"`
		Exits           []exitConfig `json:"exits"`
		Method          string       `json:"method"`
	}{digest, c.EntitlementID, c.WorkloadDescription, c.FleetDescription, c.ValidityReason,
		c.SampleCount, c.IntervalMillis, c.RequestTimeoutMillis, c.MaxClockSkewMillis, c.ValidUntilUnix, c.Exits,
		"nearest-rank ceil(0.999*N); ceil(all-Origin UP+DOWN delta * 1e9 / shortest proven inter-read nanoseconds); fixed scheduled windows"}
	if err := writeJSON(filepath.Join(output, "metadata.json"), metadata); err != nil {
		return err
	}
	raw, err := os.OpenFile(filepath.Join(output, "samples.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("exclusive samples file creation failed")
	}
	defer raw.Close()
	hash := sha256.New()
	writer := io.MultiWriter(raw, hash)
	var written int
	var baseline, previous round
	rates := make(map[string][]uint64)
	start := time.Now()
	for index := 0; index <= c.SampleCount; index++ {
		due := start.Add(time.Duration(index) * c.interval())
		if delay := time.Until(due); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.New("measurement interrupted")
			case <-timer.C:
			}
		}
		current, err := sampleRound(ctx, c, clients, index, due)
		if err != nil {
			return err
		}
		if index == 0 {
			baseline = current
		} else if err := reduce(c, previous, &current); err != nil {
			return err
		}
		data, err := json.Marshal(current)
		if err != nil || len(data) >= jsonLimit || written+len(data)+1 > evidenceLimit {
			return errors.New("raw evidence size bound exceeded")
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return errors.New("raw evidence write failed")
		}
		written += len(data) + 1
		for exit, rate := range current.Rates {
			rates[exit] = append(rates[exit], rate)
		}
		previous = current
	}
	if err := raw.Sync(); err != nil {
		return errors.New("raw evidence sync failed")
	}
	result, err := buildReport(c, baseline, previous, rates, time.Now())
	if err != nil || ctx.Err() != nil {
		return errors.New("complete measured report unavailable")
	}
	if err := writeJSON(filepath.Join(output, "summary.json"), map[string]any{
		"status": "MEASUREMENT_COMPLETE_UNREVIEWED", "production_ready": false,
		"traffic_generated_by_runner": false, "samples_sha256": hex.EncodeToString(hash.Sum(nil)),
		"samples_bytes": written, "samples_including_baseline": c.SampleCount + 1,
		"sampling_slo_proven": false, "revoke_slo_proven": false,
	}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(output, "report.pending"), result); err != nil {
		return err
	}
	if ctx.Err() != nil || os.Rename(filepath.Join(output, "report.pending"), filepath.Join(output, "report.json")) != nil {
		return errors.New("report publication interrupted or failed")
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("maestro-reserve-measure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "absolute protected configuration path")
	output := flags.String("output", "", "absolute NEW protected output directory")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *configPath == "" || *output == "" || runtime.GOOS != "linux" {
		return errors.New("usage on Linux: maestro-reserve-measure --config ABSOLUTE_FILE --output NEW_ABSOLUTE_DIRECTORY")
	}
	c, digest, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	clients := make([]*sidecaragentclient.Client, len(c.Origins))
	for i, origin := range c.Origins {
		for _, path := range []string{origin.CAFile, origin.CertFile, origin.KeyFile} {
			if _, err := protectedInput(path, path == origin.KeyFile); err != nil {
				return errors.New("unsafe existing mTLS input")
			}
		}
		client, err := sidecaragentclient.New(sidecaragentclient.Config{
			BaseURL: origin.BaseURL, ServerName: origin.ServerName, CAFile: origin.CAFile,
			CertFile: origin.CertFile, KeyFile: origin.KeyFile,
			RequestTimeout: c.timeout(), ReceiptLookupTimeout: c.timeout(),
		})
		if err != nil {
			return errors.New("existing mTLS client configuration unavailable")
		}
		clients[i] = client
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.SampleCount+1)*c.interval())
	defer cancel()
	return collect(ctx, c, digest, *output, clients)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "reserve measurement failed:", err)
		os.Exit(1)
	}
	fmt.Println("Measurement files written; unreviewed canary evidence only. No report installed or feature enabled.")
}
