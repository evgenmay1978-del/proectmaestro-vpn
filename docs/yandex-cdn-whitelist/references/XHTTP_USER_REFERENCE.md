# User-supplied XHTTP/CDN reference

Status: reference input, not a verified production configuration.
Recorded: 2026-08-29. The reference names Ubuntu 22.04 and Xray-core 26.5.9;
neither version nor support for every option below is established by this note.

## Scope and provenance

The owner supplied an XHTTP + padding guide and two third-party client JSON
examples in this task. This note preserves the technical substance after
context compaction. It does not change the canonical branch, installed Android/
TV 1.0.157 baseline, existing payments, approval boundaries, or release gates.

No third-party account identifiers, IP addresses, domains, credential/encryption
blobs, subscription URLs, or the previously pasted GitHub token are reproduced.
Do not fetch the third-party subscription or import its credentials. A complete
third-party JSON is not a MaestroVPN production template.

## Guide supplied by the owner

The proposed chain is client -> Yandex Cloud CDN -> origin (Nginx + Xray) ->
exit (Xray) -> Internet. Client TLS and CDN-to-origin TLS use separate
certificates. The guide proposes a TLS-protected VLESS hop from origin to exit.

The origin XHTTP listener is loopback-only. Nginx forwards streaming traffic
without response/request buffering. The guide maps client OPTIONS to upstream
POST and uses XHTTP packet-up mode, token-like padding, and padding carried as
a query fragment in a header. Its sample client sends OPTIONS with a body.
Its sample limits are 1,000,000 bytes per upload, 30 ms minimum interval,
and 30 buffered uploads.

The guide also calls for:

- Origin and exit DNS/certificates before the CDN resource and client CNAME.
- A managed certificate for the client CDN hostname, with persistent DNS
  validation records; the origin's certificate remains separate.
- HTTPS to the origin and a Host value matching the origin virtual host.
- Disabled CDN/browser caching, compression, segmentation, origin shielding,
  redirect following, and transformations that could alter transport headers
  or query data; GET/HEAD/OPTIONS are the proposed allowed methods.
- Matching client address/SNI/Host and certificate identity; no insecure TLS.
- A body-carrying CDN probe, subsequent HTTP-to-HTTPS redirect, certificate
  renewal hooks, and firewall checks without losing the actual SSH access.

These are supplied requirements/candidates, not claims about current Yandex
console fields or CDN behavior. The guide's stated console date is 2026-07-01.

## Sanitized observations from the two supplied client JSON examples

Both examples describe VLESS over TLS + XHTTP, packet-up mode, a shared
transport path shape, and options nested under xhttpSettings.extra. They dial
an IP while sending a DNS name as TLS SNI/HTTP Host; this differs from the
guide's hostname-dialing example and does not establish a requirement for us.

Observed transport settings:

- GET uplink with data in the request body, rather than OPTIONS.
- 3,000,000-byte uploads, 30 ms minimum interval, 100 buffered uploads.
- Session and sequence fields in the query, with duplicate/alias-looking
  session option names that must not be assumed to be supported.
- 50-150 bytes of token-like header padding; no SSE/gRPC framing headers.
- A 32,768-byte server-header setting.
- Firefox TLS fingerprint, h2/http/1.1 ALPN, one XHTTP multiplexed connection.
- Different connection/request recycling limits between the two examples.
- A long ML-KEM-related VLESS encryption value; support and interoperability
  are unverified, and none of that value is copied.

Observed routing sends an explicit domestic allow-list and private destinations
direct, selected Telegram ranges and default traffic through the tunnel, and
blocks UDP/443. This is descriptive input only: do not replace MaestroVPN's
existing routing policy or introduce third-party network exceptions from it.

## Decisions still requiring evidence

Before choosing a MaestroVPN transport profile, verify the installed/client
core capabilities against official Xray sources and current Yandex CDN
documentation. In particular, establish which extra keys are parsed, their
placement/types, and whether unsupported keys are silently ignored.

Compare OPTIONS/body and GET/body in an isolated, owner-authorized canary.
Verify the received body, not just a 204 response or Content-Length header.
Exercise realistic upload sizes, headers, multiple sessions, cancellation,
reconnects, timeout behavior, cache bypass, and the actual tunnel/exit path.
A syntactically valid JSON or successful CDN probe alone does not prove a VPN.

Retain per-customer credentials and canonical encrypted access material.
The guide's single shared sample UUID must not become a shared customer
identity. Separate the service hop to the exit from customer identity and
quota/metering attribution. Integrate gigabyte purchases through the existing
payment and Telegram-bot contracts; do not replace the current payment flow.

Any eventual deployment must include verified certificate renewal permissions,
loopback-only internal listeners, rollback, health/metrics, and isolation from
ordinary VPN users. Production inventory, deployment, real-client switching,
billing, OTA, and cutover remain subject to their existing explicit approvals.

## Where this fits

Use this note alongside ../MASTER_REQUIREMENTS.md, ../SPEC.md, ../DEFINITION_OF_DONE.md,
and ../HANDOFF.md in the parent directory. Resolve differences against those canonical
requirements and evidence; do not treat this input note as a passing gate.
The repository implementation and the still-open Task 8 findings remain the
current work, followed by the compatibility, infrastructure, metering, canary,
and production-readiness gates in the active plan.
