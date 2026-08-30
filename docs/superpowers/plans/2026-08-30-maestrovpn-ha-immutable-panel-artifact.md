# MaestroVPN HA Immutable Panel Artifact Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a repository-only, immutable Linux/amd64 `maestro-panel` artifact whose full Git SHA, build inputs, SHA-256 and size are bound by a strict canonical manifest and verified in GitHub CI.

**Architecture:** A Python standard-library validator owns the canonical manifest and verifies the exact two-member artifact directory without executing the binary. A separate workflow-policy validator pressure-tests `.github/workflows/ha-build.yml`; the workflow runs existing Go and isolated-rqlite gates, performs two byte-identical builds, creates/verifies the manifest, and uploads only the binary and manifest. The deliverable remains artifact-only and never authorizes server deployment.

**Tech Stack:** Python 3 standard library, `unittest`, Go 1.25.0, GitHub Actions on `ubuntu-24.04`, SHA-256, canonical JSON.

## Global Constraints

- Work only on `codex/yandex-cdn-whitelist-task3-sync`; preserve every unrelated dirty file and never stage `task-4-report.md` or `normalize.patch`.
- Status remains `PRODUCTION NO-GO`; do not access servers or mutate deploy, systemd, firewall, DNS, TLS, rqlite, bot, payment, customer, VPN, OTA, OLCRTC or WDTT state.
- Android/TV production baseline remains `1.0.157`; OLCRTC and WDTT remain frozen.
- The workflow receives no environment, secret, self-hosted runner or production address and cannot invoke `ssh`, `scp`, remote `curl`, `systemctl`, `nft`, `iptables` or deployment scripts. Loopback-only `curl` inside the audited `ops/ha/test-ci-rqlite-cluster.sh` harness is allowed solely for its isolated temporary cluster.
- Build only `backend/cmd/maestro-panel` with `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `-trimpath`, `-buildvcs=true`, and full `GITHUB_SHA` injected into `backend/internal/api.BuildCommit`.
- Upload exactly `maestro-panel` and `manifest.json`; never upload `.env`, credentials, keys, snapshots, databases, configs, logs or customer data.
- A successful build means only `release_readiness=NO_GO` and `deployment_authorized=false`.
- Use official pins verified on 2026-08-30: checkout `11bd71901bbe5b1630ceea73d27597364c9af683` (`v4.2.2`), setup-go `40f1582b2485089dde7abd97c1529aa768e1baff` (`v5.6.0`), upload-artifact `ea165f8d65b6e75b540449e92b4886f43607fa02` (`v4.6.2`). The `11d596...` checkout value in the parent plan is a typo and must not be used.

---

### Task 1: Strict canonical build manifest

**Files:**

- Create: `ops/ha/build_manifest.py`
- Create: `ops/ha/tests/test_build_manifest.py`

**Interfaces:**

- Produces: `build_manifest(...) -> dict[str, object]`, `verify_manifest(...) -> dict[str, object]`, and canonical JSON CLI commands `create` and `verify`.
- `build_manifest` consumes an artifact root containing exactly one regular, single-link, non-symlink, executable Linux/amd64 ELF named `maestro-panel`.
- `verify_manifest` consumes an artifact root containing exactly `maestro-panel` and `manifest.json`; it rechecks the regular-file, link, size, ELF, digest and manifest contracts but does not require the executable bit because GitHub artifact transport does not preserve Unix mode bits. A later deploy slice must restore mode explicitly with `install -m 0755` after verification.

- [ ] **Step 1: Write the failing manifest tests**

Create `ops/ha/tests/test_build_manifest.py`. Each test names the production break it catches and uses literal expected values. Start with this real round-trip behavior:

```python
from ops.ha.build_manifest import ManifestError, build_manifest, verify_manifest

FULL_SHA = "1" * 40

def write_amd64_elf(path):
    header = bytearray(64)
    header[:4] = b"\x7fELF"
    header[4] = 2
    header[5] = 1
    header[18:20] = b"\x3e\x00"
    path.write_bytes(bytes(header) + b"maestro-panel-fixture")
    path.chmod(0o755)

class BuildManifestTests(unittest.TestCase):
    def test_round_trip_binds_literal_digest_size_and_no_go_status(self):
        write_amd64_elf(self.root / "maestro-panel")
        manifest = build_manifest(
            self.root,
            repository="evgenmay1978-del/proectmaestro-vpn",
            ref="refs/heads/codex/yandex-cdn-whitelist-task3-sync",
            commit_sha=FULL_SHA,
            workflow_run_id=123,
            workflow_run_attempt=2,
            go_version="go1.25.0",
        )
        self.assertEqual(manifest["release_readiness"], "NO_GO")
        self.assertIs(manifest["deployment_authorized"], False)
        self.assertEqual(manifest["artifacts"][0]["path"], "maestro-panel")
```

Add separate tests that reject short/uppercase/wrong commit, unsafe repository/ref, zero run values, unsupported Go version, missing/extra/duplicate fields, duplicate JSON keys, non-canonical JSON, missing/extra artifact members, absolute/traversal paths, symlink, hardlink, directory/device/FIFO, zero/oversize binary, a non-executable source binary during `create`, wrong ELF class/endian/machine, digest mismatch and size mismatch. Prove separately that `verify` accepts the same regular ELF after transport strips its executable bit. Test `create` refuses an existing `manifest.json` and `verify` emits only a bounded redacted result.

- [ ] **Step 2: Run the tests and verify RED**

```powershell
python -m unittest ops.ha.tests.test_build_manifest -v
```

Expected: FAIL because `ops.ha.build_manifest` does not exist.

- [ ] **Step 3: Implement the minimal manifest validator**

Create `ops/ha/build_manifest.py` with these public signatures:

```python
class ManifestError(ValueError):
    pass

def build_manifest(
    artifact_root: Path | str,
    *,
    repository: str,
    ref: str,
    commit_sha: str,
    workflow_run_id: int,
    workflow_run_attempt: int,
    go_version: str,
) -> dict[str, object]: ...

def verify_manifest(
    artifact_root: Path | str,
    manifest_path: Path | str,
    *,
    expected_repository: str,
    expected_ref: str,
    expected_commit_sha: str,
    expected_workflow_run_id: int,
    expected_workflow_run_attempt: int,
) -> dict[str, object]: ...
```

The exact manifest shape is:

```json
{"artifacts":[{"arch":"amd64","name":"maestro-panel","os":"linux","path":"maestro-panel","sha256":"<64 lowercase hex>","size_bytes":123}],"commit_sha":"<40 lowercase hex>","deployment_authorized":false,"go_version":"go1.25.0","ref":"refs/heads/codex/yandex-cdn-whitelist-task3-sync","release_readiness":"NO_GO","repository":"evgenmay1978-del/proectmaestro-vpn","schema":"maestro-ha-build-manifest-v1","workflow_run_attempt":2,"workflow_run_id":123}
```

Serialize with `json.dumps(..., sort_keys=True, separators=(",", ":")) + "\n"`. Decode JSON with an `object_pairs_hook` that rejects duplicate keys. Open artifacts through a pinned descriptor using `O_RDONLY|O_CLOEXEC|O_NOFOLLOW` where available, validate with `fstat`, hash that descriptor, require `nlink == 1`, size `1..268435456`, and check the ELF header without executing it. `create` additionally requires an executable source mode; `verify` deliberately does not. Error text is a fixed code such as `build-manifest:invalid-artifact`, never a user value or path.

CLI:

```text
python ops/ha/build_manifest.py create --artifact-root DIR --output DIR/manifest.json --repository REPO --ref REF --commit-sha SHA --workflow-run-id N --workflow-run-attempt N --go-version go1.25.0
python ops/ha/build_manifest.py verify --artifact-root DIR --manifest DIR/manifest.json --expected-repository REPO --expected-ref REF --expected-commit-sha SHA --expected-workflow-run-id N --expected-workflow-run-attempt N
```

`create` prints nothing and atomically creates mode `0600` output. `verify` prints exactly one canonical JSON object containing `schema`, `artifact_sha256`, `artifact_size_bytes`, `release_readiness` and `deployment_authorized`.

- [ ] **Step 4: Run the manifest tests and verify GREEN**

```powershell
python -m unittest ops.ha.tests.test_build_manifest -v
python -m py_compile ops/ha/build_manifest.py ops/ha/tests/test_build_manifest.py
```

Expected: all tests PASS with no warnings or leaked fixture values.

- [ ] **Step 5: Commit Task 1**

```powershell
git add -- ops/ha/build_manifest.py ops/ha/tests/test_build_manifest.py
git commit -m "build(ha): add strict panel artifact manifest"
```

---

### Task 2: Executable workflow safety policy

**Files:**

- Create: `ops/ha/build_workflow_policy.py`
- Create: `ops/ha/tests/test_build_workflow_policy.py`

**Interfaces:**

- Produces: `validate_workflow(text: str) -> None` and a CLI that validates `.github/workflows/ha-build.yml`.
- Consumes: active YAML source and checks structural security behavior without third-party YAML dependencies.

- [ ] **Step 1: Write mutation tests first**

Write a complete safe synthetic workflow literal in the test file, call the real validator, then mutate one security property per test. Each mutation must be accepted by YAML but rejected by policy. Cover: short/unapproved action pin, `permissions: write-all`, job permission escalation, missing/broad timeout, `cancel-in-progress: true`, `pull_request_target`, `environment`, `secrets.`, self-hosted runner, production IP, second job, extra artifact upload, upload on pull requests, wildcard artifact path, hidden files, overwrite, forbidden remote/deploy command, missing cleanup, and absent self-policy test.

Representative behavior:

```python
def test_rejects_upload_that_can_run_on_pull_request(self):
    unsafe = VALID_WORKFLOW.replace(
        "if: github.event_name != 'pull_request'",
        "if: always()",
        1,
    )
    with self.assertRaisesRegex(WorkflowPolicyError, "upload-boundary"):
        validate_workflow(unsafe)
```

- [ ] **Step 2: Run the policy tests and verify RED**

```powershell
python -m unittest ops.ha.tests.test_build_workflow_policy -v
```

Expected: FAIL because `ops.ha.build_workflow_policy` does not exist.

- [ ] **Step 3: Implement the minimal policy validator**

Create `ops/ha/build_workflow_policy.py` with:

```python
class WorkflowPolicyError(ValueError):
    pass

def validate_workflow(text: str) -> None: ...

def main() -> int: ...
```

Reuse the structural-line approach in `ops/ha/test-dr-workflow-policy.py`: ignore comments and block-scalar command payload when parsing YAML keys; require exact top-level `name/on/permissions/concurrency/jobs`, one `build-panel-artifact` job, `ubuntu-24.04`, bounded timeout, `contents: read`, and full 40-hex action pins. Allow exactly checkout/setup-go/upload-artifact at the verified SHAs. Inspect real step boundaries so strings inside comments or `run` blocks cannot masquerade as keys. Scan active commands and fail on deploy, remote-network and service-management verbs. Allow only the exact isolated-rqlite harness command as a bounded loopback-network exception. Require upload path to resolve to exactly `dist/maestro-panel` and `dist/manifest.json`, with `if-no-files-found: error`, `compression-level: 0`, `overwrite: false`, `include-hidden-files: false`, and the non-PR condition.

The CLI reads only `.github/workflows/ha-build.yml`, prints `HA build workflow policy passed` on success, and otherwise prints only a fixed redacted error code.

- [ ] **Step 4: Run mutation tests and verify GREEN**

```powershell
python -m unittest ops.ha.tests.test_build_workflow_policy -v
python -m py_compile ops/ha/build_workflow_policy.py ops/ha/tests/test_build_workflow_policy.py
```

Expected: every safe fixture passes and every single-property mutation fails for the intended reason.

- [ ] **Step 5: Commit Task 2**

```powershell
git add -- ops/ha/build_workflow_policy.py ops/ha/tests/test_build_workflow_policy.py
git commit -m "build(ha): enforce panel build workflow policy"
```

---

### Task 3: Artifact-only GitHub workflow and inert deployment contract

**Files:**

- Create: `.github/workflows/ha-build.yml`
- Create: `deploy/ha/README.md`
- Modify: `ops/ha/tests/test_build_workflow_policy.py`
- Modify: `ops/ha/README.md`

**Interfaces:**

- Produces: artifact `maestro-panel-<full SHA>` containing exactly `maestro-panel` and `manifest.json`.
- Consumes: Task 1 manifest CLI, Task 2 workflow policy CLI, existing backend tests and isolated rqlite harness.

- [ ] **Step 1: Add the failing repository-workflow integration test**

Add a test that reads `ROOT/.github/workflows/ha-build.yml` and passes it to `validate_workflow`. Do not skip when the file is absent.

- [ ] **Step 2: Run and verify RED**

```powershell
python -m unittest ops.ha.tests.test_build_workflow_policy -v
```

Expected: FAIL because `.github/workflows/ha-build.yml` is absent.

- [ ] **Step 3: Create the artifact-only workflow**

Create one `build-panel-artifact` job with `timeout-minutes: 45`, `permissions: contents: read`, `runs-on: ubuntu-24.04`, and no job environment. Trigger only `push` and `pull_request` for `codex/yandex-cdn-whitelist-task3-sync` plus manual `workflow_dispatch`. It must:

1. checkout and setup Go with the verified full-SHA action pins, using `go-version-file: backend/go.mod` and `cache-dependency-path: backend/go.sum`;
2. run the manifest/policy tests and its own policy CLI;
3. run backend unit, race and vet with `MAESTRO_S2_PASS` and `MAESTRO_HY2_PASS` unset;
4. run `bash ops/ha/test-ci-rqlite-cluster.sh`, start the isolated cluster, run `go test -count=1 -tags=rqlite_integration ./...`, and stop it under `if: always()`;
5. build twice with identical flags and compare bytes;
6. use `go version -m` to require `GOOS=linux`, `GOARCH=amd64`, exact `vcs.revision=${GITHUB_SHA}` and `vcs.modified=false`;
7. create and verify `dist/manifest.json` through Task 1;
8. list `dist` through a bounded Python assertion that permits exactly two regular non-symlink files;
9. upload on push/workflow-dispatch only, never on pull request.

Use:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -buildvcs=true \
  -ldflags "-s -w -X github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api.BuildCommit=${GITHUB_SHA}" \
  -o ../dist-a/maestro-panel ./cmd/maestro-panel
```

Build the second copy into `dist-b`, compare with `cmp`, then move one verified copy to `dist/maestro-panel`. Use `umask 077`, fixed `LC_ALL=C`, and no shell tracing.

- [ ] **Step 4: Document the inert boundary**

Create `deploy/ha/README.md` and update `ops/ha/README.md`. State plainly: artifact-only, repository-only, `PRODUCTION NO-GO`; no deploy helper, users, directories, services, rqlite bootstrap/join, TLS, nginx, firewall, agents, bot pollers, import, DNS or cutover are implemented or authorized. Record manifest fields, exact two-member artifact, verification command, GitHub transport mode behavior, and the next separately reviewed slice: PKI/service templates plus offline `deploy-node plan` only.

- [ ] **Step 5: Run focused verification**

```powershell
python -m unittest ops.ha.tests.test_build_manifest ops.ha.tests.test_build_workflow_policy -v
python ops/ha/build_workflow_policy.py
python -m py_compile ops/ha/build_manifest.py ops/ha/build_workflow_policy.py ops/ha/tests/test_build_manifest.py ops/ha/tests/test_build_workflow_policy.py
git diff --check
```

Expected: PASS. Local Go/race/rqlite work is intentionally left to GitHub because the owner requested low local resource use.

- [ ] **Step 6: Commit Task 3**

```powershell
git add -- .github/workflows/ha-build.yml deploy/ha/README.md ops/ha/README.md ops/ha/tests/test_build_workflow_policy.py
git commit -m "build(ha): publish immutable panel artifact"
```

---

### Task 4: Independent review and exact-SHA GitHub evidence

**Files:**

- Modify: `CONTEXT_HANDOFF.md` only after exact-SHA CI is GREEN.

**Interfaces:**

- Produces: reviewed code SHA, workflow run/job IDs, artifact name, binary SHA-256/size and manifest SHA-256 recorded without URLs containing private tokens.
- Consumes: GitHub run for the exact pushed SHA and independent code/security review.

- [ ] **Step 1: Review before push**

Require independent Critical/Important review of manifest path safety, duplicate-key rejection, descriptor pinning, ELF validation, workflow permissions/pins, non-PR upload boundary, exact artifact membership and secret-free output. Resolve every Critical or Important finding before push.

- [ ] **Step 2: Push and wait for exact-SHA CI**

Push only the canonical branch. Wait for `HA immutable panel artifact` plus existing HA control-plane and DR workflows at the exact code SHA. Do not rerun unrelated failures blindly; classify and fix each distinct root cause through the repetition guard.

- [ ] **Step 3: Verify the downloaded artifact offline**

Download the exact run artifact into an ephemeral directory, verify the GitHub artifact metadata/run SHA, require exactly two members, run Task 1 `verify` directly against the transport-restored regular file, compare the binary digest/size to the manifest and record only redacted hashes/IDs. Do not execute the downloaded binary, do not treat its transported mode as deployment-ready, and do not deploy it.

- [ ] **Step 4: Record evidence and commit**

Append the exact SHA, run/job IDs, artifact name, binary digest/size, manifest digest and review verdict to `CONTEXT_HANDOFF.md`. Re-state `PRODUCTION NO-GO` and list remaining blockers: authoritative S3 identity, S4 network repair, east-west matrix, authenticated empty-cluster restore, PKI/service templates, offline deploy plan, shadow change-window approval, canary and cutover.

```powershell
git add -- CONTEXT_HANDOFF.md
git commit -m "docs: record immutable panel artifact evidence"
git push origin codex/yandex-cdn-whitelist-task3-sync
```

Verify local and remote branch SHA are byte-exact equal.

## Self-review result

- Spec coverage: the plan binds source, build flags, workflow run, binary digest/size and exact artifact membership; it explicitly excludes deployment.
- Placeholder scan: every implementation and verification step names concrete files, interfaces, commands and expected outcomes.
- Type consistency: Task 3 consumes the exact Task 1 CLI and Task 2 policy API; Task 4 verifies the same manifest contract.
- Transport consistency: source mode is enforced before upload; downloaded mode is not treated as a signed property and a later installer must restore it explicitly.
- Network consistency: production and remote network commands are forbidden; only the audited loopback rqlite harness is allowed.
- Safety boundary: no step needs production credentials, server access or a production mutation.
