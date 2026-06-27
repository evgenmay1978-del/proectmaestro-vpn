#!/bin/bash
# PreToolUse hook (matcher: AskUserQuestion) — HARD-BLOCKS the pop-up confirmation dialog.
# The owner has said many times NOT to pepper him with dialog widgets. Exit 2 = the harness
# denies the tool call and feeds this message back to the model.
echo "BLOCKED: AskUserQuestion (pop-up confirmation dialogs) is DISABLED by the owner — he has repeatedly told you not to use it. Do NOT retry the dialog. Decide yourself: pick the sensible default, state your choice + the reasoning in a normal prose message, and proceed. If a decision GENUINELY only he can make (money / irreversible / true either-or), ask it as ONE short plain sentence inside your reply — never as a dialog widget." >&2
exit 2
