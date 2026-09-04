# Commercial white-list staged rollout runbook

Status: executable gate contract; production mutation `NO_GO` until every
precondition below is evidenced for the exact node and exact package

This is the smallest approved production path: S4, then S2, then S3, then the
current owner-authoritative S1. A failure or unknown result stops the sequence;
no node is skipped and no parallel rollout is allowed.

## Non-negotiable boundaries

- Preserve all ordinary VPN, x-ui/3x-ui, child Xray, Hysteria, nginx, Caddy,
  panel, bot, and customer state. Never edit, replace, restart, or migrate the
  production x-ui/3x-ui units, their Xray processes, ports, or config
  directories.
- S4's existing private canary and `maestro-xray-cdn.service` on
  `18081/18082` remain active and unchanged throughout the rollout.
- Only isolated commercial sidecar/agent files and units from one reviewed
  immutable package may be staged. S4 may use only the currently clear
  `18084/18443` additions; all four candidate ports must be rechecked on the
  other nodes immediately before their change.
- Customer CDN/LTE publication defaults `OFF`. A route becomes visible only
  after a confirmed GB purchase or explicit admin enable and the full
  fail-closed publication verdict. Ordinary renewal does not enable CDN/LTE.
- Real charges, production OTA, release publication, signing, and final
  customer-traffic cutover remain separate stop gates.
- Production Android `1.0.157` supports only the unchanged ordinary bare
  subscription. It is an ordinary import/refresh/fallback baseline, not a
  CDN-transport client. `1.0.158-task7-test` is a CI artifact only and must not
  be promoted to fill that gap.
- OLCRTC and WDTT remain frozen.

## Gate 0: exact repository and artifact

Run the existing repository gates from a clean checkout of the candidate exact
SHA. `TASK15_RELEASE_DIR` must be an absolute protected candidate-release
directory and `TASK15_EVIDENCE_TRUST` must be an absolute protected regular
evidence-trust file for that same candidate. The operator supplies both values
from the protected change sheet; they must never be pasted into Git, chat, or
this runbook. Missing, relative, wrong-type, unreadable, or mismatched inputs
are `STOP`.

Linux guard check: execute this block by itself immediately before the wrapper
action, with no intervening command or action:

```bash
python ops/maestro-repetition-guard.py check \
  --action task15_s4_candidate_release_linux \
  --family release_wrapper_linux
```

Inspect both results before proceeding: the check must exit `0` **and** its
first output token must be the literal `ALLOW` or `ALLOW_CORRECTED`. Blank
output, any other token (including `BLOCKED`), or a nonzero exit is `STOP`; do
not paste or execute the wrapper block. Only after both conditions are visibly
satisfied may the operator execute this separate block:

```bash
bash -ceu '
  : "${TASK15_RELEASE_DIR:?STOP: protected release directory is required}"
  : "${TASK15_EVIDENCE_TRUST:?STOP: protected evidence trust file is required}"
  [[ "$TASK15_RELEASE_DIR" == /* && -d "$TASK15_RELEASE_DIR" ]]
  [[ "$TASK15_EVIDENCE_TRUST" == /* && -f "$TASK15_EVIDENCE_TRUST" && -r "$TASK15_EVIDENCE_TRUST" ]]
  exec bash ops/validate-yandex-cdn-release.sh \
    --release-dir "$TASK15_RELEASE_DIR" \
    --evidence-trust "$TASK15_EVIDENCE_TRUST"
'
```

PowerShell guard check: execute this block by itself immediately before the
wrapper action, with no intervening command or action:

```powershell
python ops/maestro-repetition-guard.py check `
  --action task15_s4_candidate_release_windows `
  --family release_wrapper_windows
```

Inspect both results before proceeding: the check must leave `$LASTEXITCODE`
equal to `0` **and** its first output token must be the literal `ALLOW` or
`ALLOW_CORRECTED`. Blank output, any other token (including `BLOCKED`), or a
nonzero exit is `STOP`; do not paste or execute the wrapper block. Only after
both conditions are visibly satisfied may the operator execute this separate
block:

```powershell
& {
    if ([string]::IsNullOrWhiteSpace($env:TASK15_RELEASE_DIR) -or
        [string]::IsNullOrWhiteSpace($env:TASK15_EVIDENCE_TRUST) -or
        -not [IO.Path]::IsPathRooted($env:TASK15_RELEASE_DIR) -or
        -not [IO.Path]::IsPathRooted($env:TASK15_EVIDENCE_TRUST) -or
        -not (Test-Path -LiteralPath $env:TASK15_RELEASE_DIR -PathType Container) -or
        -not (Test-Path -LiteralPath $env:TASK15_EVIDENCE_TRUST -PathType Leaf)) {
        throw 'STOP: protected candidate release/evidence inputs are absent or invalid'
    }
    & .\ops\validate-yandex-cdn-release.ps1 `
      --release-dir $env:TASK15_RELEASE_DIR `
      --evidence-trust $env:TASK15_EVIDENCE_TRUST
    if ($LASTEXITCODE -ne 0) { throw 'STOP: candidate release validation failed' }
}
```

The example action IDs bind the first target S4. For S2, S3, and current S1,
replace only the node segment with the exact target label and repeat the entire
lifecycle; an S4 ALLOW or result cannot authorize another node.

After the Linux example returns zero, close it before moving on:

```text
python ops/maestro-repetition-guard.py success --action task15_s4_candidate_release_linux --family release_wrapper_linux --evidence-code release_validation_green
```

If it returns nonzero, times out, or is unknown, the next executable command is
instead:

```text
python ops/maestro-repetition-guard.py fail --action task15_s4_candidate_release_linux --family release_wrapper_linux --reason-code release_validation_failed
```

Use the same mutually exclusive `success`/`fail` closure with action
`task15_s4_candidate_release_windows` and family `release_wrapper_windows` for
the PowerShell example. A failure then requires diagnosis, matching `correct`,
and a fresh corrected `check` before one retry.

Run `python scripts/validate_yandex_cdn_docs.py` and
`python -m unittest scripts.tests.test_yandex_cdn_docs` as two additional,
separately guarded actions. Every `check` is node/action-specific and sits
immediately before exactly one semantic action. After that action, record
matching `success` only when its evidence is green. On any nonzero, timeout, or
unknown result, the next executable command must be matching `fail`; diagnose,
record `correct` with a different family, and obtain a new corrected `check`
before one retry. A generic or detached ALLOW must never authorize a later
action or multiple commands.

Heavy/race/vet/Android work runs only in GitHub Actions. Record the exact
commit, independent review verdict, every required run ID, immutable artifact
name, member list, byte size, SHA-256, and signer/attestation identity in the
protected change sheet. Do not build or substitute a production binary on the
host.

The present repository has an inert systemd template at
`deploy/maestro-xray-cdn-agent.service` and the sidecar agent implementation at
`sidecar-agent/cmd/maestro-xray-cdn-agent`. It does not provide an authorized
production `apply` command. Until a reviewed immutable per-node package binds
those bytes, config digest, node certificate, firewall rules, backup, restore,
and rollback sheet, the next gate must return `STOP`; operators must not invent
an ad-hoc installer.

## Gate 1: per-node preflight

Repeat this gate immediately before each node. Save only redacted evidence.

1. Resolve the exact host through its owner-authoritative pin. For S1, reject
   the stale local alias that points to the deleted old host.
2. Exercise the current provider console/recovery path. A login page, CAPTCHA,
   or unauthorized VNC URL is not console proof.
3. Re-run the bounded read-only inventory: unit fragment/ExecStart/working
   directory/environment-file metadata, active/enabled state, process-bound
   listeners, time synchronization, one default route, capacity, failed units,
   and aggregate firewall ownership. Do not dump environments or configs.
4. Compare ordinary units, executable paths, and listeners byte-for-byte with
   the accepted before-state. Any drift is `STOP`.
5. Recheck `18081/18082/18084/18443`. On S4, require the existing canary to own
   `18081/18082` and require `18084/18443` to be free. On every other node,
   require all four to be free before staging.
6. Capture the exact service/data backup using the verified service-specific
   backup procedure. Prove its integrity and run the exact restore command on
   an isolated restore target. A copied live SQLite main file, an unverified
   archive, or a command that has not restored successfully is `STOP`.
7. Verify the immutable artifact manifest against the staged bytes; bind its
   release ID and config digest. Verify the node certificate identity, trust
   chain, expiry, key ownership/mode, and controller name without printing
   private material.
8. Review the exact firewall delta and its saved prior ruleset. The agent
   preflight must converge only the repository-defined four allowed rules;
   broad source ranges or extra rules are `STOP`.
9. Rehearse the node-specific rollback sheet without touching production and
   prove that every referenced previous byte, path, unit, and ruleset exists.
10. Require an exclusive change lock, a fresh second management session, an
    independent observer, and a monotonic timer ready to measure rollback.

No mutation begins while any item is false, unknown, stale, or evidenced only
for another node.

## Gate 2: isolated S4 canary

1. Record the ordinary before-state and query the existing repository canary
   CLI with its read-only `status` command. Resolve the CLI only from the
   reviewed immutable artifact; require it to be an executable regular file.
   Do not call `prepare`, `activate`, or `rollback` on the private canary.
2. Stage the commercial agent/package only in its isolated release paths. Do
   not alter `maestro-xray-cdn.service`, x-ui/3x-ui, `/usr/local/x-ui`, or any
   ordinary listener.
3. Start only the isolated commercial agent/unit named by the reviewed package.
   Prove exact release/config digests, active/enabled state, one process, its
   expected listeners, loopback health, and a fresh exact receipt.
4. With one test route identity, prove add, revoke, receipt recovery after an
   unknown outcome, and resume. Each result must converge the exact managed set
   while static/private-canary users remain unchanged.
5. Prove direct sidecar traffic, Yandex CDN traffic, literal-edge access with
   correct SNI/Host, per-user uplink plus downlink counters, selected-exit
   country label truth, TCP, UDP, DNS, idle recovery, and a network transition.
6. Prove ordinary service/process/listener/config identity and a real ordinary
   client baseline remain unchanged. Any regression triggers immediate
   rollback.
7. Execute one deliberate commercial rollback using
   `COMMERCIAL_ROLLBACK.md`, demonstrate restoration in under five minutes,
   and retain the private canary. Then re-apply the same immutable release and
   repeat the complete S4 validation. A different artifact is not a re-apply.

## Gate 3: S2, S3, then current S1

For each node, rerun Gates 1 and 2 with these additional stop conditions:

- S2: resolve ownership between systemd-networkd and networking before any
  firewall/network change; preserve Hysteria, nginx, Caddy, and the single
  `vpn_bot` process; capacity must remain safe.
- S3: resolve network ownership and the intended firewall owner while UFW is
  inactive; preserve x-ui and the child Xray below `/usr/local/x-ui`.
- Current S1: use only the owner-authoritative replacement identity; preserve
  maestro-panel, x-ui/Xray, Hysteria, and nginx.

Before an Origin joins the common CDN group, prove it holds the complete managed
desired set and that every exposed country identity routes to the same selected
exit regardless of serving Origin. Stop on any identity, route, release, port,
health, receipt, accounting, or rollback mismatch.

## Gate 4: subscriptions, clients, bots, channel, and refresh

Use one synthetic/test entitlement and sanitized result IDs; never record a
private subscription URL.

1. Prove production MaestroVPN `1.0.157` still imports and refreshes the exact
   ordinary bare subscription and uses the recoverable ordinary fallback. This
   is only an ordinary non-regression gate; `1.0.157` cannot satisfy any CDN,
   country-attribution, zero-balance, or top-up-resume row.
2. Run the commercial CDN canary separately in Happ, Incy, Karing, and at
   least one standards-compliant CDN-capable client. Test import, refresh, TCP,
   UDP, DNS, idle, network transition, country attribution, zero balance,
   top-up resume, and ordinary-only fallback.
3. Keep the MaestroVPN commercial-client row `NO_GO` until an explicitly
   authorized production MaestroVPN runtime, bound to a reviewed production
   version and artifact, supports the required CDN transport and passes the
   same commercial matrix. `1.0.158-task7-test` cannot satisfy this gate and
   must not be installed, promoted, released, signed, or published as OTA.
4. Prove users without a confirmed CDN purchase receive no CDN/LTE nodes after
   fresh render, cached render, bot delivery, automatic refresh, or last-known-
   good fallback.
5. Resolve both existing production bot identities and the existing customer
   channel from live protected configuration. Prove exactly one poller per bot,
   preserved pending updates, login-only payment comment, admin confirmation,
   one-tap Incy delivery, Happ fallback, subscription refresh, and channel
   notice without exposing secrets. Never confirm a real payment as a test.
6. Prove the `1.0.157` ordinary bare subscription refreshes byte-exact without
   CDN nodes, while entitled CDN-capable clients refresh automatically to the
   accepted commercial generation. After an authorized production MaestroVPN
   CDN runtime exists, prove its automatic refresh separately. A manual reissue,
   a newly created test-only subscription, or one successful client does not
   close this gate.
7. Record a recoverable last-known-good subscription path and prove it serves
   ordinary VPN when the commercial path is unavailable.

## Gate 5: 48-hour shadow accounting

For a continuous 48 hours compare Xray uplink plus downlink counters, immutable
usage intervals, ledger debits, customer balance projection, and Yandex CDN
bytes/request counts. The window restarts after a process reset, missing sample,
counter epoch ambiguity, ledger mismatch, unexplained request cost, or rollout
change. No customer is charged during this shadow window.

## Gate 6: temporary subscription cleanup

The private test subscription is a rollback canary and stays active throughout
the fleet rollout and 48-hour observation. Automatic deletion is eligible only
when all of the following are `GREEN` for the same accepted generation:

- S4, deliberate S4 rollback, S4 re-apply, S2, S3, and current S1;
- byte-exact automatic refresh of the ordinary `1.0.157` bare subscription and
  commercial refresh in Happ, Incy, Karing, and a standards-compatible client;
- an explicitly authorized production MaestroVPN CDN-capable runtime and its
  complete commercial client/refresh matrix;
- both bots, customer channel, panel login, default-OFF enforcement, and
  last-known-good recovery;
- 48-hour accounting/cost comparison and a recoverable cleanup record.

The cleanup transaction must re-read every predicate immediately before delete
and use an idempotent exact test-subscription identity. Any false, unknown, or
changed predicate retains the subscription. Failure after the delete request is
an unknown outcome and must be resolved by read-before-write, never a blind
second delete. Until this automation and its live receipt are proven, cleanup
status remains `RETAIN`.

Until the explicitly authorized production MaestroVPN runtime supports and
passes the CDN transport matrix, commercial customer cutover remains `NO_GO`
and the private test subscription must remain `RETAIN`, even when every
third-party-client canary is green.

Completion of this runbook does not authorize final customer traffic cutover,
real charging, or production OTA.
