# Architecture

```text
Panel/control plane → desired state → agents on S1/S2/S3/S4 → immutable sidecar release
client → approved CDN edge → per-node isolated Xray sidecar → egress
local stats → node listener → central idempotent metering → immutable ledger
```

The target is mandatory multi-node across all four servers. Production 3x-ui and ordinary VPN are separate and untouched.
