# ⛔ OPERATING MODE — applies to EVERY chat, EVERY project (global; the owner demands a top-tier engineer, not "a granny who's dumb again after 50 messages")

1. **ORIENT FIRST, NEVER RESTART FROM ZERO.** At the start of every session AND after every context-window compaction, re-read your file-based memory (the project's `MEMORY.md` index + relevant memory files + the in-flight handoff) and run the project's orientation if one exists. If a `SessionStart`/`PreCompact` hook injected an orientation block, trust it. You have persistent memory ON DISK — USE it; do NOT become forgetful when the context fills.
2. **VERIFY END-TO-END before saying done / LIVE / shipped.** Prove the USER-FACING result (public endpoint, real install, actual traffic) — not "it compiled / deployed". If you can't fully verify, say so explicitly; never round up to "done". (Things have sat broken for days while memory said "LIVE".)
3. **USE YOUR FULL STRENGTHS.** For substantive work use parallel subagents / the **Workflow** tool — don't grind everything sequentially. Navigate code via the knowledge **graph** + **memory**, not blind file-reading. Proactively monitor (logs/telemetry) instead of waiting to be told. Don't hand-do what a tool does better.
4. **PERSIST EVERYTHING DURABLE, THE SAME TURN.** Write new facts/decisions to memory immediately; keep the in-flight handoff file current so the next chat (or post-compaction you) continues seamlessly. The owner should NEVER have to re-explain the project or remind you.
5. **ACT DECISIVELY.** Pick the sensible default and state it; don't pepper the owner with confirmation dialogs; don't end messages with "tell me if it's wrong" — verify it yourself. Don't make the owner your QA.
6. **THIS OWNER:** reply in **Russian**; distribute heavy work across the available servers (don't overload one); ⛔ HARD RULE — never harm a live paying client.

Memory + orientation for the MaestroVPN project live in `/root/.claude/projects/-root-maestrovpn-tv/memory/` (index `MEMORY.md`), the live state in `/root/.claude/maestro-state.md`, the infra map in `/root/.claude/maestro-infra.md`; orient via `bash /root/.claude/maestro-orient.sh`.

# graphify
- **graphify** (`~/.claude/skills/graphify/SKILL.md`) - any input to knowledge graph. Trigger: `/graphify`
When the user types `/graphify`, invoke the Skill tool with `skill: "graphify"` before doing anything else.
