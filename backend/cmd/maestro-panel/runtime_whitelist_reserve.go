package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const runtimeWhiteListReserveLimit = 64 << 10

var errRuntimeWhiteListReserveUnavailable = errors.New("white-list reserve measurement unavailable")

type runtimeWhiteListReserveProvider func(context.Context) (map[string]controlplane.WhiteListAdmissionReserve, error)

// The report is trusted operator input from measured canary traffic, not a
// bandwidth estimate or a claim that parsing JSON proves the production SLO.
// Reload once per pass so renewal does not require restarting ordinary VPN.
func runtimeWhiteListReserveFile(path string, now func() time.Time) runtimeWhiteListReserveProvider {
	path = strings.TrimSpace(path)
	if path == "" || now == nil {
		return nil
	}
	return func(ctx context.Context) (map[string]controlplane.WhiteListAdmissionReserve, error) {
		if ctx == nil || ctx.Err() != nil {
			return nil, errRuntimeWhiteListReserveUnavailable
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > runtimeWhiteListReserveLimit {
			return nil, errRuntimeWhiteListReserveUnavailable
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errRuntimeWhiteListReserveUnavailable
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, runtimeWhiteListReserveLimit+1))
		if err != nil || len(data) > runtimeWhiteListReserveLimit || ctx.Err() != nil {
			return nil, errRuntimeWhiteListReserveUnavailable
		}
		var report struct {
			SchemaVersion int    `json:"schema_version"`
			Unit          string `json:"unit"`
			Basis         string `json:"basis"`
			Measurements  []struct {
				ExitID                     string `json:"exit_id"`
				MeasuredP999BytesPerSecond uint64 `json:"measured_p999_bytes_per_second"`
				MeasuredAtUnix             int64  `json:"measured_at_unix"`
				ValidUntilUnix             int64  `json:"valid_until_unix"`
			} `json:"measurements"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&report) != nil || decoder.Decode(new(any)) != io.EOF ||
			report.SchemaVersion != 1 || report.Unit != "BYTES_PER_SECOND" || report.Basis != "UPLINK_PLUS_DOWNLINK" || len(report.Measurements) == 0 {
			return nil, errRuntimeWhiteListReserveUnavailable
		}
		reserves := make(map[string]controlplane.WhiteListAdmissionReserve, len(report.Measurements))
		for _, measurement := range report.Measurements {
			if measurement.ExitID == "" || strings.TrimSpace(measurement.ExitID) != measurement.ExitID || measurement.MeasuredP999BytesPerSecond == 0 {
				return nil, errRuntimeWhiteListReserveUnavailable
			}
			if _, duplicate := reserves[measurement.ExitID]; duplicate {
				return nil, errRuntimeWhiteListReserveUnavailable
			}
			reserve := controlplane.WhiteListAdmissionReserve{
				MeasuredP999BytesPerSecond: measurement.MeasuredP999BytesPerSecond,
				MeasuredAtUnix:             measurement.MeasuredAtUnix, ValidUntilUnix: measurement.ValidUntilUnix,
			}
			if _, err := reserve.RequiredBytes(now().Unix()); err != nil {
				return nil, errRuntimeWhiteListReserveUnavailable
			}
			reserves[measurement.ExitID] = reserve
		}
		return reserves, nil
	}
}
