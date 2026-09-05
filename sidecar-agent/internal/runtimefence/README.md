# Managed-session fence proof runtime

This package is specific to Xray module
`v0.0.0-20260509173629-1bdb488c9ec0`. It wraps the registered dispatcher before
`core.New`; it does not replace an already running feature, vendor upstream, or
alter the ordinary/private Xray process. The proof executable is **not ready
for production deployment**. Its commercial identities start denied.

The existing operator command forms are retained:

```
xray version
xray run -test -config /absolute/path/config.json
xray run -config /absolute/path/config.json
```

Only the new isolated executable registers a `google.protobuf.Any` config
factory. The pinned source has no existing Any config factory. There are exactly
two accepted TypeUrls:

* `type.maestrovpn.internal/runtimefence/dispatcher-v1`: Value is UTF-8 JSON
  `{"schema":1,"boot_id":"<64 lowercase hex>","config_digest":"<64 lowercase hex>"}`.
* `type.maestrovpn.internal/runtimefence/service-v1`: Value is UTF-8 JSON
  `{"schema":1}`.

These are private core configuration envelopes, not network configuration
inputs. `Inject` creates them from a clone after rejecting preexisting Any apps,
duplicate/missing dispatchers, duplicate/missing/unexpected Commander services,
direct Commander listeners, managed sniffing/fallbacks and fake DNS. It replaces
the sole default dispatcher and the sole StatsService entry, retaining the
HandlerService and existing protected API transport. Unknown TypeUrls, fields,
duplicate fields, trailing JSON, invalid values and bodies above 4096 bytes fail
closed. No protobuf descriptors or generated source are hand-written.

The existing Commander gRPC server additionally exposes
`/maestro.runtimefence.v1.ManagedSessions/Apply`. Both request and response use
the already generated `google.protobuf.BytesValue`, containing one strict JSON
object. The API inherits the isolated configuration's existing transport and
client authentication; it creates no listener. A caller must use that existing
protected transport, not expose Commander publicly.

Control/receipt wire schema is 2; the private Any configuration schema above
remains 1. Request fields:

```
{"schema":2,"operation":"grant|renew|fence","email":"wl:<identity>:exit-s1",
 "boot_id":"<physical process digest>","config_digest":"<input file SHA-256>",
 "generation":1,"clock_domain":"<runtime/local domain SHA-256>",
 "deadline_boottime_ns":123456789000000}
```

Email also permits exits s2–s4. Generation is a nonzero uint64, scoped to that
email and physical boot. It versions control operations, including each fresh
renewal; it is not the durable desired-state generation. A future caller must
map its stable desired/control binding and renewal sequence to this monotonic
operation version, rather than update durable desired state every few seconds.
An exact same-generation, same-operation, same-absolute-deadline retry is idempotent
and never extends a deadline. A stale generation or changed operation/deadline at
the same generation is rejected. A grant requires the
currently installed forward VLESS MemoryUser on `maestro-cdn-in`, with both user
traffic counters enabled, no online counter, no Vision flow or reverse account.
It is bound to that specific in-memory user. After a fence, grant requires
RemoveUser/AddUser to create a new MemoryUser, preventing an old idle mux from
inheriting the new authorization. The runtime does not provision users itself.

Grant and renew require a positive signed 64-bit `deadline_boottime_ns`, an
absolute Linux `CLOCK_BOOTTIME` value in nanoseconds. At the actual runtime read,
`0 < deadline - now <= 5 seconds` must hold, with checked arithmetic. An RPC
delivered after its deadline cannot become a fresh lease. Fence requires the
deadline to be absent or zero. Renew requires a higher operation generation, the same installed
MemoryUser, and a lease which has not expired. It retains existing sessions and
uses the newly authorized absolute deadline. Expiry, explicit fence, or runtime close prevents
renewal, including delayed exact retries. Regrant after expiry/fence requires
the replacement MemoryUser and completed drain described above.

The local caller and runtime use `ReadLeaseClock`; neither converts a request
back into a duration starting at RPC receipt. Runtime independently computes
and reports `clock_domain` as SHA-256 of the literal `linux:CLOCK_BOOTTIME`, NUL,
kernel boot ID, NUL, and `/proc/self/ns/time` link. The caller must compare the
runtime-reported domain with its own locally computed domain, in addition to
the existing exact Xray process boot/config binding. A boot digest alone does
not prove a shared time namespace. The existing Xray client transport uses the
isolated loopback mTLS API; the future lease caller must preserve that boundary.
This clock contract cannot authorize a remote-clock caller.

BOOTTIME includes host suspend and has no wall-clock fallback. An independent
Go timer is only a wake hint which starts the existing cancel/interrupt drain.
Dispatch admission, every counted I/O start, renewal and timer callbacks check
the actual kernel BOOTTIME under the same gate lock. Thus a delayed wake hint,
including one delayed by suspend, cannot authorize another operation after the
absolute deadline. A failed, zero or regressing clock read fences existing
leases and denies further grant/renew/I/O. Explicit fence can still drain and
return real counters without using time authority to reopen users. Old timer
callbacks cannot fence a later renewal. Close/fence stops the active wake hint;
failed or timed-out drain does not reopen admission. Autonomous expiry emits no
final-counter receipt; an explicit fence must still prove true drain.

A successful grant response has fields `schema` (2), `state` (`granted`), `email`,
`boot_id`, `config_digest`, `generation`, `clock_domain` (computed by runtime),
`reset_sequence` (0), and `observed_at`
(UTC RFC3339Nano). Successful grant and renew use state `granted` and additionally
return the unchanged `deadline_boottime_ns` and advisory `lease_remaining_ms`.
The latter floors the actual BOOTTIME remaining duration to whole milliseconds;
it can be zero near expiry and never exceeds 5000. A caller derives its local
authority from the shared absolute deadline, never by adding the remaining
duration to receipt time. It rejects a zero/already elapsed result. Exact retries
return the original deadline and decreasing remaining duration. A successful
fence omits both lease fields. Neither a kernel lease deadline nor `observed_at`
is an asserted byte cutoff or billing boundary.
State `fenced` additionally has signed 64-bit `uplink` and `downlink` containing
real nonnegative cumulative counters; an existing complete zero pair still uses
this state. State `fenced_unused` is permitted only when both counters are absent,
no successful managed `gate.start` has ever occurred for this email during the
physical process boot, and the same complete session-drain proof has finished.
Its UP/DOWN fields are absent, never substituted zeros. The ever-started history
survives replacement MemoryUsers and regrants for that email. A partial pair or
missing counters after any successful start still fails closed. `fenced_unused`
allows a caller to distinguish a safely fenced, never-dispatched identity from
an unavailable usage sample; it is not a byte sample or an asserted cutoff time.
A fence retry may observe a later
timestamp; it never changes cumulative usage while that generation is fenced.

The boot digest is SHA-256 of actual kernel boot ID, NUL, PID, NUL, Linux process
start ticks, matching the agent's physical process identity. Config digest is
SHA-256 of exact input file bytes. All three destructive StatsService entry
points (`GetStats`, `QueryStats`, `GetUsersStats`) reject Reset=true, including
the legacy v2ray service alias. All normal StatsService methods are delegated
unchanged. This executable provides no reset API or fabricated reset epochs.

Fence atomically closes admission, then cancels and interrupts live user
streams, including their captured parent connection. It waits at most two
seconds, further shortened by the request context. A successful final receipt
requires completed dispatch workers, completed stop actions, and zero active
counted I/O calls. A blocked underlying read/write/interrupt retains its registry
slot and produces no receipt on timeout. It remains fenced. The timestamp is
the real final observation time, **not an invented billing boundary timestamp**.

For Dispatch, UP remains counted before the returned writer's pipe write, as
in the pinned implementation; the returned reader remains a raw `*pipe.Reader`
for XUDP. For DispatchLink, the real counted WrapLink is inside the I/O guard.
The managed delegate's policy disables a second user-counter layer. Ordinary
unmanaged dispatch uses a separate delegate with the original policy. Normal
completion of one mux child preserves its parent's connection and queued DOWN
bytes; only a user fence performs destructive interruption. Registry entries
survive worker completion until any already started counted I/O has returned.

Memory is bounded to 4096 historical identities, 4096 outstanding streams,
and 128 streams per identity. Exhaustion denies new admission. History is
retained for the boot to reject stale commands; an API cannot forget a fence.
The executable accepts one config file of at most 1 MiB and does not print its
contents, credentials, requests or receipts.

Compatibility: the method and BytesValue envelope remain the same. Control and
receipt schema 1, duration-only `lease_ms` grants and mixed duration/deadline
requests are rejected before user lookup. Callers must send schema 2, preserve
the shared absolute BOOTTIME deadline through transport delays and retries,
verify the runtime-reported clock domain, support renew and non-extending retries,
and use independent operation generations. No real caller or production
deployment is supplied by this package. The existing desired receipt TTL and
refresh loop cannot drive this lease; they must be wired explicitly before
deployment, along with the actual control binding. Backend period transition must
first revoke before the boundary, prove genuine drain, and debit this real
final cumulative observation; this package alone does not complete period
accounting, all-Origin admission, or commercial live readiness.

Linux construction requires the actual BOOTTIME syscall, kernel boot identity
and time namespace to be readable and valid. Unsupported/non-Linux construction
fails explicitly; there is no duration, MONOTONIC, or wall-clock fallback. The
existing pinned `golang.org/x/sys v0.43.0` supplies the syscall; no version change
or new dependency is required.
