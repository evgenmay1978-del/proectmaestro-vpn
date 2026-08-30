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
`certificate`, purpose (`server`, `client` or `peer`), and exact sorted DNS,
IP, URI SAN and EKU OID lists.

Required trust domains are `rqlite-http`, `rqlite-raft`, `dispatcher`,
`bot-gateway`, `lease-verifier`, `node-status` and `github-probe`. rqlite
v10.1.0 exposes one `-node-ca-cert` trust store and one
`-node-cert`/`-node-key` pair for both incoming and outgoing inter-node TLS.
Therefore every `rqlite-raft` voter has one unique `peer` leaf with exactly
serverAuth and clientAuth EKUs. It cannot cross nodes, cross trust domains or be
reused for HTTP. All other leaves retain exactly one `server` or `client`
purpose. CA and leaf fingerprints cannot cross trust domains; a leaf cannot
serve more than its one declared stable role.

The role matrix is a closed allowlist of exactly 37 leaf roles. `serverAuth`
is OID
`1.3.6.1.5.5.7.3.1`; `clientAuth` is OID `1.3.6.1.5.5.7.3.2`.

| Trust domain | Exact roles | Purpose and exact EKU |
| --- | --- | --- |
| `rqlite-http` | `s2-http-server`, `s3-http-server`, `s4-http-server` | `server`; serverAuth |
| `rqlite-http` | `s2-panel-rqlite-client`, `s3-panel-rqlite-client`, `s4-panel-rqlite-client` | `client`; clientAuth |
| `rqlite-http` | `s2-backup-rqlite-client`, `s3-backup-rqlite-client`, `s4-backup-rqlite-client`, `importer-rqlite-client` | `client`; clientAuth |
| `rqlite-raft` | `s2-raft-peer`, `s3-raft-peer`, `s4-raft-peer` | `peer`; serverAuth and clientAuth |
| `dispatcher` | `s2-controlplane-dispatcher-client`, `s3-controlplane-dispatcher-client`, `s4-controlplane-dispatcher-client` | `client`; clientAuth |
| `bot-gateway` | `s2-bot-gateway-server`, `s3-bot-gateway-server`, `s4-bot-gateway-server` | `server`; serverAuth |
| `bot-gateway` | `s2-telegram-bot-primary-client`, `s3-telegram-bot-primary-client`, `s4-telegram-bot-primary-client` | `client`; clientAuth |
| `bot-gateway` | `s2-telegram-bot-secondary-client`, `s3-telegram-bot-secondary-client`, `s4-telegram-bot-secondary-client` | `client`; clientAuth |
| `lease-verifier` | `s2-lease-verifier-server`, `s3-lease-verifier-server`, `s4-lease-verifier-server` | `server`; serverAuth |
| `lease-verifier` | `s1-agent-lease-client`, `s2-agent-lease-client`, `s3-agent-lease-client`, `s4-agent-lease-client` | `client`; clientAuth |
| `node-status` | `s1-agent-status-server`, `s2-agent-status-server`, `s3-agent-status-server`, `s4-agent-status-server` | `server`; serverAuth |
| `github-probe` | `github-workflow-probe-client` | `client`; clientAuth |

The directional split is deliberate: an agent HTTPS endpoint presents its
`node-status` server leaf and verifies one of the per-node
`controlplane-dispatcher-client` leaves for apply/status calls. The normal
public panel certificate is outside this private profile, so `github-probe`
contains only the workflow client leaf. Telegram bots have no `rqlite-http`
role. S1 roles are future-issuance blockers only; they do not authorize an S1
voter, issuance or deployment. Every per-node role requires a distinct leaf
fingerprint even when SANs overlap.

Application identity SANs are immutable, not profile-selected. Each
`s2-controlplane-dispatcher-client`,
`s3-controlplane-dispatcher-client` and
`s4-controlplane-dispatcher-client` role has
`dns_sans: ["controlplane-dispatcher"]` exactly; this is the DNS SAN type read
by `backend/internal/applyagent/http.go`. Each per-node primary-bot role has
`dns_sans: ["bot-primary"]` exactly, and each per-node secondary-bot role has
`dns_sans: ["bot-secondary"]` exactly; separate leaves prevent cross-process
and cross-node reuse while the application still binds the stable Telegram bot
identity and credential version. `github-workflow-probe-client` has
`dns_sans: ["workflow-probe"]` exactly, matching the parent nginx/probe
contract. Every fixed identity role has empty `ip_sans` and `uri_sans`.
Missing, additional, renamed or wrong-type identity SANs fail even if the
profile requests them.

Paths are relative single-component names below a pinned root. Absolute,
nested, traversal, symlink, hardlink, non-regular, unexpected and oversized
members fail. Duplicate/non-canonical JSON and every PEM private-key marker
fail.

Each trust domain is a one-level chain: one self-signed root directly signs its
leaves and intermediates are forbidden. A root requires critical
`BasicConstraints=CA:TRUE,pathlen:0`, critical
`KeyUsage=keyCertSign,cRLSign`, Subject Key Identifier and matching Authority
Key Identifier. A leaf requires critical `BasicConstraints=CA:FALSE`, critical
`KeyUsage=digitalSignature`, Subject Key Identifier, Authority Key Identifier
matching its root, exact SANs and the exact non-`anyExtendedKeyUsage` EKU set
declared by its purpose. Unknown or duplicate critical extensions fail.

Accepted public keys are RSA 3072/4096 or EC P-256/P-384. Accepted certificate
signature algorithms are SHA-256/SHA-384 with RSA or ECDSA. Non-positive or
over-20-octet serials, weak/unknown keys or signatures, CA/leaf constraint
violations and certificates outside `notBefore <= evaluation_time < notAfter`
or below the minimum remaining lifetime fail.

Only OpenSSL `>=3.0.0,<4.0.0` is accepted; LibreSSL/BoringSSL and unknown
versions fail. Exact normalized `openssl version -v` output is recorded.
Every chain check uses fixed argv with `verify`, `-trusted`, `-no-CAfile`,
`-no-CApath`, `-no-CAstore`, `-x509_strict`, `-check_ss_sig`,
`-auth_level 2`, `-verify_depth 0`, `-attime <evaluation_time>` and exact
`-purpose sslserver` or `sslclient`. A `peer` leaf must pass both purposes.
Exact SAN, extension, key and signature policy is checked separately without
accepting OpenSSL's display text as unbounded evidence.

Output schema `maestro-ha-pki-evidence-v1` contains only the profile digest,
evaluation time, exact normalized OpenSSL version, `NO_GO/false`, fixed role
names, public CA/leaf fingerprints, not-after timestamps and sorted blockers.
It excludes subjects, SAN values, paths, OpenSSL stderr, certificate bodies and
private material. Errors use fixed codes without user values.

### Offline node inventory and plan

Input schema `maestro-ha-node-inventory-v1` contains one node ID (`s2`, `s3` or
`s4`), role `rqlite-voter`, target `linux/amd64`, logical address identifiers,
exact template selection and expected immutable artifact identity. It contains
no password, token, key, customer datum or production endpoint literal.

Output schema `maestro-ha-deploy-plan-v1` binds node/inventory digest, artifact
workflow run ID and attempt, artifact ID and exact name, head commit SHA/ref,
GitHub-reported archive SHA-256, binary and manifest SHA-256/size, PKI evidence
digest, exact template digests and a sorted fixed
destination/owner/group/mode map. It is always `NO_GO/false` and lists every
hard blocker.

Inventory artifact values are untrusted assertions, never authority. The
planner compares them only with separately supplied, bounded transport
evidence. Task 6 independently reads every GitHub identity field from GitHub;
those fields prove transport identity only and never authorize deployment.

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

Tasks 2 through 4 create explicitly unverified checkpoints. None is considered
complete or deployable until Task 5 proves the combined exact SHA on Linux,
including real OpenSSL integration, the panel runtime contract and
`systemd-analyze verify`.

## Task 2: Offline PKI verifier, RED first

**Files**

- Create: `ops/ha/pki_verify.py`
- Create: `ops/ha/pki-verify.py`
- Create: `ops/ha/tests/test_pki_verify.py`
- Create: `deploy/ha/pki-profile.json.example`

**RED**

Reject duplicate/non-canonical JSON, unknown keys, missing/extra trust domains,
missing/extra/renamed roles, wrong role-to-domain/purpose/EKU mapping, missing,
additional, renamed or wrong-type fixed identity SANs, role reuse, unsafe
paths/links, unexpected members, private-key markers and sensitive output.
Positive and negative synthetic chains cover root/leaf
BasicConstraints, pathlen/depth, KeyUsage, dual-purpose peer EKU, single-purpose
EKU, SAN exactness, unknown/duplicate critical extensions, public-key algorithm
and size/curve, signature algorithm, serial bounds, direct issuer/signature,
not-before/not-after, minimum remaining lifetime, OpenSSL version/range,
strict-purpose invocation, OpenSSL failure and malformed bounded output.

Tests generate synthetic private material only below a fresh temp directory,
never print it and delete it. Pure policy tests run on Windows. The Task 2
commit remains an unverified checkpoint until the same tests use real OpenSSL
in exact-SHA Linux CI.

```text
python -m unittest ops.ha.tests.test_pki_verify -v
```

**GREEN**

Use Python standard library plus the fixed OpenSSL 3.x argv/profile above,
`shell=False`, bounded timeouts/I/O and descriptor checks. Parse only bounded,
explicit command outputs. Emit canonical JSON or a fixed error code. No network
module or private-key option. The accepted OpenSSL version and every executed
purpose check are asserted in tests and the exact normalized version is
evidence.

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
- Create: `backend/cmd/maestro-panel/runtime_service_template_test.go`

The rqlite template targets pinned `10.1.0` and only the tagged official
interfaces: `-node-id`, `-fk`, `-http-addr`, `-http-adv-addr`,
`-http-ca-cert`, `-http-cert`, `-http-key`, `-http-verify-client`,
`-raft-addr`, `-raft-adv-addr`, `-node-ca-cert`, `-node-cert`,
`-node-key`, `-node-verify-client`, `-node-verify-server-name` and the
positional data directory. Tagged parser/source and generated help are checked
in Linux CI. `-node-no-verify`, invented `-raft-cert/-key/-ca` flags,
wildcards, join/bootstrap automation and `[Install]` are forbidden. HTTP and
Raft use separate trust domains; the one per-node Raft peer cert/key is
deliberately dual-purpose because v10.1.0 uses it for both directions.

The panel template launches only the immutable panel path and sets the real
source-backed `MAESTRO_LISTEN=127.0.0.1:8910` interface plus
`MAESTRO_CONTROL_PLANE=rqlite`. Its environment has exactly three unique HTTPS
endpoints plus CA, cert, key and key-bundle paths matching existing Go runtime
configuration. It has no OLCRTC/WDTT setting. A Go runtime contract test reads
the exact service/env templates, proves the rqlite factory is selected, the
runtime parser accepts all three endpoints, and `MAESTRO_LISTEN` parses to a
loopback-only address; template-text assertions alone are insufficient.

Both units require dedicated identity, `UMask=0077`, bounded restart/timeouts,
`NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectSystem=strict`,
protected kernel/control-group/clock/hostname, empty capabilities, bounded
address families and explicit read/write paths. They contain no shell command.

**RED**

Fail on missing hardening, `[Install]`, wildcard/shell/arbitrary flag blob,
secret literal, unsafe writable path, missing TLS separation/`-fk`, unstable
node IDs, non-dual-purpose Raft peer identity, absent hostname verification,
`-node-no-verify`, invented Raft TLS flags, panel non-loopback/legacy/wrong
endpoint count, runtime rejection, OLCRTC/WDTT reference or v10.1.0 tagged
source/help mismatch.

**GREEN**

```text
python -m unittest ops.ha.tests.test_service_templates -v
cd backend && go test ./cmd/maestro-panel \
  -run TestHAServiceTemplateRuntimeContract -count=1
```

Linux CI builds an isolated verification root, verifies the v10.1.0 tagged flag
surface, runs the exact panel runtime contract test and
`systemd-analyze verify` without installing or starting anything. The Task 3
commit remains an unverified checkpoint until this exact-SHA Linux proof is
GREEN.

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
PKI/template/planner tests, real OpenSSL 3.x positive/negative integration, the
panel runtime contract, pinned rqlite v10.1.0 flag proof, wrapper syntax and
Linux `systemd-analyze verify`. It records the exact normalized OpenSSL
version in test/evidence output. Synthetic certificates exist only in
`RUNNER_TEMP`, are cleaned and never uploaded. Permissions remain read-only.
Artifact upload stays the existing exact two-member panel bundle on non-PR
events only.

```text
python -m unittest ops.ha.tests.test_build_manifest \
  ops.ha.tests.test_build_workflow_policy \
  ops.ha.tests.test_pki_verify \
  ops.ha.tests.test_service_templates \
  ops.ha.tests.test_deploy_node -v
python ops/ha/build_workflow_policy.py
cd backend && go test ./cmd/maestro-panel \
  -run TestHAServiceTemplateRuntimeContract -count=1
cd ..
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

1. independently read from GitHub the workflow run ID/attempt, artifact
   ID/name, head SHA/ref, GitHub-reported archive SHA-256, exact member list and
   the binary/manifest SHA-256 and sizes; never trust inventory assertions;
2. run the planner on synthetic offline input and that bounded reviewed
   transport evidence without executing its binary;
3. prove deterministic output and hash it;
4. record source SHA, run/job IDs, artifact identity/digests, exact OpenSSL
   version, review verdict and redacted plan digest in `CONTEXT_HANDOFF.md`;
5. regenerate baseline and run docs validation;
6. commit/push and prove local/tracking/GitHub refs equal.

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
