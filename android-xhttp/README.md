# Android XHTTP native boundary

Source implementation of authenticated TCP and UDP-over-TCP v2. No Android ABI
build, device socket-protection, XHTTP data transfer or coexistence proof is claimed here.
The app, selector, server delivery and workflows are not connected by this module.

The module uses Xray 26.5.9, exact commit
1bdb488c9ec09ea51e6899697d5b7437f3cf6eb2. It builds one new in-process
C-shared library with a small JNI bridge. It does not rebuild ordinary libbox,
ship another gomobile go.Seq runtime, create a VpnService, or modify WDTT/OLCRTC.
Avoiding go.Seq removes the known Java-class collision; it does NOT prove that
two independent Go native runtimes safely coexist on Android. That remains an
explicit ABI/device gate (load order, signal handling, callbacks, repeated
start/stop and app/service lifecycle).

## Exact Kotlin ABI

The future class is com.maestrovpn.tv.whitelist.XhttpNative:

    object XhttpNative {
        init { System.loadLibrary("maestro_xhttp") }
        @JvmStatic external fun nativeStart(
            sessionId: Long, payload: ByteArray, protector: SocketProtector
        ): Int
        @JvmStatic external fun nativeStop(sessionId: Long): Int
    }

    interface SocketProtector {
        fun protectSocket(sessionId: Long, fd: Int): Boolean
    }

Keep these names/methods from shrinking/obfuscation. Call Start/Stop off the main
thread. Session IDs must strictly increase, including after failed Start, within
1..Int.MAX_VALUE for this process. The engine is single-session; Stop must name
the exact session, is idempotent and cannot stop a newer one.

Status codes: 0 success, 1 invalid typed input, 2 engine busy, 3 stale/reused
session ID, 4 core start failure, 5 JNI/callback setup failure, 6 core close
failure. No core error text, transport material or credentials cross the ABI.
Stop revokes callbacks and starts closing all tracked sockets independently of
core.Close. Native cleanup is bounded to three seconds. A close failure/timeout
poisons the manager: another engine cannot start in the same process.

The callback runs synchronously on a Go transport thread. It must check that
sessionId still names the same service/account/network, call the existing
VPNService.protect(fd), check its Boolean result and bind the socket to that
session's underlying Network before returning true. Network.bindSocket can use
ParcelFileDescriptor.fromFd(fd) and close only that duplicate; never adopt/close
the original fd. Missing network, false, stale session or exception means false.
Do not block waiting for UI work or re-enter nativeStart/nativeStop from this
callback. The JNI bridge owns a global callback ref, takes a local ref for each
in-flight call, attaches/detaches only threads it attaches, clears exceptions as
denial, and checks the generation again after the callback.

## Typed input and routing

payload is UTF-8 JSON, at most 16 KiB, exactly these eleven scalar fields:

- schema: 1
- address: canonical public literal IPv4, resolved through the selected Android
  Network by the caller; hostname/system DNS input is rejected
- port: 443
- server_name and host: the same published TLS SNI/HTTP Host
- path: the published XHTTP path, 2..2048 ASCII bytes
- client_id: published VLESS UUID
- encryption: published mlkem768x25519plus.native.0rtt. material
- socks_port: caller-selected available loopback port, 1024..65535
- socks_user and socks_pass: distinct per-session random 24..64-byte values,
  canonical unpadded base64url

Do not log the payload. The caller must fetch/validate fresh publication
authorization for the selected trusted account, generate SOCKS credentials with
SecureRandom, preserve server SNI/Host/path/encryption, and never infer runtime
authorization from the balance response. Native validation is not entitlement
authorization. Stop/restart on account/network changes and authorization loss.

Only the existing approved VLESS/XHTTP packet-up, GET/body, query session/sequence,
Firefox fingerprint and TLS h2 settings are generated. There is one
password-authenticated TCP SOCKS5 listener on 127.0.0.1 and one VLESS outbound.
Xray itself has no inbound listener. There is no direct/DNS fallback or arbitrary configuration input.
The native external dialer accepts only TCP to the one allowed IP:443.
Xray 26.5.9's SOCKS UDP filter remembers only the authenticated peer IP without
expiration. On loopback this would authorize UDP from other local apps after
one legitimate association, allowing them to spend the account's CDN balance.
Therefore this boundary exposes no UDP listener and rejects UDP_ASSOCIATE, BIND,
SOCKS4 and no-auth clients. UDP uses the same authenticated SOCKS5 TCP CONNECT
to sp.v2.udp-over-tcp.arpa:0, followed by UOTv2 framing. Every association must
authenticate with the current session's random credentials; comparisons use
crypto/subtle. Domain destinations remain domains inside VLESS: no local DNS
lookup is performed. Per-packet destinations and connected UOT mode are both
supported. Separate core.Dial UDP connections preserve distinct destinations.

The libbox SOCKS outbound must set network: ["tcp", "udp"] and
udp_over_tcp: {"enabled": true, "version": 2}. The pinned libbox source
SagerNet/sing-box fc25cedc25e9b1fcecb4f1589453cb381d518031,
protocol/socks/outbound.go (SHA256
7567857030ca9aa5aebb598d4938538e4b39754bd619373ff163364fff64f2f0),
routes both DialContext UDP and ListenPacket through uot.Client when enabled.
Native codecs use the already pinned sing v0.5.1. Its generic SOCKS handler and
uot.NewServerConn are deliberately not used: they can open UDP sockets or resolve
destination domains locally. Stable core.Dial supplies Xray's own instance context;
exported session APIs force the sole maestro-cdn outbound tag.

Bounds: 32 accepted TCP connections, 64 UDP destination links across the engine,
10 seconds for authentication/initial request and dispatch, 60 seconds idle for
UOT associations, 10 seconds per UOT write, and a three-second native stop bound.
Zero-length UDP and payloads above 7526 bytes are rejected by closing the
association before dispatch; oversized responses also close it. This explicit
limit is required by pinned Xray XUDP's 8192-byte buffer and 666-byte overhead
check; no silent truncation, direct fallback or unauthenticated UDP listener is
allowed. Android device validation must cover UDP DNS and ordinary app datagrams,
as well as this size limit. This source does not claim general jumbo UDP support.

The generated sockopt.mark is an internal session cookie. The custom system
dialer consumes it and NEVER applies SO_MARK. This is needed because upstream
XHTTP caches HTTP clients globally: a late old client keeps its old cookie and
cannot obtain the current account's protector even for the same CDN address.
The wrapper closes all tracked raw upstream connections on Stop independently
of Xray Close. No speculative direct route or application UID bypass is used.

Pinned core.Dial does not expose TCP half-close. The current stream bridge
closes both directions when either copy ends; protocols that send FIN and then
wait for a response require a later supported upstream half-close interface.
No unsupported internal context keys or linkname hooks are used to bypass this.

## Root-owned validation and build

Do not build on the owner's weak Windows PC. In isolated exact-SHA GitHub CI:

    cd android-xhttp
    go test -mod=readonly ./...
    go test -mod=readonly -race ./...
    go vet -mod=readonly ./...

Tests use synthetic transport material, in-memory pipes and local loopback sockets.
They cover unauthorized traffic, TCP, both UOT modes, distinct domain/IP targets,
explicit datagram bounds, and closing native sockets while core.Close stalls.
The real-Xray local test starts the owned SOCKS listener and negotiates authentication;
it does not send any request to a CDN or the internet.

Use Go 1.26 (the pinned Xray module requires it), NDK 28.0.13004108 and API 23.
Build each ABI into its own staging directory:

    CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
      CC="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android23-clang" \
      go build -mod=readonly -trimpath -buildmode=c-shared \
      -o staging/arm64-v8a/libmaestro_xhttp.so .

    CGO_ENABLED=1 GOOS=android GOARCH=arm GOARM=7 \
      CC="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi23-clang" \
      go build -mod=readonly -trimpath -buildmode=c-shared \
      -o staging/armeabi-v7a/libmaestro_xhttp.so .

The JNI C file includes NDK jni.h and cgo's generated _cgo_export.h; no gomobile
tool or separate Java runtime is needed. Root must verify ABI/exported JNI symbols,
record checksums and package only the .so files in the test APK. Existing libbox,
WDTT and OLCRTC artifact identities must remain unchanged.

go.mod versions are copied from the exact cached Xray go.mod. go.sum is the
deduplicated exact contents of that upstream go.sum and the repository's
sidecar-agent/go.sum, including the existing Xray module checksums. No sums or
versions were invented. CI may identify a necessary graph correction; do not
claim dependency closure until the readonly build succeeds.
