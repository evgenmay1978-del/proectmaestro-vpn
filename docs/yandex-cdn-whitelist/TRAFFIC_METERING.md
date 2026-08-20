# Traffic metering

Each isolated sidecar exposes opaque per-user cumulative counters locally. A listener emits epoch-aware idempotent samples to one central writer. Aggregate 3x-ui/interface/client/IP counters are not a billing source.
