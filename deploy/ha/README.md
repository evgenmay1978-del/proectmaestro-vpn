# HA offline artifact and node-plan boundary

## PRODUCTION NO-GO

This slice is offline-only and repository-only. It builds and verifies one
Linux/amd64 `maestro-panel` binary in GitHub Actions, verifies public PKI
material, defines six inert service/environment templates and emits a
deterministic node plan. It does not deploy, install, render, enable, start,
restart, import, migrate, bootstrap, join or cut over anything.

The existing backup systemd templates in this directory are unrelated to the
six node-plan templates listed below. Neither template set authorizes panel
deployment or makes this slice production-ready.

The workflow artifact is named `maestro-panel-<full commit SHA>` and contains
exactly two regular, non-symlink files:

- `maestro-panel`
- `manifest.json`

`manifest.json` uses schema `maestro-ha-build-manifest-v1`. Its exact top-level
fields are:

- `schema`, `repository`, `ref`, `commit_sha`, `go_version`;
- `workflow_run_id`, `workflow_run_attempt`;
- `release_readiness` fixed to `NO_GO`;
- `deployment_authorized` fixed to `false`;
- `artifacts`, containing exactly one entry for `maestro-panel` with `name`,
  `path`, `os`, `arch`, `sha256` and `size_bytes`.

The entry is fixed to `os: linux`, `arch: amd64`, and the exact binary digest
and byte size. The workflow also proves Go `1.25.0`, the full Git commit, and an
unmodified VCS build before upload.

## Offline verification

Download the artifact for the reviewed GitHub run and extract it into a fresh,
private directory. Do not execute the downloaded binary. From a checkout of the
same reviewed repository commit, run:

```bash
python ops/ha/build_manifest.py verify \
  --artifact-root /absolute/path/to/extracted-artifact \
  --manifest /absolute/path/to/extracted-artifact/manifest.json \
  --expected-repository evgenmay1978-del/proectmaestro-vpn \
  --expected-ref '<exact ref of the reviewed run>' \
  --expected-commit-sha '<reviewed 40-hex commit>' \
  --expected-workflow-run-id '<reviewed positive run id>' \
  --expected-workflow-run-attempt '<reviewed positive attempt>'
```

For a branch push the ref is `refs/heads/<branch>`. A manual dispatch can use a
different selected ref; copy the exact ref from the reviewed run and manifest.

The verifier rejects unexpected bundle members, identity mismatches, malformed
or non-canonical manifests, non-ELF/wrong-architecture binaries, size changes
and SHA-256 changes. A successful result still reports `NO_GO` and
`deployment_authorized: false`.

GitHub artifact transport does not preserve the source executable mode; a
downloaded regular file can therefore arrive non-executable. Transport mode is
not build-provenance evidence. The verifier accepts that transport-only mode
loss while still checking the bytes and manifest. Do not run `chmod`, install
or execute the binary in this slice. A later reviewed installer must restore
the intended mode under its own policy.

The upload step runs only for branch pushes and manual workflow dispatches. It
never uploads from a pull-request event. All workflow permissions are read-only,
and the workflow receives no production environment or repository secrets.

The artifact source is pinned independently to commit
`f577c67ad229fe89278430d35a3ec65f6ce454e5`. Inventory and transport evidence
cannot replace that trust anchor. A successfully verified artifact remains
unauthorized for execution or installation.

## Offline PKI verification

`ops/ha/pki-verify.py` accepts one private offline directory containing only the
canonical `pki-profile.json`, seven public CA certificates and exactly 37 public
leaf certificates. It accepts no private-key option, rejects every PEM private
key marker, never uses a network and invokes fixed OpenSSL argv with no shell.
Run it on Linux with OpenSSL `>=3.0.0,<4.0.0`:

```bash
python ops/ha/pki-verify.py \
  --root /absolute/private/offline/pki-root \
  > /absolute/private/offline/pki-evidence.json
```

The input schema is `maestro-ha-pki-profile-v1`. Its exact top-level fields are
`schema`, `release_readiness`, `deployment_authorized`, `evaluation_time`,
`minimum_remaining_seconds` and `trust_domains`; readiness is fixed to `NO_GO`
and authorization to `false`. The seven required trust domains are
`rqlite-http`, `rqlite-raft`, `dispatcher`, `bot-gateway`, `lease-verifier`,
`node-status` and `github-probe`. Their role matrix is a closed allowlist of
exactly 37 leaves.

The rqlite server identities are fixed per node:

| Role | Exact DNS SAN |
| --- | --- |
| `s2-http-server` | `s2-rqlite-http.internal` |
| `s3-http-server` | `s3-rqlite-http.internal` |
| `s4-http-server` | `s4-rqlite-http.internal` |
| `s2-raft-peer` | `s2-rqlite-raft.internal` |
| `s3-raft-peer` | `s3-rqlite-raft.internal` |
| `s4-raft-peer` | `s4-rqlite-raft.internal` |

These roles have empty IP and URI SAN lists. HTTP server leaves belong only to
the `rqlite-http` trust domain and have `serverAuth`. Every unique Raft leaf
belongs only to `rqlite-raft` and has both `serverAuth` and `clientAuth`.
Default outbound Raft verification binds the presented certificate to the
peer's advertised hostname. Inbound membership remains CA-scoped; this slice
does not claim per-peer runtime fingerprint pinning.

The canonical redacted output schema is `maestro-ha-pki-evidence-v1`. Its exact
top-level fields are `schema`, `profile_sha256`, `evaluation_time`,
`openssl_version`, `release_readiness`, `deployment_authorized`, `blockers` and
`trust_domains`. A trust-domain entry contains only `name`, CA certificate
fingerprint, CA SPKI digest, CA serial, CA not-after and `certificates`; a leaf
entry contains only `role`, certificate fingerprint, SPKI digest, serial and
not-after. Evidence excludes subjects, SAN values, paths, certificate bodies,
OpenSSL stderr and all private material. Failures expose only fixed error codes.

## Inert service and environment templates

The planner accepts exactly these six templates:

- `maestro-panel.env.example`;
- `maestro-panel.service`;
- `rqlite-s2.env.example`;
- `rqlite-s3.env.example`;
- `rqlite-s4.env.example`;
- `rqlited@.service`.

Their independent template-source anchor is
`8289ce78be8dcb2c00829d6b9781d4b52a18cb73`; each byte size and SHA-256 is
fixed in the planner. The units have no `[Install]`, bootstrap, join or shell
command. They use dedicated identities, fixed paths and systemd hardening, but
remain repository data: they create, render, copy, enable and start nothing.
They contain no OLCRTC or WDTT setting. `maestro-agent.service` is deliberately
absent because `backend/cmd/maestro-agent` does not exist.

## Offline deploy-node plan

`plan` is the only supported command. It requires:

- a canonical `maestro-ha-node-inventory-v1` for exactly one of `s2`, `s3` or
  `s4`, role `rqlite-voter` and target `linux/amd64`;
- separately reviewed `maestro-ha-artifact-transport-evidence-v1`;
- the extracted two-member artifact root and its `manifest.json`;
- canonical `maestro-ha-pki-evidence-v1`;
- a dedicated directory containing exactly the six trusted templates above.

Do not pass the repository `deploy/ha` directory as `--templates-root`: it also
contains this README and unrelated backup files. Use a separately prepared,
bounded six-member template directory and run:

```bash
ops/ha/deploy-node.sh plan \
  --inventory /absolute/private/offline/inventory.json \
  --transport-evidence /absolute/private/offline/transport-evidence.json \
  --artifact-root /absolute/private/offline/extracted-artifact \
  --manifest /absolute/private/offline/extracted-artifact/manifest.json \
  --pki-evidence /absolute/private/offline/pki-evidence.json \
  --templates-root /absolute/private/offline/exact-six-template-root
```

The canonical stdout schema is `maestro-ha-deploy-plan-v1`. It always contains
`authorized: false` and `release_readiness: "NO_GO"`, plus redacted input and
artifact digests, exact template digests, a fixed desired
destination/owner/group/mode map and unresolved blockers. The two independent
source anchors above cannot be overridden by inventory. The planner invokes no
OpenSSL and emits or executes no install, copy, chmod, systemctl, firewall,
network, SSH, bootstrap or join command. `apply`, aliases, unknown commands and
extra positional arguments fail before artifacts are read; there is no apply
path.

## Explicitly outside this slice

This repository slice implements and authorizes none of the following:

- PKI issuance, private-key provisioning or production certificate access;
- template rendering, a mutating deploy helper or production filesystem changes;
- production users, groups, directories, service activation or timers;
- rqlite bootstrap, join, restore or business-data import;
- nginx, firewall, DNS, traffic switching or open ports;
- server access, agents, bot pollers or Telegram changes;
- target runtime smoke, customer migration, shadow, canary or cutover.

Because repository-local validation can only run after the pinned checkout
action, GitHub branch/ruleset protection and CODEOWNERS review for
`.github/workflows/**` remain a required external control. No workflow file
approval may be inferred from the self-policy alone.

OLCRTC and WDTT remain frozen. Production remains unauthorized while readiness
is `NO_GO` and deployment authorization is `false`. Real PKI issuance,
rqlite bootstrap/join, template rendering, target smoke, shadow deployment,
canary and cutover require later independent review, exact-SHA evidence and
separate explicit owner approval.
