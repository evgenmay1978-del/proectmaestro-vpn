# Commercial white-list canary evidence

Status: evidence template; current verdict `PRODUCTION NO_GO`

This file is the redacted Task 15 evidence index. It must never contain host
addresses, credentials, UUIDs, certificate/private-key material, customer
identifiers, payment data, bot tokens, private subscription URLs, or raw logs.
Protected raw evidence stays outside Git and is referenced only by UTC window,
test ID, exact release/config identity, and digest.

## Accepted repository baseline

| Evidence | Value | Scope |
| --- | --- | --- |
| Candidate checkpoint | `ac8c6bf160d1adcd08b9e9046c9577d47ce9cdcf` | Repository-only baseline before Task 15 docs |
| HA artifact workflow | Run `33848098013`, green | Repository/CI only |
| S4 package workflow | Run `33848098068`, green | Repository/CI only |
| Yandex CDN release workflow | Run `33848098064`, green | Repository/CI only |
| Production mutation | `NO_GO` | Missing per-host production gates |

The earlier owner-reported mobile white-list pass remains useful historical
evidence for the preserved private S4 canary, but it is not reused as current
Task 15 fleet, client-matrix, accounting, or production-readiness proof.

## Read-only inventory evidence

| Node | Identity/role | Ordinary baseline | Candidate ports | Read-only verdict | Mutation verdict |
| --- | --- | --- | --- | --- | --- |
| S4 | Existing VPN plus private CDN canary node | x-ui and Hysteria active | Existing canary owns `18081/18082`; `18084/18443` clear | `READ_ONLY PASS` | `NO_GO` |
| S2 | Multi-protocol/bot node | Hysteria, nginx, Caddy, `vpn_bot` active | All four clear | `READ_ONLY PASS`; network ownership unresolved | `NO_GO` |
| S3 | x-ui/VPN node | x-ui with protected child Xray active | All four clear | `READ_ONLY PASS`; network ownership unresolved | `NO_GO` |
| Current S1 | Replacement public control plane | maestro-panel, x-ui/Xray, Hysteria, nginx active | All four clear | `READ_ONLY PASS`; stale old alias rejected | `NO_GO` |

## Per-host preflight and rollout record

Fill one row only from a fresh protected evidence bundle. `UNKNOWN` and blank
are failures.

| Node/stage | Exact reviewed SHA and CI | Console | Fresh inventory/ports | Backup plus restore | Artifact/config digest | Node cert | Firewall plan | Apply | Functional canary | Ordinary baseline | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| S4 initial | Green repository baseline only | Not proven | Read-only snapshot only | Not proven | Not bound to host | Not proven | Not proven | Not run | Not run | Not revalidated after apply | `NO_GO` |
| S4 deliberate rollback under five minutes | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S4 same-release re-apply | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S2 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S3 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| Current S1 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |

For every completed stage record protected evidence digests for direct sidecar,
Yandex CDN, literal edge with correct SNI/Host, counter delta, add/revoke/resume,
unknown-receipt recovery, complete desired set, selected-exit country truth,
and ordinary baseline. Never paste the endpoint or identity.

## Client and delivery matrix

| Consumer/flow | Ordinary bare import/refresh/fallback | CDN transport and commercial cases | Current result |
| --- | --- | --- | --- |
| MaestroVPN `1.0.157` | Not run; must remain byte-exact and contain no CDN nodes | `NOT_SUPPORTED`; cannot prove CDN, country attribution, zero balance, or top-up resume | Ordinary non-regression `NO_GO`; not a commercial-client candidate |
| Explicitly authorized production MaestroVPN CDN runtime | Not defined | Runtime/version/artifact not authorized and CDN matrix not run | Commercial cutover `NO_GO` |
| Happ | Not run | Import, refresh, TCP/UDP/DNS, idle/transition, attribution, zero balance, top-up resume not run | `NO_GO` |
| Incy | Historical private-canary pass only | Current full commercial matrix not run | `NO_GO` |
| Karing | Not run | Full commercial matrix not run | `NO_GO` |
| Standards-compliant CDN-capable client | Not run | Full commercial matrix not run | `NO_GO` |

`1.0.158-task7-test` may appear only as a CI test artifact. It cannot satisfy
the authorized production MaestroVPN CDN-runtime row and must not be installed,
promoted, released, signed, or published as OTA.

## Customer delivery and automatic refresh

| Required proof | Current result |
| --- | --- |
| Both live bot identities resolved; exactly one poller each; pending updates preserved | Not run |
| Customer channel identity resolved and redacted delivery proved | Not run |
| Panel login shows the same customer-bound subscription | Not run |
| Incy one-tap and Happ fallback work without exposing a private URL | Not run |
| MaestroVPN `1.0.157` ordinary bare subscriptions refresh byte-exact with no CDN nodes | Not run |
| Entitled Happ/Incy/Karing/standards-compatible subscriptions refresh to one accepted commercial generation | Not run |
| Explicitly authorized production MaestroVPN CDN runtime refreshes and passes the commercial matrix | Runtime not authorized; `NO_GO` |
| Customers without a confirmed CDN purchase never see CDN/LTE nodes, including cached and last-known-good paths | Not run |
| Recoverable last-known-good ordinary subscription path | Not proven |

## 48-hour accounting and request-cost observation

The window has not started. Record periodic sanitized aggregates without
customer identities.

| UTC interval | Xray uplink plus downlink | Immutable usage applied | Ledger debit | Balance projection | Yandex CDN bytes/requests | Expected cost | Mismatch |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Pending | — | — | — | — | — | — | Window not started |

Any missing interval, reset ambiguity, duplicate application, negative
projection, unexplained CDN request/byte difference, or fleet change restarts
the continuous 48-hour window. No real customer charge is authorized by this
observation.

## Temporary subscription cleanup gate

Current action: `RETAIN`.

Deletion becomes eligible only after every fleet row, deliberate rollback and
same-release re-apply, third-party client row, bot/channel/panel flow, ordinary
`1.0.157` byte-exact refresh, default-OFF proof, last-known-good recovery, and
48-hour accounting row is green for the same generation. It additionally
requires an explicitly authorized production MaestroVPN CDN-capable runtime and
its complete commercial import/refresh/transport matrix. The deletion receipt
must prove an idempotent exact test identity and read-before-write resolution of
unknown outcome. Any missing runtime, mismatch, unknown, stale evidence, or
generation change retains the subscription.

## Final decision

Production staging: `NO_GO`.

Customer-live cutover: `NOT_AUTHORIZED`.

Real charges, release/signing/OTA, OLCRTC, and WDTT: unchanged and out of scope.
