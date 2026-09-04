# Commercial white-list rollback

Status: production rollback contract; not yet proven under five minutes

Rollback is scoped to the isolated commercial CDN sidecar/agent and its managed
`wl:` identities. It must not edit, replace, restart, or restore production
x-ui/3x-ui, child Xray, Hysteria, nginx, Caddy, maestro-panel, bot state,
ordinary subscriptions, or their configuration directories.

## Entry conditions

Before any rollout mutation, the protected node change sheet must contain the
exact previous isolated release pointer, artifact/config digests, unit state,
listener ownership, saved firewall ruleset, service/data backup, independently
proved restore command, console path, operator/observer, and expected ordinary
before-state. If any item is missing, rollback is not executable and rollout is
`NO_GO`.

The rollback clock starts when the abort decision is recorded and ends only
after managed publication is closed, isolated state is restored, and the
ordinary baseline is revalidated. The hard acceptance limit is less than five
minutes.

## Immediate rollback triggers

- ordinary VPN, panel, bot, reverse proxy, route, DNS, or listener regression;
- wrong release/config digest, node identity, certificate, or active Origin set;
- extra/missing firewall rule or unresolved network ownership;
- incomplete desired set, wrong selected exit, false country label, stale or
  missing receipt;
- counter, ledger, balance, CDN byte/request, or attribution mismatch;
- failed add, revoke, resume, refresh, last-known-good, or default-OFF check;
- unknown mutation outcome that cannot be resolved read-only;
- inability to finish rollback within the remaining five-minute budget.

## Ordered rollback procedure

1. Stop new commercial changes and record the monotonic start time. Do not stop
   ordinary services.
2. Fail closed at the control plane: disable publication for the affected test
   entitlement/generation through the canonical idempotent admin path. Resolve
   an unknown response with the existing exact-action receipt lookup; never
   edit the database or retry blindly.
3. Remove the affected Origin from new commercial publication only through the
   reviewed external CDN change sheet. Confirm the remaining Origins still have
   the complete managed set. Never alter DNS or a shared Origin by guess.
4. Stop only the isolated commercial agent/unit introduced by the accepted
   package. On S4, do not stop or roll back the existing
   `maestro-xray-cdn.service`; its private canary and `18081/18082` ownership
   must remain unchanged.
5. Restore the saved pre-change isolated firewall ruleset through its separately
   reviewed external change sheet. Restore the commercial release pointer and
   state only with the accepted bundle's
   `maestro-xray-cdn-commercial-operator rollback` command and the same profile
   used for apply. The operator re-verifies its manifest before mutation. Do not
   improvise a restore, invoke the private-canary rollback, or use a backup from
   another node.
6. Start only the prior isolated commercial unit if it was active before the
   change. Prove its exact digest, process count, listener ownership, and health.
   If no prior commercial unit existed, prove the candidate ports returned to
   their accepted before-state.
7. Revalidate ordinary unit fragment/ExecStart identity, active/enabled state,
   executable path, listeners, one default route, time synchronization, failed
   units, and real ordinary client traffic against the before-state.
8. Query the existing repository canary CLI with read-only `status` on S4 and
   prove the private canary is still active. The canary CLI's `rollback` command
   removes that canary and is prohibited in this commercial rollback.
9. Record the stop time, elapsed duration, sanitized evidence digests, exact
   reason, restored release, and final verdict. If elapsed time is five minutes
   or more, the Task 15 rollback gate is failed even when service later recovers.

## Commercial data and customer recovery

- Never delete ledger, usage interval, order, payment, balance, or purchase
  history. Correct a committed commercial error only with the canonical
  compensating transaction after independent review.
- Keep customer CDN/LTE publication `OFF` until readiness returns. Ordinary VPN
  remains available through the recoverable last-known-good subscription path.
- Do not issue or confirm a real payment during rollback validation.
- Preserve pending Telegram updates and exactly one poller per bot. A bot
  rollback must not duplicate payment confirmations or delivery messages.
- Keep the private test subscription while any fleet, client, bot, channel,
  refresh, accounting, or recovery gate is not green.

## Post-cleanup recovery

Deletion of the temporary subscription is permitted only after the complete
Task 15 all-green predicate and a recoverable last-known-good record. If a fault
appears after cleanup, restore through that reviewed protected record; never
reconstruct credentials from logs, chat, Git, or customer data. An unknown
cleanup outcome is resolved read-before-write.

Production Android remains `1.0.157`; `1.0.158-task7-test` remains test-only.
OLCRTC and WDTT remain frozen. Final customer cutover, real charging, and OTA
are outside this rollback authorization.
