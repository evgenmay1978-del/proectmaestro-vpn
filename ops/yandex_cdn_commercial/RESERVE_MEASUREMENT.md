# Bounded isolated reserve measurement

`backend/cmd/maestro-reserve-measure` reads the existing authenticated
`GET /v1/usage` endpoint through `sidecaragentclient.LookupUsage`. It never sends
desired POST, provisions identities, resets counters, generates traffic, changes
billing, installs a report, or enables publication/collector. Use a separate
`isolated-*` synthetic entitlement; the owner's private white-list subscription,
its credential and static canary are outside this tool's scope.

Build this command in the existing exact-SHA GitHub validation environment. Run
the resulting verified binary on the Linux controller with existing mTLS leaf
files and an explicitly prepared synthetic managed route:

```text
maestro-reserve-measure --config /PROTECTED/measurement-config.json --output /PROTECTED/NEW-measurement-directory
```

Both paths must be absolute. Output must not already exist. Input and output
ancestors must be real directories without group/other write permissions (a
world-writable temporary directory is intentionally unsuitable). Config and key
files must be regular files with no group/other permissions; CA/cert files must
not be group/other writable. New output directory is0700, files0600. Keep the
configuration and its directory under the trusted operator's ownership.

The following describes the exact config shape. Strings in angle brackets are
placeholders, not runnable credentials or observed bindings; all values must
come from the actual isolated canary and its protected operational record.

```json
{
  "schema_version": 1,
  "entitlement_id": "isolated-<own-test-account>",
  "workload_description": "<actual workload, directions, concurrent devices/connections and target>",
  "fleet_description": "<actual active fleet and why every Origin for each exit is included>",
  "validity_reason": "<reason this measured workload and fleet remain applicable until the explicit expiry>",
  "sample_count": 1000,
  "interval_millis": 1000,
  "request_timeout_millis": 200,
  "max_clock_skew_millis": 10,
  "valid_until_unix": 0,
  "origins": [{
    "base_url": "https://<observed-agent-address>",
    "server_name": "<verified-agent-TLS-name>",
    "ca_file": "/PROTECTED/existing-agent-ca.crt",
    "cert_file": "/PROTECTED/existing-controller.crt",
    "key_file": "/PROTECTED/existing-controller.key",
    "expected_binding": {
      "action_key": "<observed-action-key>",
      "origin_id": "<observed-origin-id>",
      "release_id": "<observed-release-id>",
      "xray_process_boot_id": "<observed-64-lowercase-hex-digest>",
      "config_digest": "<observed-64-lowercase-hex-digest>",
      "desired_generation": 1,
      "managed_user_set_digest": "<observed-64-lowercase-hex-digest>"
    }
  }],
  "exits": [{"exit_id": "<measured-exit-id>", "origin_ids": ["<observed-origin-id>"]}]
}
```

The example is intentionally invalid: expiry0 and placeholders are rejected.
No throughput, expiry or measurement configuration is generated from defaults.
The chosen timing numbers illustrate the contract; they do not establish the
adequacy of a production workload or the statistical confidence of a tail bound.
Specify1000–10000 windows, interval250–2000ms, total duration≤1hour, request
timeout1ms–one quarter of the interval, clock tolerance0–100ms,1–8 Origins and
1–8 exits. Expiry must be later than the complete run and within24hours of start;
this upper limit does not justify a24hour validity claim. Describe its actual
justification. Duplicate/unmapped Origins and duplicate exits are rejected.

Before starting, root verifies saved strict SSH trust, exact live layout and
bindings, baseline/rollback, healthy existing receipt refresh, the account's
managed membership and complete active-Origin mapping. Choose and record a
bounded workload that actually covers the proposed account's simultaneous
connections/devices. Warm its counters with separate controlled synthetic
traffic before the baseline; then keep the intended workload running throughout
measurement. The runner cannot discover omitted active Origins or attest that
an operator's workload/fleet/validity description is true. Changing that scope
invalidates the intended use of the report even if its JSON still parses.

Every round queries all configured Origins concurrently. Rounds have equal
scheduled spacing; late starts, timeouts, missing pairs, changed bindings,
non-increasing server sample times, detected clock drift, decreasing counters
or arithmetic overflow fail the whole series. There are no retries, skipped
windows, zero substitutions or inferred resets. Per-exit samples sum one
account's UP+DOWN deltas across every mapped Origin. To avoid understating rate
because of request jitter, divide that sum by the shortest proven interval
between the prior request's end and current request's start, rounding upward.
That interval is a lower bound on actual counter-read separation. These are
conservative estimates for equally scheduled windows, not perfectly simultaneous
physical counter reads. Raw evidence records actual times and the denominator.

The report uses nearest rank `ceil(0.999 * N)` across the complete rate series.
An exit whose p999 is zero, or any configured account/Origin route carrying no
positive traffic over the run, produces no report.1000 samples provide an
empirical quantile; they do not prove statistical confidence, independent
samples, account capacity or a paid launch SLO.

Outputs are `metadata.json`, bounded `samples.jsonl` (≤64MiB), `summary.json`
with its SHA256 and explicit unreviewed/non-production status, and `report.json`
in the exact [provider schema](RESERVE_REPORT.md). Evidence retains only the
configured synthetic account's counters and public runtime bindings, not other
managed users, URLs, certificate paths or keys. No report is published until the
whole series succeeds; failure leaves partial raw evidence for diagnosis.
Do not resume a partial series or overwrite its directory. Stop the separately
owned traffic processes through their own cleanup procedure after measurement.

Review the raw evidence and scope before installing any report using the
existing provider contract. This command's cadence does not prove the production
collector≤2s or revoke≤5s; same-boot reset, period/outage, rollout and customer
publication gates remain separate. A measured S4-only scope cannot authorize
another exit or a subsequently expanded fleet.
