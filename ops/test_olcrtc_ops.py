import json
import os
from pathlib import Path
import shutil
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
        self.real_stat = shutil.which("stat")
        self.assertIsNotNone(self.real_stat)
        self._write_fake("id", r"""#!/bin/sh
if [ "${1:-}" = "-u" ]; then printf '0\n'; exit 0; fi
exit 64
""")
        self._write_fake("stat", r"""#!/bin/sh
if [ "${1:-}" = "-c" ] && [ "${2:-}" = "%u" ]; then
  printf '%s\n' "${FAKE_STAT_UID:-0}"
  exit 0
fi
exec "$REAL_STAT" "$@"
""")
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
case "$phase" in
  stage|restart|verify|rollback|commit)
    case " $* " in
      *"/run/maestro-olcrtc-owner.lock"*|*"/run/maestro-olcrtc-global.lock"*) ;;
      *) printf 'ssh:unsafe-lock:%s\n' "$phase" >> "$FAKE_CALLS" ;;
    esac
    ;;
esac
if [ "$phase" = stage ]; then
  case " $* " in
    *"trap"*) ;;
    *) printf 'ssh:unsafe-stage-recovery\n' >> "$FAKE_CALLS" ;;
  esac
  if [ "${FAKE_STAGE_ROLLBACK_RESTORE_FAIL:-}" = 1 ]; then
    case " $* " in
      *'systemctl restart "$unit" >/dev/null 2>&1 || true'*|*'systemctl stop "$unit" >/dev/null 2>&1 || true'*)
        printf 'ssh:rollback-lock-removed-after-restore-failure\n' >> "$FAKE_CALLS" ;;
        ;;
    esac
  fi
fi
if [ "$phase" = verify ]; then
  case " $* " in
    *"sleep 5"*) ;;
    *) printf 'ssh:unsafe-verify-retry\n' >> "$FAKE_CALLS" ;;
  esac
fi
if [ "$phase" = rollback ]; then
  case " $* " in
    *'! -d "$lock"'*|*'[ -d "$lock" ] || exit 0'*) ;;
    *) printf 'ssh:unsafe-rollback\n' >> "$FAKE_CALLS" ;;
  esac
fi
if [ "$strict" = 1 ] && [ "$known" = 1 ] && [ "$identity" = 1 ]; then
  printf 'ssh:strict:%s\n' "$phase" >> "$FAKE_CALLS"
else
  printf 'ssh:unsafe:%s\n' "$phase" >> "$FAKE_CALLS"
fi
cat >/dev/null || true
if [ "${FAKE_SSH_FAIL_PHASE_ONCE:-}" = "$phase" ] && [ ! -f "${FAKE_SSH_ONCE_MARKER:?}" ]; then
  : > "$FAKE_SSH_ONCE_MARKER"
  exit 41
fi
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
  if [ "${FAKE_PANEL_POST_RESPONSE_LOST:-}" = 1 ]; then
    : > "${FAKE_PANEL_COMMITTED:?}"
    exit 56
  fi
  if [ "${FAKE_PANEL_POST_RESPONSE_AMBIGUOUS:-}" = 1 ]; then
    : > "${FAKE_PANEL_AMBIGUOUS:?}"
    exit 56
  fi
  exit 0
fi
printf 'curl:panel-get\n' >> "$FAKE_CALLS"
if [ -f "${FAKE_PANEL_COMMITTED:-/nonexistent}" ]; then
  printf '{"key":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","room":"global-old","rooms":{"owner":{"room":"new-room","key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider":"wbstream"}}}\n'
  exit 0
fi
if [ -f "${FAKE_PANEL_AMBIGUOUS:-/nonexistent}" ]; then printf '{broken-json\n'; exit 0; fi
if [ "${FAKE_PANEL_GET_MALFORMED:-}" = 1 ]; then printf '{broken-json\n'; exit 0; fi
printf '{"key":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","room":"global-old","rooms":{"owner":{"room":"old-room","key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}\n'
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
            "REAL_STAT": str(self.real_stat),
            "FAKE_SSH_ONCE_MARKER": str(self.base / "ssh-once"),
            "FAKE_PANEL_COMMITTED": str(self.base / "panel-committed"),
            "FAKE_PANEL_AMBIGUOUS": str(self.base / "panel-ambiguous"),
        })
        env.update(extra)
        return env

    def run_room(self, **extra):
        return subprocess.run(
            ["sh", str(ROOM), "owner", "new-room", "wbstream"],
            cwd=ROOT, env=self.env(**extra), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10,
        )

    def run_global(self, **extra):
        return subprocess.run(
            ["sh", str(ROOM), "https://telemost.example.invalid/global"],
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

    def test_group_writable_config_is_rejected_before_network(self):
        self.config.chmod(0o620)
        result = self.run_room()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.call_lines(), [])

    def test_stage_has_remote_recovery_before_panel(self):
        result = self.run_room(FAKE_SSH_FAIL_PHASE="stage")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertNotIn("ssh:unsafe-stage-recovery", calls)
        self.assertNotIn("curl:panel-post", calls)

    def test_global_room_update_is_transactional(self):
        result = self.run_global()
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.call_lines()
        self.assertLess(calls.index("ssh:strict:verify"), calls.index("curl:panel-post"))
        self.assertEqual(calls[-1], "ssh:strict:commit")

    def test_commit_cleanup_failure_does_not_rollback_published_room(self):
        result = self.run_room(FAKE_SSH_FAIL_PHASE="commit")
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.call_lines()
        self.assertIn("curl:panel-post", calls)
        self.assertNotIn("ssh:strict:rollback", calls)

    def test_malformed_panel_state_fails_before_remote_stage(self):
        result = self.run_room(FAKE_PANEL_GET_MALFORMED="1")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertNotIn("ssh:strict:stage", calls)
        self.assertNotIn("curl:panel-post", calls)

    def test_secret_boundary_files_reject_unsafe_modes(self):
        for path in (self.identity, self.known_hosts, self.panel_env):
            with self.subTest(path=path.name):
                path.chmod(0o620)
                result = self.run_room()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.call_lines(), [])
                self.calls.unlink(missing_ok=True)
                path.chmod(0o600)

    def test_secret_files_require_root_only_modes(self):
        for mode in (0o640, 0o604, 0o644):
            with self.subTest(mode=oct(mode)):
                self.config.chmod(mode)
                result = self.run_room()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.call_lines(), [])
                self.calls.unlink(missing_ok=True)
                self.config.chmod(0o600)

    def test_secret_boundary_requires_effective_root(self):
        source = LIB.read_text(encoding="utf-8")
        self.assertIn('[ "$euid" -eq 0 ]', source)

    def test_secret_boundary_rejects_owner_mismatch(self):
        result = self.run_room(FAKE_STAT_UID="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.call_lines(), [])

    def test_panel_commit_with_lost_response_is_reconciled_without_rollback(self):
        result = self.run_room(FAKE_PANEL_POST_RESPONSE_LOST="1")
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.call_lines()
        self.assertGreaterEqual(calls.count("curl:panel-get"), 2)
        self.assertNotIn("ssh:strict:rollback", calls)

    def test_ambiguous_panel_result_preserves_verified_remote_state_and_lock(self):
        result = self.run_room(FAKE_PANEL_POST_RESPONSE_AMBIGUOUS="1")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertGreaterEqual(calls.count("curl:panel-get"), 2)
        self.assertNotIn("ssh:strict:rollback", calls)
        self.assertNotIn("ssh:strict:commit", calls)

    def test_commit_cleanup_retries_and_next_update_is_not_blocked(self):
        first = self.run_room(FAKE_SSH_FAIL_PHASE_ONCE="commit")
        self.assertEqual(first.returncode, 0, first.stdout)
        self.assertGreaterEqual(self.call_lines().count("ssh:strict:commit"), 2)
        second = self.run_room()
        self.assertEqual(second.returncode, 0, second.stdout)

    def test_stage_restore_failure_keeps_recovery_lock(self):
        result = self.run_room(
            FAKE_SSH_FAIL_PHASE="stage",
            FAKE_STAGE_ROLLBACK_RESTORE_FAIL="1",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(
            "ssh:rollback-lock-removed-after-restore-failure",
            self.call_lines(),
        )

    def test_loopback_url_rejects_userinfo_host_confusion(self):
        content = self.config.read_text(encoding="utf-8")
        self.config.write_text(
            content.replace(
                "PANEL_URL=http://127.0.0.1:8910",
                "PANEL_URL=http://127.0.0.1:8910@evil.invalid:80",
            ),
            encoding="utf-8",
        )
        self.config.chmod(0o600)
        result = self.run_room()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.call_lines(), [])

    def test_token_is_not_passed_in_process_arguments(self):
        source = ROOM.read_text(encoding="utf-8")
        self.assertNotIn('"$TOKEN"', source)
        self.assertNotIn("olc_panel_token", source)

    def test_remote_state_snapshots_enablement_and_backup_before_publish(self):
        source = ROOM.read_text(encoding="utf-8")
        self.assertIn("previous-enabled", source)
        self.assertIn("systemctl is-enabled", source)
        self.assertNotIn("systemctl enable 'olcrtc-srv@$LOGIN' >/dev/null 2>&1 || true", source)
        self.assertIn("previous-exists", source)

    def test_remote_verification_failure_never_posts_panel(self):
        result = self.run_room(FAKE_SSH_FAIL_PHASE="verify")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertNotIn("curl:panel-post", calls)
        self.assertIn("ssh:strict:rollback", calls)

    def test_panel_failure_restores_previous_remote_config(self):
        result = self.run_room(FAKE_PANEL_POST_STATUS="502")
        self.assertNotEqual(result.returncode, 0)
        calls = self.call_lines()
        self.assertIn("ssh:strict:rollback", calls)
        self.assertNotIn("ssh:unsafe-rollback", calls)

    def test_secret_is_never_printed(self):
        result = self.run_room(FAKE_PANEL_POST_STATUS="502")
        combined = result.stdout + "\n".join(self.call_lines())
        self.assertNotIn("secret-wb-marker", combined)
        self.assertNotIn("test-admin-token", combined)

    def test_health_marks_ssh_probe_failure(self):
        result = subprocess.run(
            ["sh", str(HEALTH)], cwd=ROOT,
            env=self.env(FAKE_SSH_FAIL_PHASE="health"), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stdout)
        snapshot = json.loads(self.health.read_text(encoding="utf-8"))
        self.assertFalse(snapshot["probe_ok"])
        self.assertEqual(snapshot["error"], "ssh_unavailable")
        self.assertEqual(snapshot["exits"], {})

    def test_health_uses_strict_ssh_and_atomic_snapshot(self):
        result = subprocess.run(
            ["sh", str(HEALTH)], cwd=ROOT, env=self.env(), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stdout)
        snapshot = json.loads(self.health.read_text(encoding="utf-8"))
        self.assertTrue(snapshot["probe_ok"])
        self.assertEqual(snapshot["error"], "")
        self.assertTrue(snapshot["exits"]["owner"]["healthy"])
        self.assertEqual(self.call_lines(), ["ssh:strict:health"])
        self.assertEqual(stat.S_IMODE(self.health.stat().st_mode) & 0o077, 0)


if __name__ == "__main__":
    unittest.main()
