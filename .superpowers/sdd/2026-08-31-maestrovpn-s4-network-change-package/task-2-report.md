# Task 2 — S4 change-package boundary report

## Scope

- Base: `5a2eed80e93a758341a05530a2420bcd728d0439`
- Branch: `codex/yandex-cdn-whitelist-task3-sync`
- Changed only the Task 2 module, tests, thin wrapper, and this report.
- No network, process, production, server, DNS, firewall, customer, or VPN action was added or invoked.

## RED-first evidence

Before implementation, `python -m unittest
ops.ha.tests.test_s4_network_change_package.S4BoundaryContractTests -v` failed
with the required public filesystem/CLI boundary functions missing. The failure
was local Windows-runnable and was not a skip.

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
Ran 34 tests ... OK (skipped=7)

python -m py_compile ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py
exit 0

python ops/ha/s4-network-change-package.py --help
exit 0

git diff --check
exit 0
```

The six skips are explicitly POSIX-only descriptor/output tests; they are not
RED evidence or a claim of POSIX validation. Exact-SHA Ubuntu CI is required
next and is the authority for these checks.

## Remaining gate

Push the exact Task 2 commit and require Ubuntu GitHub Actions to run the full
POSIX suite on that exact SHA. Repository work remains production NO-GO.
