# HA panel artifact boundary

## PRODUCTION NO-GO

This slice is artifact-only and repository-only. It builds and verifies one
Linux/amd64 `maestro-panel` binary in GitHub Actions. It does not deploy,
install, start, restart, import, migrate or cut over anything.

The existing backup systemd templates in this directory are unrelated to the
panel artifact. Their presence does not authorize panel deployment or make this
slice production-ready.

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

## Explicitly outside this slice

This panel artifact slice implements and authorizes none of the following:

- a panel deploy helper or production filesystem changes;
- production users, groups, directories, services or timers;
- rqlite bootstrap, join, restore or business-data import;
- PKI, TLS certificates, nginx, firewall or open ports;
- server access, agents, bot pollers or Telegram changes;
- DNS, traffic switching, customer migration or cutover.

Because repository-local validation can only run after the pinned checkout
action, GitHub branch/ruleset protection and CODEOWNERS review for
`.github/workflows/**` remain a required external control. No workflow file
approval may be inferred from the self-policy alone.

The next separately reviewed slice is limited to PKI/service templates and an
offline `deploy-node plan`. It must remain non-mutating until separately
approved and verified.
