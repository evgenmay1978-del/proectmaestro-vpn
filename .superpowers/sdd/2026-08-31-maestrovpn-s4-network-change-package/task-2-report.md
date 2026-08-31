# Task 2 — S4 change-package boundary report

## Scope

- Base: `f63242745f2d9fb6af6d25339bd5b91fe3d238db`
- Branch: `codex/yandex-cdn-whitelist-task3-sync`
- Round 5 changed only the Task 2 module, focused tests, and this report.
- No network, process, production, server, DNS, firewall, customer, or VPN action was added or invoked.

## Review fix round 5 — RED → GREEN

- RED: the new direct `run()` strict-time matrix used otherwise exact package
  argv with `+00:00`, fractional seconds, an impossible date, lowercase `z`,
  and missing seconds. On Windows all cases returned
  `s4-network-change-package:unsupported-platform` before trusted-UTC parsing,
  rather than the required redacted `input` error. The `read_inventory` and
  `publish_change_package` sentinels remained uncalled and no candidate output
  existed.
- GREEN: strict CLI UTC is parsed immediately after manual argv preflight and
  before platform/input work. Each invalid-time case now returns exit `3`, empty
  stdout, exactly `s4-network-change-package:input` on stderr, does not call
  either sentinel, and leaves no output.
- The post-link parent-directory-fsync fault regression now role-classifies at
  least two directory fsync calls and requires this order: failing publication
  fsync, invocation-owned final rollback unlink, cleanup/rollback fsync. It
  fails if the repeated cleanup fsync is omitted. This POSIX-only test is
  skipped on Windows and awaits the required Ubuntu exact-SHA CI gate.

## Implemented boundary

- Descriptor-pinned POSIX input: private owner/mode, regular single-link,
  no-symlink, bounded `16_384` byte read, before/open/after metadata checks,
  canonical parsing, and SHA-256 binding.
- POSIX-only private `0700` output directory plus `0600` same-directory temp,
  fsync, hard-link no-clobber publish, final inode/directory rechecks and
  invocation-owned rollback.
- Post-link fault handling derives an invocation-owned rollback candidate from
  the still-open descriptor even if `link` creates the final and then reports an
  error; temp unlink is rechecked against that descriptor immediately before
  removal.
- Non-POSIX fail-closed behavior before filesystem access; redacted manual CLI
  preflight; stable exits `0`, `2`, and `3`; no success stdout; a bootstrap-only
  wrapper.

## Local verification (Windows)

```text
python -m unittest ops.ha.tests.test_s4_network_change_package -v
Ran 56 tests ... OK (skipped=21)

python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4SecureInputTests -v
Ran 5 tests ... OK (skipped=5)

python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4SecureOutputTests -v
Ran 7 tests ... OK (skipped=7)

python -m py_compile ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py
exit 0

python ops/ha/s4-network-change-package.py --help
exit 0

git diff --check
exit 0
```

The 21 full-suite skips are POSIX descriptor/output cases. The local Windows
result is not POSIX validation and does not claim POSIX CI is green.

## Remaining gate

Push the exact Task 2 commit and require Ubuntu GitHub Actions to run the full
POSIX suite on that exact SHA. Repository work remains production NO-GO.
