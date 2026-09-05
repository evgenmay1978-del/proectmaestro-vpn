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

Request fields:

```
{"schema":1,"operation":"grant|renew|fence","email":"wl:<identity>:exit-s1",
 "boot_id":"<physical process digest>","config_digest":"<input file SHA-256>",
 "generation":1,"lease_ms":5000}
```

Email also permits exits s2–s4. Generation is a nonzero uint64, scoped to that
email and physical boot. It versions control operations, including each fresh
renewal; it is not the durable desired-state generation. A future caller must
map its stable desired/control binding and renewal sequence to this monotonic
operation version, rather than update durable desired state every few seconds.
An exact same-generation, same-operation, same-lease-length retry is idempotent
and never extends a deadline. A stale generation or changed operation/length at
the same generation is rejected. A grant requires the
currently installed forward VLESS MemoryUser on `maestro-cdn-in`, with both user
traffic counters enabled, no online counter, no Vision flow or reverse account.
It is bound to that specific in-memory user. After a fence, grant requires
RemoveUser/AddUser to create a new MemoryUser, preventing an old idle mux from
inheriting the new authorization. The runtime does not provision users itself.

Grant and renew require `lease_ms` from 1 through 5000; fence requires it to be
absent or zero. Renew requires a higher operation generation, the same installed
MemoryUser, and a lease which has not expired. It retains existing sessions and
arms a fresh bounded deadline. Expiry, explicit fence, or runtime close prevents
renewal, including delayed exact retries. Regrant after expiry/fence requires
the replacement MemoryUser and completed drain described above.

Deadlines use Go monotonic time. An independent timer autonomously denies and
starts the existing cancel/interrupt drain at expiry. Dispatch admission, every
counted I/O start, and renewal also check the deadline under the same gate lock,
so delayed timer scheduling cannot authorize another operation. Old timer
callbacks cannot fence a later renewal. Close/fence stops the active timer;
failed or timed-out drain does not reopen admission. Autonomous expiry emits no
final-counter receipt; an explicit fence must still prove true drain.

A successful grant response has fields `schema`, `state` (`granted`), `email`,
`boot_id`, `config_digest`, `generation`, `reset_sequence` (0), and `observed_at`
(UTC RFC3339Nano). Successful grant and renew use state `granted` and additionally
return `lease_expires_at` and `lease_remaining_ms`. The former is only a runtime
wall-clock hint, never an exact byte cutoff, billing boundary, or clock authority.
The latter floors the actual monotonic remaining duration to whole milliseconds;
it can be zero near expiry and never exceeds 5000. A caller derives its local
deadline from request-start monotonic time plus this remaining duration and
rejects a zero/already elapsed result. Exact retries return the original expiry
and decreasing remaining duration. A successful fence omits both lease fields
and additionally has signed 64-bit `uplink`
and `downlink` containing real nonnegative cumulative counters. No zero counters
are invented when a counter pair is absent. A fence retry may observe a later
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

Compatibility: the method, BytesValue envelope and schema number remain the
same, but unleased legacy grant requests are intentionally rejected. Callers
must send a bounded lease, support renew and the non-extending retry contract,
and use independent operation generations. No real caller or production
deployment is supplied by this package. The existing desired receipt TTL and
refresh loop cannot drive this lease; they must be wired explicitly before
deployment, along with the actual control binding. Backend period transition must
first revoke before the boundary, prove genuine drain, and debit this real
final cumulative observation; this package alone does not complete period
accounting, all-Origin admission, or commercial live readiness.
