# Commercial white-list canary evidence

Status: 2026-09-05 checkpoint; b441 ACTIVE, six shipped checks true;
synthetic generations 2/3/4 PASS; full promotion remains NO_GO.

This file is the redacted Task 15 evidence index. It must never contain host
addresses, credentials, UUIDs, certificate/private-key material, customer
identifiers, payment data, bot tokens, private subscription URLs, or raw logs.
Protected raw evidence stays outside Git and is referenced only by UTC window,
test ID, exact release/config identity, and digest.

## Accepted repository baseline

| Evidence | Value | Scope |
| --- | --- | --- |
| Reviewed replacement source/package | `b4415daa90c95a38f9a7b9adea7642c66e63a420`; selected-exit readiness budget and exact TCP loopback health exception; scoped reviews PASS | Source/package identity, independent of later documentation HEAD |
| Replacement exact-SHA CI | Yandex `33928873964`: all six jobs success, including Android `101204799664`, refreshed 2026-09-05; immutable `33928874093`/job `101203186787` and network `33928874014` GREEN | Repository/CI only; no reruns |
| Installed b441 artifact | ID `9957942956`; `54141763` bytes; archive SHA-256 `7a74ff26f181c44456493958577005f938b417cd2eca501dab60376989736b7b`; manifest SHA-256 `a687657c43a20d77512a26ac73821c161513ddaebdb4dda6af466bde2855add5`; nine members verified | Direct S4 fetch/plan PASS; locked shipped apply EXIT 0; ACTIVE with six checks true |
| Retained previous package | `3603f11bbc35a4a9d708c41db1bc13f0d2907805`, artifact `9956162284`; exact runs `33923773188`, `33923773196`, `33923773238` terminal GREEN | Previous install PASS; generation 1 functional proof failed; old package/proof retained |
| Canonical ingress source | `aad63b52c74aedd2c568f0ed4a6a9f912e31e262`, workflow correction `26895992db384d8275c36720a622d96862505f69`; scoped review and isolated real nginx parser PASS | Included in b441 ancestry; no ingress installation or CDN switch |
| Failed immutable artifact | `dbbd950ab556b92b103cd51f5a4b2686acb74ef5`, artifact ID `9955259827`; exact fetch and plan passed, but real first apply failed on relay-CA directory traversal | Retained failed evidence; must not be reapplied or modified in place |
| Historical failed-install recovery | Operator returned `dbbd950...` to `ABSENT`; exact firewall rollback and ordinary/private baseline passed | Superseded by installed `3603f11...`; failed artifact retained |
| Current recovery console | Earlier session expired with login CAPTCHA/direct VNC Unauthorized; owner authorized continuation; official QEMU console now connected/encrypted with Ubuntu tty1 login visible | Console PASS; fresh preflight remains required before bounded upgrade |

The earlier owner-reported mobile white-list pass remains useful historical
evidence for the preserved private S4 canary, but it is not reused as current
Task 15 fleet, client-matrix, accounting, or production-readiness proof.

## Read-only inventory evidence

| Node | Identity/role | Ordinary baseline | Candidate ports | Read-only verdict | Mutation verdict |
| --- | --- | --- | --- | --- | --- |
| S4 | Existing VPN plus private CDN canary node | Ordinary/private saved hashes, units/PIDs and strict SSH PASS after upgrade; commercial b441 ACTIVE | Private `18081/18082`; commercial Xray `28081/18084`, loopback API `28082`, agent `18443`, loopback health `18444` | Console/preflight/locked apply and synthetic 2/3/4 receipts PASS; no nginx/firewall/CDN/private change | GET-only recovery PASS; unknown-outcome fault injection, ingress, client/CDN, rollback/re-apply and promotion open |
| S2 | Multi-protocol/bot node | Hysteria, nginx, Caddy, `vpn_bot` active | Five-port commercial set requires fresh proof | `READ_ONLY PASS`; network ownership unresolved | `NO_GO` |
| S3 | x-ui/VPN node | x-ui with protected child Xray active | Five-port commercial set requires fresh proof | `READ_ONLY PASS`; network ownership unresolved | `NO_GO` |
| Current S1 | Replacement public control plane | maestro-panel, x-ui/Xray, Hysteria, nginx active | Five-port commercial set requires fresh proof | Strict pinned SSH `PASS`; exact controller-source leaf staged only | `NO_GO` |

## Per-host preflight and rollout record

Fill one row only from a fresh protected evidence bundle. `UNKNOWN` and blank
are failures.

| Node/stage | Exact reviewed SHA and CI | Console | Fresh inventory/ports | Backup plus restore | Artifact/config digest | Node cert | Firewall plan | Apply | Functional canary | Ordinary baseline | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| S4 failed `dbbd950...` attempt | All exact workflows green; immutable artifact verified | Connected encrypted QEMU console PASS | Fresh five-port and SSH proof passed | Backup plus isolated SQLite restore PASS | Manifest `b005eba...`; config `8ff75a...`; runtime input `57a9ae...`; shipped plan PASS | Source chain/SAN/EKU/key/mode checks PASS | Applied under rollback timer, then exactly rolled back | Failed: Xray could not traverse agent-owned `runtime/relay-ca`; operator recovered to `ABSENT` | Not run; packaged ingress missing | Full post-recovery baseline PASS | `STOP`; do not reapply |
| S4 previous corrected apply | `3603f11bbc35a4a9d708c41db1bc13f0d2907805`; exact CI GREEN; artifact `9956162284` | PASS at original apply | Five-port/SSH proof passed at apply | Protected backup/restore proof retained | Manifest `623fa69...`; config `8ff75a...`; runtime `afb832c...`; plan/apply match | Relay-CA `root:xray` `0750`; files `0640`; real service users PASS | Source-restricted delta retained after strict SSH/observer | Historical ACTIVE; now replaced by b441 and retained | Synthetic POST 503; receipt GET 404; generation 1 persisted | Ordinary/private files, units and PIDs unchanged | Historical install PASS; generation 1 never replayed |
| S4 b441 upgrade | `b4415daa90c95a38f9a7b9adea7642c66e63a420`; exact CI/review GREEN; artifact `9957942956` | Official QEMU console connected/encrypted PASS | Fresh strict SSH, previous shipped status and baseline PASS | Protected backup retained; change sheet transferred before apply | Manifest `a687657...`; config `5fd67f...`; runtime `bc65f94...`; plan/apply match | Protected inputs bound by plan/apply | Exact source-restricted delta unchanged | Locked shipped apply EXIT 0; ACTIVE, six checks true | Add 2/revoke 3/resume 4 and GET-only recovery PASS; no lost-response injection | Post-upgrade ordinary/private hashes, units/PIDs and strict SSH PASS; old package retained | Isolated upgrade/synthetic proof PASS; promotion NO_GO |
| S4 deliberate rollback under five minutes | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S4 same-release re-apply | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S2 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| S3 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |
| Current S1 | — | — | — | — | — | — | — | Not run | Not run | Not run | `NO_GO` |

For every completed stage record protected evidence digests for direct sidecar,
Yandex CDN, literal edge with correct SNI/Host, counter delta, add/revoke/resume,
unknown-receipt recovery, complete desired set, selected-exit country truth,
and ordinary baseline. Never paste the endpoint or identity.

The b441 plan binds release `b4415daa90c9-s4commercial-bc65f9447a8e945a`,
config SHA-256 `5fd67f3bf80fc3a7c0bfae1de32d421b66ee11ac95bff8ea3f851ca71d052f38`
and runtime-input SHA-256
`bc65f9447a8e945a4b7b8686859f54e7f05408f6477b2171047724ed3dde3f34`.
Generation 1 was delivered exactly once after a leaf-only TLS correction;
HTTP 503 took 6240 ms and exact receipt GET was 404. Recovery is GET-only;
neither generation 1 nor the unsent-TLS correction may be replayed. Reviewed
local helpers preserved the old proof and bound generations 2/3/4 to b441
and previous commit `3603f11bbc35a4a9d708c41db1bc13f0d2907805`. Preparation
executed on S4 after ACTIVE/baseline proof; nine protected files moved to S1.

| Synthetic phase | POST result/time | Exact receipt GET result/time | Verdict |
| --- | --- | --- | --- |
| Add generation 2 | 200 / 263 ms | 200 / 146 ms | PASS |
| Revoke generation 3 | 200 / 290 ms | 200 / 149 ms | PASS |
| Resume generation 4 | 200 / 232 ms | 200 / 152 ms | PASS |
| GET-only resume recovery | No POST sent; zero requests replayed | 200 / 198 ms | PASS; no lost-response fault injection |

All receipts matched exact release/config/boot/generation/managed-set digest
with freshness at most 30 seconds. Current desired generation 4 is resumed.
Old generation 1 was retained byte-identical and never replayed. GET-only
recovery did not simulate a lost response; unknown-outcome fault injection
remains open. No real customer, nginx/firewall/CDN or private-canary change ran.

The one existing paid Yandex CDN resource still points at the preserved private
canary path. Reviewed canonical ingress source preserves the private path to
`18081`, routes only the new secret commercial path to `28081`, preserves the
full HTTP request, and returns `404` otherwise. It is not installed; its exact
firewall/CDN rollback is not exercised. The Yandex CDN functional canary is
therefore not runnable and remains `NO_GO`. No second paid CDN resource is
authorized.

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
| Both live bot identities resolved; exactly one poller each; pending updates preserved | Read-only PASS: S1 `@MaestroSecureVPN_bot` / `vpnbot.service`; S2 `@MaestroSecureNaive_bot` / `vpn_bot.service`; active, one process each, webhook absent, pending zero; commercial flow unproven |
| Customer channel identity resolved and redacted delivery proved | `@maestrovpn` configuration-backed; publishing rights and delivery unproven; no messages sent |
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

S4 b441 isolated upgrade: `PASS`. Further bounded S4 operations require their
current preconditions; full fleet/customer promotion remains `NO_GO`.

Customer-live cutover: `NOT_AUTHORIZED`.

Real charges, release/signing/OTA, OLCRTC, and WDTT: unchanged and out of scope.
