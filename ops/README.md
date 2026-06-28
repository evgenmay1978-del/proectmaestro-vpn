# ops/ — repeatable MaestroVPN operations as tool-scripts

Applying the lesson from the training videos (esp. Anthropic-skills #5: *"if a task can be done with
code, do it with code"* → run a tested script instead of re-deriving the commands each session →
0 tokens, stable, fast). These encapsulate the operations I used to type out by hand every time.

All run **on S1** (where the panel, telemetry, mirror and repo live).

| script | when to use | safety |
|--------|-------------|--------|
| `deploy-panel.sh [--dry-run]` | after editing the Go backend — build + deploy maestro-panel | verifies /healthz + /order/tariffs + service active; **rolls back** the binary on failure. `--dry-run` = build+vet only. |
| `verify-ota.sh [--sync]` | after cutting an OTA release — confirm the chain reached the fleet | read-only (—sync triggers the mirror upload). **Fails loudly if the 107 waypoint breaks.** |
| `crash-reports.sh` | check the fleet's real crashes — read this, don't wait for «клиенты говорят» | read-only. |

The orient/snapshot script lives separately at `/root/.claude/maestro-orient.sh` (run by the
SessionStart hook). Memory index: see `MEMORY.md` → §🖥️ / §🎓.
