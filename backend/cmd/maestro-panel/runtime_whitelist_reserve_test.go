package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const runtimeReserveFixture = `{"schema_version":1,"unit":"BYTES_PER_SECOND","basis":"UPLINK_PLUS_DOWNLINK","measurements":[{"exit_id":"exit-nl","measured_p999_bytes_per_second":3000000,"measured_at_unix":2000000,"valid_until_unix":2000020}]}`

func TestRuntimeWhiteListReserveFileReloadsExplicitMeasurementAndExpires(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	path := filepath.Join(t.TempDir(), "reserve.json")
	provider := runtimeWhiteListReserveFile(path, func() time.Time { return now })
	if _, err := provider(context.Background()); err == nil {
		t.Fatal("missing measurement file must not create a reserve")
	}
	if err := os.WriteFile(path, []byte(runtimeReserveFixture), 0600); err != nil {
		t.Fatal(err)
	}
	reserves, err := provider(context.Background())
	if err != nil || len(reserves) != 1 {
		t.Fatalf("load explicit measurement: count=%d error=%v", len(reserves), err)
	}
	bytes, err := reserves["exit-nl"].RequiredBytes(now.Unix())
	if err != nil || bytes != 15_000_000 {
		t.Fatalf("reserve must be measured p999 multiplied by five: %d, %v", bytes, err)
	}
	if _, exists := reserves["exit-other"]; exists {
		t.Fatal("measurement must not fall back across exits")
	}
	now = now.Add(20 * time.Second)
	if _, err := provider(context.Background()); err == nil {
		t.Fatal("expired measurement must not be renewed from the current time")
	}
	now = time.Unix(2_000_000, 0)
	if err := os.WriteFile(path, []byte(strings.Replace(runtimeReserveFixture, "3000000", "1000000", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	reserves, err = provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes, err = reserves["exit-nl"].RequiredBytes(now.Unix()); err != nil || bytes != 10_000_000 {
		t.Fatalf("replacement report and reserve floor: %d, %v", bytes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider(ctx); err == nil {
		t.Fatal("cancelled operation must not authorize admission")
	}
}

func TestRuntimeWhiteListReserveFileRejectsAmbiguousOrInvalidReports(t *testing.T) {
	measurement := `{"exit_id":"exit-nl","measured_p999_bytes_per_second":3000000,"measured_at_unix":2000000,"valid_until_unix":2000020}`
	for name, body := range map[string]string{
		"unknown schema":     strings.Replace(runtimeReserveFixture, `"schema_version":1`, `"schema_version":2`, 1),
		"wrong unit":         strings.Replace(runtimeReserveFixture, "BYTES_PER_SECOND", "BITS_PER_SECOND", 1),
		"wrong basis":        strings.Replace(runtimeReserveFixture, "UPLINK_PLUS_DOWNLINK", "DOWNLINK_ONLY", 1),
		"zero rate":          strings.Replace(runtimeReserveFixture, "3000000", "0", 1),
		"overflow":           strings.Replace(runtimeReserveFixture, "3000000", "18446744073709551615", 1),
		"future measurement": strings.Replace(runtimeReserveFixture, "2000000", "2000001", 1),
		"duplicate exit":     strings.Replace(runtimeReserveFixture, measurement, measurement+","+measurement, 1),
		"unknown field":      strings.Replace(runtimeReserveFixture, `"schema_version":1`, `"schema_version":1,"default_rate":42`, 1),
		"trailing document":  runtimeReserveFixture + `{}`,
		"empty":              `{}`,
		"oversize":           strings.Repeat(" ", runtimeWhiteListReserveLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reserve.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			provider := runtimeWhiteListReserveFile(path, func() time.Time { return time.Unix(2_000_000, 0) })
			if reserves, err := provider(context.Background()); err == nil || len(reserves) != 0 {
				t.Fatal("invalid report must fail closed without partial measurements")
			}
		})
	}
}

func TestRuntimeWhiteListReserveConfigurationIsOptionalAndDoesNotEnablePublication(t *testing.T) {
	environment := completeRQLiteEnvironment()
	config, err := readRQLiteRuntimeConfig(mapGetenv(environment))
	if err != nil || runtimeWhiteListReserveFile(config.WhiteListReserveFile, time.Now) != nil {
		t.Fatal("absent report must leave the admission provider disabled")
	}
	environment["MAESTRO_WHITELIST_RESERVE_FILE"] = " /run/maestro/whitelist-reserve.json "
	config, err = readRQLiteRuntimeConfig(mapGetenv(environment))
	if err != nil || config.WhiteListReserveFile != "/run/maestro/whitelist-reserve.json" {
		t.Fatal("measurement report path was not forwarded to rqlite runtime")
	}
}
