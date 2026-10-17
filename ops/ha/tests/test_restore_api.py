import os
import ssl
import tempfile
import unittest
from pathlib import Path

from ops.ha.restore_api import RestoreAPIError, inspect_empty, inspect_restored, load_sqlite


EMPTY_RESPONSE = b'{"results":[{"columns":["name"],"types":["text"]}]}'
LOAD_RESPONSE = b'{"results":[]}'


class FakeResponse:
    def __init__(self, body, status=200, content_length=None):
        self.status = status
        self._body = body
        self._content_length = len(body) if content_length is None else content_length

    def getheader(self, name):
        if name.lower() == "content-length":
            return str(self._content_length)
        return None

    def read(self, amount=-1):
        if amount < 0:
            return self._body
        return self._body[:amount]


class FakeConnection:
    def __init__(self, response, fail_request=False):
        self.response = response
        self.fail_request = fail_request
        self.requests = []
        self.closed = False

    def request(self, method, path, body=None, headers=None):
        self.requests.append((method, path, body, dict(headers or {})))
        if self.fail_request:
            raise OSError("synthetic transport marker")

    def getresponse(self):
        return self.response

    def close(self):
        self.closed = True


class ConnectionFactory:
    def __init__(self, responses, fail_request=False):
        self.responses = list(responses)
        self.fail_request = fail_request
        self.calls = []
        self.connections = []

    def __call__(self, host, port, *, context, timeout):
        self.calls.append((host, port, context, timeout))
        response = self.responses[len(self.connections)]
        connection = FakeConnection(response, self.fail_request)
        self.connections.append(connection)
        return connection


class RestoreAPITests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        for name in ("ca.crt", "client.crt", "client.key"):
            path = self.root / name
            path.write_text("synthetic fixture", encoding="utf-8")
            os.chmod(path, 0o600)
        self.config = {
            "format_version": 1,
            "nodes": [
                {"node_id": "S2", "endpoint": "https://127.0.0.1:4401"},
                {"node_id": "S3", "endpoint": "https://127.0.0.1:4403"},
                {"node_id": "S4", "endpoint": "https://127.0.0.1:4405"},
            ],
            "ca": str(self.root / "ca.crt"),
            "client_cert": str(self.root / "client.crt"),
            "client_key": str(self.root / "client.key"),
        }
        self.context = object()
        self.context_calls = []

    def tearDown(self):
        self.temp.cleanup()

    def context_factory(self, ca, cert, key):
        self.context_calls.append((ca, cert, key))
        return self.context

    def test_inspect_empty_requires_three_exact_https_voters_and_mtls(self):
        factory = ConnectionFactory([FakeResponse(EMPTY_RESPONSE) for _ in range(3)])
        self.assertTrue(
            inspect_empty(
                self.config,
                connection_factory=factory,
                context_factory=self.context_factory,
            )
        )
        self.assertEqual(
            [(host, port) for host, port, _, _ in factory.calls],
            [("127.0.0.1", 4401), ("127.0.0.1", 4403), ("127.0.0.1", 4405)],
        )
        self.assertEqual(len(self.context_calls), 1)
        for connection in factory.connections:
            self.assertTrue(connection.closed)
            self.assertEqual(len(connection.requests), 1)
            method, path, body, headers = connection.requests[0]
            self.assertEqual(method, "POST")
            self.assertEqual(path, "/db/query?level=strong")
            self.assertIn(b"sqlite_master", body)
            self.assertEqual(headers["Content-Type"], "application/json")
            self.assertNotIn("Authorization", headers)

        invalid = dict(self.config)
        invalid["nodes"] = self.config["nodes"][:2]
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                invalid,
                connection_factory=ConnectionFactory([]),
                context_factory=self.context_factory,
            )
        invalid = dict(self.config)
        invalid["nodes"] = [
            {"node_id": "S2", "endpoint": "http://127.0.0.1:4401"},
            *self.config["nodes"][1:],
        ]
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                invalid,
                connection_factory=ConnectionFactory([]),
                context_factory=self.context_factory,
            )

    def test_inspect_nonempty_or_malformed_response_fails_closed(self):
        nonempty = b'{"results":[{"columns":["name"],"types":["text"],"values":[["customers"]]}]}'
        factory = ConnectionFactory(
            [FakeResponse(EMPTY_RESPONSE), FakeResponse(nonempty), FakeResponse(EMPTY_RESPONSE)]
        )
        self.assertFalse(
            inspect_empty(
                self.config,
                connection_factory=factory,
                context_factory=self.context_factory,
            )
        )
        malformed = ConnectionFactory([FakeResponse(b'{"results":[]}')])
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                self.config,
                connection_factory=malformed,
                context_factory=self.context_factory,
            )

    def test_load_uses_one_post_db_load_without_redirect_or_retry(self):
        image = self.root / "restore.sqlite3"
        image.write_bytes(b"SQLite format 3\x00" + (b"0" * 1024))
        os.chmod(image, 0o600)
        factory = ConnectionFactory([FakeResponse(LOAD_RESPONSE)])
        load_sqlite(
            self.config,
            image,
            connection_factory=factory,
            context_factory=self.context_factory,
        )
        self.assertEqual(len(factory.calls), 1)
        connection = factory.connections[0]
        self.assertTrue(connection.closed)
        self.assertEqual(len(connection.requests), 1)
        method, path, body, headers = connection.requests[0]
        self.assertEqual((method, path), ("POST", "/db/load"))
        self.assertEqual(body, image.read_bytes())
        self.assertEqual(headers["Content-Type"], "application/octet-stream")
        self.assertEqual(int(headers["Content-Length"]), image.stat().st_size)
        self.assertNotIn("Authorization", headers)

    def test_readback_requires_exact_schema_and_table_counts_on_all_voters(self):
        checksum = "a" * 64
        manifest = {
            "schema": {
                "version": 1,
                "checksum": "b" * 64,
                "migrations": [{"version": 1, "checksum": checksum}],
            },
            "table_counts": [{"table": "schema_migrations", "count": 1}],
        }
        readback = (
            '{"results":['
            '{"columns":["version","checksum"],"types":["integer","text"],'
            '"values":[[1,"' + checksum + '"]]},'
            '{"columns":["row_count"],"types":["integer"],"values":[[1]]}'
            ']}'
        ).encode()
        factory = ConnectionFactory([FakeResponse(readback) for _ in range(3)])
        inspect_restored(
            self.config,
            manifest,
            connection_factory=factory,
            context_factory=self.context_factory,
        )
        self.assertEqual(len(factory.calls), 3)
        for connection in factory.connections:
            self.assertEqual(len(connection.requests), 1)
            method, path, body, headers = connection.requests[0]
            self.assertEqual((method, path), ("POST", "/db/query?level=strong"))
            self.assertIn(b"schema_migrations", body)
            self.assertIn(b"COUNT(*)", body)
            self.assertNotIn("Authorization", headers)

        bad = readback.replace(b"[[1]]", b"[[2]]")
        with self.assertRaises(RestoreAPIError):
            inspect_restored(
                self.config,
                manifest,
                connection_factory=ConnectionFactory([FakeResponse(bad)]),
                context_factory=self.context_factory,
            )

    def test_transport_failure_is_unknown_outcome_and_never_replayed(self):
        image = self.root / "restore.sqlite3"
        image.write_bytes(b"SQLite format 3\x00" + (b"0" * 1024))
        os.chmod(image, 0o600)
        factory = ConnectionFactory([FakeResponse(LOAD_RESPONSE)], fail_request=True)
        with self.assertRaisesRegex(RestoreAPIError, "unknown-load-outcome"):
            load_sqlite(
                self.config,
                image,
                connection_factory=factory,
                context_factory=self.context_factory,
            )
        self.assertEqual(len(factory.calls), 1)
        self.assertEqual(len(factory.connections[0].requests), 1)

    def test_http_wrong_ca_missing_client_and_oversize_fail_closed(self):
        invalid = dict(self.config)
        invalid["client_key"] = str(self.root / "missing.key")
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                invalid,
                connection_factory=ConnectionFactory([]),
                context_factory=self.context_factory,
            )

        with self.assertRaises(RestoreAPIError):
            inspect_empty(self.config, connection_factory=ConnectionFactory([]))

        oversize = FakeResponse(EMPTY_RESPONSE, content_length=1_048_577)
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                self.config,
                connection_factory=ConnectionFactory([oversize]),
                context_factory=self.context_factory,
            )

        redirect = FakeResponse(b"", status=307)
        factory = ConnectionFactory([redirect])
        with self.assertRaises(RestoreAPIError):
            inspect_empty(
                self.config,
                connection_factory=factory,
                context_factory=self.context_factory,
            )
        self.assertEqual(len(factory.calls), 1)


if __name__ == "__main__":
    unittest.main()
