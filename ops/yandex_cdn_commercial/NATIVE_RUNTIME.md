# Native Android CDN publication contract

`GET /account/whitelist-runtime` is the optional rqlite adapter's native delivery
route. The public transport is the same trusted HTTPS origin as the selected
account's subscription and balance. The only identity input is
`Authorization: Bearer <subscription-token>`. No query parameters, account ID,
cookies, token in URL or redirect-based credential forwarding are supported.
The handler does not create a new TLS listener or trust forwarded headers.

The actual `ServiceBusiness` authenticates the token through the existing
customer service, then calls the configured `WhiteListPublicationSource` under
its existing timeout. The runtime source resolves real durable commercial
admission, account status, balance, all active Origins, exact live receipts and
metering freshness. A balance `PUBLISHABLE` or old `/info.white_list` metadata is
not runtime authority. Missing publication configuration stays unavailable;
this endpoint does not enable the existing publication flag.

HTTP200 contains exactly this envelope and typed profile fields:

```text
schema_version: 1
issued_at_unix: integer
fresh_until_unix: integer
projection_version: positive integer
desired_generation: positive integer
profiles: nonempty array (maximum16), each containing:
  route_id: 64 lowercase hexadecimal characters
  label: existing validated public country label
  transport_profile_id: immutable profile identifier
  transport_release_id: immutable release identifier
  compatibility_preset_id: immutable preset identifier
  address: validated published hostname or approved public IPv4 literal
  port: 443
  server_name: validated TLS SNI hostname
  host: identical to server_name
  path: protected canonical XHTTP path
  client_id: canonical client UUID
  encryption: validated client ML-KEM material
```

Schema1 fixes protocolVLESS, networkXHTTP, modepacket-up, TLS, ALPNh2,
fingerprintfirefox, uplinkGET/body, sessionID query keyauth/length16 and sequence
query keychunk_id. There is no arbitrary Xray JSON, extra object, DNS/routing
policy, local listener or SOCKS credential in the server response. Existing
canonical share-link validation also validates every native transport; one bad
node rejects the entire batch. Public labels and client IDs must be unique.
The opaque route hash binds transport/provenance fields; cosmetic label changes
do not change it. The complete JSON response is bounded to64KiB.

Freshness is rechecked after the source returns. `issued_at_unix` rounds the
current UTC time upward to whole seconds; `fresh_until_unix` rounds the actual
source deadline downward. At least1 and at most5 complete seconds must remain;
otherwise delivery fails closed. The renderer never extends the source deadline.
Android derives a conservative monotonic deadline from its request-start time
plus `(fresh_until_unix - issued_at_unix)` seconds and rejects an already-expired
response. Local wall-clock correction must not extend the lease. Account
revision, selected network and runtime session must still match when applying
an asynchronous response. A fresh unchanged transport can renew its lease
without restarting Xray; changed route/provenance requires a new gated session.

The Android service resolves a published hostname through its current bound
`Network`, preserves SNI/Host, and supplies the native engine only its typed
schema1 with a validated public literal IPv4 plus newly generated local SOCKS
port/credentials. The native engine does not independently authorize entitlement.
CDN remains explicit/manual and phone-only; ordinary selection is independent.

Status contract:

- 401: missing, malformed or ambiguous Bearer header.
- 404: unknown subscription token, preserving the existing customer contract.
- 400: query parameters are present.
- 405: method other thanGET, includingHEAD.
- 403: no permitted CDN entitlement, no balance or expired ordinary access.
- 503: optional provider absent, stale/pending publication, invalid material,
  release/receipt failure, inadequate remaining lease or source error.

Any non200 response invalidates the client's CDN lease and contains no profile
material. All responses use `Cache-Control: no-store`; there is no ETag or
ordinary-subscription cache reuse. Bare `/sub` remains untouched for1.0.157 and
ordinary clients. Source availability is not deployment, Android runtime proof,
release approval or customer cutover approval.
