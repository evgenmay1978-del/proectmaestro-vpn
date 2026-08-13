import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
ROOM = ROOT / "ops" / "olcrtc-room.sh"
HEALTH = ROOT / "ops" / "olcrtc-health.sh"
LIB = ROOT / "ops" / "olcrtc-ssh-config.sh"


class OlcRtcOpsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.calls = self.base / "calls"
        self.identity = self.base / "s3-key"
        self.known_hosts = self.base / "known-hosts"
        self.panel_env = self.base / "panel.env"
        self.wb_token = self.base / "wb.token"
        self.config = self.base / "olc.env"
        self.health = self.base / "health.json"
        self.identity.write_text("test-private-key\n", encoding="utf-8")
        self.known_hosts.write_text("s3.example.invalid ssh-ed25519 TEST\n", encoding="utf-8")
        self.panel_env.write_text("MAESTRO_ADMIN_TOKEN=test-admin-token\n", encoding="utf-8")
        self.wb_token.write_text("secret-wb-marker\n", encoding="utf-8")
        for path in (self.identity, self.known_hosts, self.panel_env, self.wb_token):
            path.chmod(0o600)
        self.config.write_text(textwrap.dedent(f"""\
            S3_HOST=s3.example.invalid
            S3_USER=root
            S3_IDENTITY_FILE={self.identity}
            S3_KNOWN_HOSTS_FILE={self.known_hosts}
            PANEL_URL=http://127.0.0.1:8910
            PANEL_ENV_FILE={self.panel_env}
            WB_TOKEN_FILE={self.wb_token}
            OLC_HEALTH_FILE={self.health}
        """), encoding="utf-8")
        self.config.chmod(0o600)
        self._write_fake("ssh", r"""#!/bin/sh
set -eu
phase=unknown
strict=0
identity=0
known=0
for arg in "$@"; do
  case "$arg" in
    *maestro-phase=preflight*) phase=preflight ;;
    *maestro-phase=stage*) phase=stage ;;
    *maestro-phase=restart*) phase=restart ;;
    *maestro-phase=verify*) phase=verify ;;
    *maestro-phase=rollback*) phase=rollback ;;
    *maestro-phase=commit*) phase=commit ;;
    *maestro-phase=health*) phase=health ;;
    StrictHostKeyChecking=yes) strict=1 ;;
    UserKnownHostsFile=*) known=1 ;;
    *s3-key) identity=1 ;;
  esac
done
if [ "$strict" = 1 ] && [ "$known" = 1 ] && [ "$identity" = 1 ]; then
  printf 'ssh:strict:%s\n' "$phase" >> "$FAKE_CALLS"
else
  printf 'ssh:unsafe:%s\n' "$phase" >> "$FAKE_CALLS"
fi
cat >/dev/null || true
if [ "${FAKE_SSH_FAIL_PHASE:-}" = "$phase" ]; then exit 41; fi
if [ "$phase" = health ]; then printf 'owner active 1\n'; fi
""")
        self._write_fake("curl", r"""#!/bin/sh
set -eu
is_post=0
out=""
write_code=""
prev=""
for arg in "$@"; do
  [ "$prev" = "-o" ] && out="$arg"
  [ "$prev" = "-w" ] && write_code="$arg"
  [ "$prev" = "-X" ] && [ "$arg" = POST ] && is_post=1
  case "$arg" in http://*|https://*) url="$arg" ;; esac
  prev="$arg"
done
if [ "$is_post" = 1 ]; then
  printf 'curl:panel-post\n' >> "$FAKE_CALLS"
  status="${FAKE_PANEL_POST_STATUS:-200}"
  [ -z "$out" ] || printf '{"ok":true}\n' > "$out"
  [ -z "$write_code" ] || printf '%s' "$status"
  exit 0
fi
printf 'curl:panel-get\n' >> "$FAKE_CALLS"
printf '{"rooms":{"owner":{"room":"old-room","key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}\n'
""")
        self._write_fake("openssl", r"""#!/bin/sh
printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n'
""")

    def _write_fake(self, name, body):
        path = self.bin / name
        path.write_text(body, encoding="utf-8")
        path.chmod(0o755)

    def env(self, **extra):
        env = os.environ.copy()
        env.update({
            "PATH": str(self.bin) + os.pathsep + env.get("PATH", ""),
            "FAKE_CALLS": str(self.calls),
            "MAESTRO_OLCRTC_ENV_FILE": str(self.config),
            "MAESTRO_OLCRTC_LIB": str(LIB),
            "OLC_HEALTH_FILE": str(self.health),
            "TMPDIR": str(self.base),
        })
        env.update(extra)
        return env

    def run_room(self, **extra):
        return subprocess.run(
            ["sh", str(ROOM), "owner", "new-room", "wbstream"],
            cwd=ROOT, env=self.env(**extra), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10,
        )

    def call_lines(self):
        if not self.calls.exists():
            return []
        return self.calls.read_text(encoding="utf-8").splitlines()

    def test_room_is_published_only_after_remote_join(self):
        result = self.run_room()
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.call_lines()
        self.assertLess(calls.index("ssh:strict:verify"), calls.index("curl:panel-post"))
        self.assertEqual(calls[-1], "ssh:strict:commit")
        self.assertNotIn("ssh:unsafe", "\n".join(calls))

    def test_remote_verification_failure_never_posts_panel(self):
        result = self.run_room(FAKE_SSH_FAIL_PHASE="verify")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertNotIn("curl:panel-post", calls)
        self.assertIn("ssh:strict:rollback", calls)

    def test_panel_failure_restores_previous_remote_config(self):
        result = self.run_room(FAKE_PANEL_POST_STATUS="502")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ssh:strict:rollback", self.call_lines())

    def test_secret_is_never_printed(self):
        result = self.run_room(FAKE_PANEL_POST_STATUS="502")
        combined = result.stdout + "\n".join(self.call_lines())
        self.assertNotIn("secret-wb-marker", combined)
        self.assertNotIn("test-admin-token", combined)

    def test_health_uses_strict_ssh_and_atomic_snapshot(self):
        result = subprocess.run(
            ["sh", str(HEALTH)], cwd=ROOT, env=self.env(), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stdout)
        snapshot = json.loads(self.health.read_text(encoding="utf-8"))
        self.assertTrue(snapshot["exits"]["owner"]["healthy"])
        self.assertEqual(self.call_lines(), ["ssh:strict:health"])
        self.assertEqual(stat.S_IMODE(self.health.stat().st_mode) & 0o077, 0)


if __name__ == "__main__":
    unittest.main()
