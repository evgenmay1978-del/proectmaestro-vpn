# MaestroVPN HA PKI, inert service templates and offline node plan

> Status: focused implementation plan. Repository-only. PRODUCTION NO-GO.

## Goal

Continue the accepted HA program after immutable artifact commit
`f577c67ad229fe89278430d35a3ec65f6ce454e5` with the smallest honest
pre-deployment slice:

1. verify public X.509 material and trust-role separation offline;
2. add inert least-privilege rqlite and panel service templates;
3. produce a deterministic redacted `deploy-node plan`;
4. prove the slice in GitHub at the exact pushed SHA.

This plan has no `apply` path and authorizes no deployment.

## Authority and preserved boundaries

- The sole branch remains `codex/yandex-cdn-whitelist-task3-sync`.
- Android/TV production baseline remains `1.0.157`; no APK, tag, signing,
  release or OTA change is in scope.
- Ordinary VPN and every production process remain unchanged.
- OLCRTC and WDTT remain frozen.
- No SSH, live inventory, PKI issuance, secret access, rqlite bootstrap/join,
  import, firewall, nginx, DNS, bot, billing or customer mutation is allowed.
- `maestro-agent.service` is excluded because `backend/cmd/maestro-agent`
  does not exist.
- Runner enrollment, deploy workflow, probes, nginx/firewall templates, live
  smoke tests and any apply implementation are later reviewed slices.
- Manifest values remain `release_readiness: "NO_GO"` and
  `deployment_authorized: false`.
- Existing unrelated dirty files and `normalize.patch` are never staged.

## Architecture

`pki-verify.py` consumes a private offline directory containing only a
canonical public profile manifest, CA certificates and leaf certificates. It
uses fixed local OpenSSL argv without a shell and emits canonical redacted JSON
evidence. It never accepts or reads a private key and never uses a network.

The systemd files are inert repository templates. They have no `[Install]`,
create nothing, enable nothing, use dedicated identities and fixed paths, and
cannot bootstrap or join a cluster.

`deploy-node plan` imports the existing build-manifest verifier and the pure PKI
evidence validator in process. It consumes precomputed canonical PKI evidence;
it never invokes OpenSSL. It also consumes explicit offline node inventory,
exact GitHub artifact identity and exact templates. It emits one canonical JSON
plan with `authorized: false`, `release_readiness: "NO_GO"`, public digests,
a desired file/mode map and unresolved blockers. It emits and executes no
installation, service, firewall or network command.

## Fixed interfaces

### PKI input and evidence

Input schema `maestro-ha-pki-profile-v1` has exact keys `schema`,
`release_readiness`, `deployment_authorized`, `evaluation_time`,
`minimum_remaining_seconds` and `trust_domains`. Readiness is fixed to `NO_GO`
and authorization to `false`.

Each trust domain has stable `name`, one relative `ca_certificate`, and one or
more certificate entries. A certificate entry has stable `role`, relative
`certificate`, purpose (`server` or `client`), and exact sorted DNS, IP, URI SAN
and EKU OID lists.

Required trust domains are `rqlite-http`, `rqlite-raft`, `dispatcher`,
`bot-gateway`, `lease-verifier`, `node-status` and `github-probe`. CA and leaf
fingerprints cannot cross trust domains; a leaf cannot serve multiple roles.

Paths are relative single-component names below a pinned root. Absolute,
nested, traversal, symlink, hardlink, non-regular, unexpected and oversized
members fail. Duplicate/non-canonical JSON and every PEM private-key marker
fail.

Output schema `maestro-ha-pki-evidence-v1` contains only the profile digest,
evaluation time, `NO_GO/false`, fixed role names, public CA/leaf fingerprints,
not-after timestamps and sorted blockers. It excludes subjects, SAN values,
paths, OpenSSL stderr, certificate bodies and private material. Errors use fixed
codes without user values.

### Offline node inventory and plan

Input schema `maestro-ha-node-inventory-v1` contains one node ID (`s2`, `s3` or
`s4`), role `rqlite-voter`, target `linux/amd64`, logical address identifiers,
exact template selection and expected immutable artifact identity. It contains
no password, token, key, customer datum or production endpoint literal.

Output schema `maestro-ha-deploy-plan-v1` binds node/inventory digest, artifact
commit/run/archive/member identity, PKI evidence digest, exact template digests
and a sorted fixed destination/owner/group/mode map. It is always
`NO_GO/false` and lists every hard blocker.

`plan` is the only command. `apply`, aliases, unknown commands and extra
positionals fail before reading artifacts.

## Task 1: Focused plan checkpoint

**File**

- Create:
  `docs/superpowers/plans/2026-08-30-maestrovpn-ha-pki-service-offline-plan.md`.

**Steps**

1. Record and independently review this focused scope.
2. Regenerate the redacted baseline.
3. Run docs tests/validator.
4. Commit/push canonical branch.
5. Require the exact-SHA GitHub docs job GREEN.

Commit: `docs(ha): plan offline node deployment checks`.

## Task 2: Offline PKI verifier, RED first

**Files**

- Create: `ops/ha/pki_verify.py`
- Create: `ops/ha/pki-verify.py`
- Create: `ops/ha/tests/test_pki_verify.py`
- Create: `deploy/ha/pki-profile.json.example`

**RED**

Reject duplicate/non-canonical JSON, unknown keys, missing/extra trust domains,
role reuse, unsafe paths/links, unexpected members, private-key markers, wrong
CA/signature/purpose/SAN/EKU, invalid lifetime, forbidden CA/leaf reuse,
OpenSSL failure/malformed output and sensitive output.

Tests generate synthetic private material only below a fresh temp directory,
never print it and delete it. Pure policy tests run on Windows; real OpenSSL
integration is mandatory in Linux CI.

```text
python -m unittest ops.ha.tests.test_pki_verify -v
```

**GREEN**

Use Python standard library plus fixed OpenSSL argv, `shell=False`, bounded
timeouts/I/O and descriptor checks. Emit canonical JSON or a fixed error code.
No network module or private-key option.

```text
python -m unittest ops.ha.tests.test_pki_verify -v
python -m py_compile ops/ha/pki_verify.py ops/ha/pki-verify.py
```

Commit: `feat(ha): add offline PKI evidence verifier`.

## Task 3: Inert service templates, RED first

**Files**

- Create: `deploy/ha/rqlited@.service`
- Create: `deploy/ha/rqlite-s2.env.example`
- Create: `deploy/ha/rqlite-s3.env.example`
- Create: `deploy/ha/rqlite-s4.env.example`
- Create: `deploy/ha/maestro-panel.service`
- Create: `deploy/ha/maestro-panel.env.example`
- Create: `ops/ha/tests/test_service_templates.py`

The rqlite template targets pinned `10.1.0`. Exact flags are verified against
official v10.1.0 source/help and proved in Linux CI. It uses stable node ID,
`-fk`, separate HTTP/Raft TLS, fixed listen/advertise values, fixed state path,
no wildcard, no join/bootstrap automation and no `[Install]`.

The panel template launches only the immutable panel path, listens on loopback,
sets `MAESTRO_CONTROL_PLANE=rqlite`, and requires exactly three unique HTTPS
endpoints plus CA, cert, key and key-bundle paths matching existing Go runtime
configuration. It has no OLCRTC/WDTT setting.

Both units require dedicated identity, `UMask=0077`, bounded restart/timeouts,
`NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectSystem=strict`,
protected kernel/control-group/clock/hostname, empty capabilities, bounded
address families and explicit read/write paths. They contain no shell command.

**RED**

Fail on missing hardening, `[Install]`, wildcard/shell/arbitrary flag blob,
secret literal, unsafe writable path, missing TLS separation/`-fk`, unstable
node IDs, panel non-loopback/legacy/wrong endpoint count, OLCRTC/WDTT reference
or v10.1.0 help mismatch.

**GREEN**

```text
python -m unittest ops.ha.tests.test_service_templates -v
```

Linux CI builds an isolated verification root and runs `systemd-analyze verify`
without installing or starting anything.

Commit: `build(ha): add inert rqlite and panel templates`.

## Task 4: Offline deploy-node plan, RED first

**Files**

- Create: `ops/ha/deploy_node.py`
- Create: `ops/ha/deploy-node.sh`
- Create: `ops/ha/tests/test_deploy_node.py`

**RED**

Prove exact `plan` only; early reject `apply`/unknown/extra args; strict
inventory schema/node/role/arch; existing artifact identity/member checks; PKI
identity/freshness checks; exact template membership/digests; fixed safe
destinations/owners/groups/modes; deterministic canonical redacted
`NO_GO/false`; and zero process/socket/DNS/HTTP/SSH/filesystem mutation seams.

Output has no install, copy, chmod, systemctl, firewall, bootstrap, join,
secret, raw address or private-path command.

**GREEN**

The shell file is a strict syntax-checked wrapper. Python imports
`build_manifest` and only the pure `pki_verify.validate_evidence` path in
process. Planning cannot invoke OpenSSL. Roots/members are bounded and
rechecked. Destinations come only from a fixed allowlist.

```text
python -m unittest ops.ha.tests.test_deploy_node -v
bash -n ops/ha/deploy-node.sh
```

Commit: `feat(ha): add offline node deployment plan`.

## Task 5: Documentation and exact-SHA CI

**Files**

- Modify: `deploy/ha/README.md`
- Modify: `ops/ha/README.md`
- Modify: `.github/workflows/ha-build.yml`
- Modify: `ops/ha/build_workflow_policy.py`
- Modify: `ops/ha/tests/test_build_workflow_policy.py`

The workflow keeps self-policy immediately after checkout, then runs focused
PKI/template/planner tests, wrapper syntax and Linux `systemd-analyze verify`.
Synthetic certificates exist only in `RUNNER_TEMP`, are cleaned and never
uploaded. Permissions remain read-only. Artifact upload stays the existing
exact two-member panel bundle on non-PR events only.

```text
python -m unittest ops.ha.tests.test_build_manifest \
  ops.ha.tests.test_build_workflow_policy \
  ops.ha.tests.test_pki_verify \
  ops.ha.tests.test_service_templates \
  ops.ha.tests.test_deploy_node -v
python ops/ha/build_workflow_policy.py
python -m py_compile ops/ha/build_manifest.py \
  ops/ha/build_workflow_policy.py ops/ha/pki_verify.py \
  ops/ha/pki-verify.py ops/ha/deploy_node.py
bash -n ops/ha/deploy-node.sh
git diff --check
```

Require independent review with zero Critical/Important inside this scope. Push
only canonical branch and wait for every affected exact-SHA check. Classify
unrelated failures; never rerun blindly.

Commit: `build(ha): verify offline node deployment plan`.

## Task 6: Evidence checkpoint

After exact-SHA jobs are GREEN:

1. run the planner on synthetic offline input and the reviewed artifact without
   executing its binary;
2. prove deterministic output and hash it;
3. record source SHA, run/job IDs, review verdict and redacted plan digest in
   `CONTEXT_HANDOFF.md`;
4. regenerate baseline and run docs validation;
5. commit/push and prove local/tracking/GitHub refs equal.

Re-state `PRODUCTION NO-GO`. Remaining blockers include S3 identity, S4 network,
east-west proof, authenticated/versioned empty-cluster restore, artifact
attestation/rulesets, real PKI issuance, rqlite bootstrap/join, target runtime
smoke, owner-approved shadow, canary and cutover.

Commit: `docs: record offline HA node plan evidence`.

## Completion criteria

The slice completes only when every file/contract exists, local focused checks
pass, Linux OpenSSL/systemd checks are GREEN at exact SHA, independent review is
clean, exact evidence is recorded, refs match and no production/customer state
changed.

It still does not authorize deployment. The next slice is real PKI issuance
plus reviewed bootstrap/join, followed by a separately approved non-public
shadow window.
