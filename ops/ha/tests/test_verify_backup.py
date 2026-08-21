import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest

from ops.ha.verify_backup import BackupVerificationError, build_manifest, verify_bundle


HEX_A = "a" * 64
HEX_B = "b" * 64
GIT_SHA = "a" * 40
SIGNER = "A" * 40
RECIPIENT = "B" * 40
MARKER = "SYNTHETIC-SECRET-MARKER"


class VerifyBackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.image = self.root / "control-plane.sqlite3"
        self.keys = self.root / "application-keys.json"
        self.gpg_home = self.root / "gnupg"
        self.gpg_home.mkdir(mode=0o700)
        self._create_image()
        self.keys.write_text('{"format_version":1,"keys":[]}', encoding="utf-8")
        os.chmod(self.keys, 0o600)
        self.metadata = {
            "format_version": 1,
            "repository_commit_sha": GIT_SHA,
            "workflow_run_id": 123456,
            "rqlite_version": "10.1.0",
            "created_at_utc": "2026-08-12T00:00:00Z",
            "signing_key_fingerprint": SIGNER,
            "recipient_key_fingerprint": RECIPIENT,
            "nodes": ["S2", "S3", "S4"],
        }

    def tearDown(self):
        self.temp.cleanup()

    def _create_image(self):
        db = sqlite3.connect(self.image)
        db.executescript(
            """
            PRAGMA foreign_keys=ON;
            CREATE TABLE schema_migrations(
              version INTEGER PRIMARY KEY, checksum TEXT NOT NULL
            );
            CREATE TABLE cluster_restore_state(
              singleton_id INTEGER PRIMARY KEY,
              cluster_id TEXT NOT NULL,
              restore_epoch INTEGER NOT NULL,
              restored_from_backup_sha256 TEXT,
              activated INTEGER NOT NULL,
              created_at_unix INTEGER NOT NULL,
              activated_at_unix INTEGER
            );
            CREATE TABLE import_runs(
              import_run_id TEXT PRIMARY KEY, completed_at_unix INTEGER
            );
            CREATE TABLE import_batches(
              import_run_id TEXT, batch_index INTEGER, applied_at_unix INTEGER
            );
            CREATE TABLE backup_watermarks(
              backup_id TEXT PRIMARY KEY,
              schema_version INTEGER NOT NULL,
              backup_sha256 TEXT NOT NULL,
              destination TEXT NOT NULL,
              status TEXT NOT NULL,
              created_at_unix INTEGER NOT NULL,
              verified_at_unix INTEGER
            );
            INSERT INTO schema_migrations VALUES(1, '""" + HEX_A + """');
            INSERT INTO schema_migrations VALUES(2, '""" + HEX_B + """');
            INSERT INTO cluster_restore_state
              VALUES(1, '""" + ("c" * 64) + """', 7, NULL, 1, 100, 100);
            INSERT INTO import_runs VALUES('run-2', 200);
            INSERT INTO import_batches VALUES('run-2', 3, 201);
            INSERT INTO backup_watermarks
              VALUES('backup-1', 2, '""" + ("d" * 64) + """', 'drill', 'verified', 202, 204);
            """
        )
        db.commit()
        db.close()
        os.chmod(self.image, 0o600)

    def _write_manifest(self, manifest):
        path = self.root / "manifest.json"
        path.write_text(
            json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
        os.chmod(path, 0o600)
        signature = self.root / "manifest.sig"
        signature.write_bytes(b"synthetic-detached-signature")
        os.chmod(signature, 0o600)

    def test_accepts_exact_full_git_sha1_identity(self):
        metadata = dict(self.metadata)
        metadata["repository_commit_sha"] = "d" * 40
        manifest = build_manifest(self.image, self.keys, metadata)
        self.assertEqual(
            manifest["repository_commit_sha"],
            "d" * 40,
        )

    def test_uses_real_backup_watermark_created_at_column(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self.assertEqual(
            manifest["receipts"]["backup_created_at_high_watermark"],
            202,
        )

    @staticmethod
    def _valid_gpg(command):
        del command
        return 0, f"[GNUPG:] VALIDSIG {SIGNER} 2026-08-12 0 4 0 1 10 00 {SIGNER}\n", ""

    def test_round_trip_manifest_is_canonical_and_image_derived(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self.assertEqual(manifest["format_version"], 1)
        self.assertEqual(manifest["source"]["restore_epoch"], 7)
        self.assertEqual(manifest["nodes"], ["S2", "S3", "S4"])
        self.assertEqual(
            manifest["image"]["sha256"],
            hashlib.sha256(self.image.read_bytes()).hexdigest(),
        )
        self.assertEqual(manifest["table_counts"], sorted(manifest["table_counts"], key=lambda item: item["table"]))
        encoded = json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n"
        self.assertNotIn(str(self.root), encoded)

    def test_rejects_unknown_missing_duplicate_or_noncanonical_manifest_field(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        manifest["unexpected"] = True
        self._write_manifest(manifest)
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)
        raw = (self.root / "manifest.json").read_text(encoding="utf-8")
        raw = raw.replace('"format_version":1', '"format_version":1,"format_version":1')
        (self.root / "manifest.json").write_text(raw, encoding="utf-8")
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)

    def test_rejects_missing_extra_link_or_ambiguous_member(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        extra = self.root / "extra.txt"
        extra.write_text("extra", encoding="utf-8")
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)
        extra.unlink()
        (self.root / "application-keys.json").unlink()
        os.symlink(self.image, self.root / "application-keys.json")
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)

    def test_rejects_wrong_hash_size_signature_or_signer(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        manifest["image"]["sha256"] = HEX_B
        self._write_manifest(manifest)
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, "C" * 40, self.gpg_home, run_gpg=self._valid_gpg)

    def test_rejects_schema_count_receipt_or_watermark_drift(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        manifest["schema"]["version"] = 1
        self._write_manifest(manifest)
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)
        manifest = build_manifest(self.image, self.keys, self.metadata)
        manifest["receipts"]["batch_index_high_watermark"] = 99
        self._write_manifest(manifest)
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)

    def test_rejects_sqlite_integrity_or_foreign_key_failure(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)
        with self.image.open("r+b") as handle:
            handle.seek(100)
            handle.write(b"corruption")
        with self.assertRaises(BackupVerificationError):
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=self._valid_gpg)

    def test_output_and_errors_exclude_markers_paths_and_gpg_output(self):
        manifest = build_manifest(self.image, self.keys, self.metadata)
        self._write_manifest(manifest)

        def hostile_gpg(command):
            del command
            return 2, "", MARKER + str(self.root)

        with self.assertRaises(BackupVerificationError) as caught:
            verify_bundle(self.root, SIGNER, self.gpg_home, run_gpg=hostile_gpg)
        rendered = str(caught.exception)
        self.assertNotIn(MARKER, rendered)
        self.assertNotIn(str(self.root), rendered)


if __name__ == "__main__":
    unittest.main()
