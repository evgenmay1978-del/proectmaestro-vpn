from pathlib import Path
import unittest

from ops.yandex_cdn_commercial import render_ingress


ASSET_ROOT = Path(__file__).parents[1]


def canary(path: str = "/private/path", host: str = "cdn.example.test") -> dict:
    return {
        "inbounds": [
            {
                "protocol": "vless",
                "port": 18081,
                "streamSettings": {
                    "network": "xhttp",
                    "security": "none",
                    "xhttpSettings": {"host": host, "path": path},
                },
            }
        ]
    }


def material(path: str = "/commercial/path", host: str = "cdn.example.test") -> dict:
    return {
        "active_origin_ips": ["192.0.2.1"],
        "controller_source_ip": "192.0.2.2",
        "managed_credentials": [],
        "public_host": host,
        "relay_routes": [],
        "secret_path": path,
        "server_decryption": "example",
    }


class IngressRendererTests(unittest.TestCase):
    def test_render_preserves_private_and_commercial_requests_without_uri_rewrite(self) -> None:
        template = (ASSET_ROOT / "templates" / "ingress-nginx.conf.tmpl").read_text(encoding="utf-8")

        rendered = render_ingress.render(canary(), material(), template).decode("utf-8")

        self.assertIn("location = /private/path", rendered)
        self.assertIn("location ^~ /private/path/", rendered)
        self.assertIn("proxy_pass http://127.0.0.1:18081;", rendered)
        self.assertIn("location = /commercial/path", rendered)
        self.assertIn("location ^~ /commercial/path/", rendered)
        self.assertIn("proxy_pass http://127.0.0.1:28081;", rendered)
        self.assertIn("location / { return 404; }", rendered)
        self.assertNotIn("rewrite ", rendered)

    def test_render_rejects_overlapping_routes_and_host_mismatch(self) -> None:
        template = (ASSET_ROOT / "templates" / "ingress-nginx.conf.tmpl").read_text(encoding="utf-8")

        with self.assertRaisesRegex(render_ingress.Refused, "route_paths_overlap"):
            render_ingress.render(canary("/private"), material("/private/child"), template)
        with self.assertRaisesRegex(render_ingress.Refused, "shared_host_mismatch"):
            render_ingress.render(canary(), material(host="other.example.test"), template)

    def test_service_is_isolated_from_default_nginx_unit(self) -> None:
        unit = (ASSET_ROOT / "templates" / "maestro-cdn-ingress.service").read_text(encoding="utf-8")

        self.assertIn("User=www-data", unit)
        self.assertIn("ExecStartPre=/usr/sbin/nginx -t -q -c /etc/maestro-cdn-ingress/nginx.conf", unit)
        self.assertIn('ExecStart=/usr/sbin/nginx -c /etc/maestro-cdn-ingress/nginx.conf -g "daemon off;"', unit)
        self.assertNotIn("nginx.service", unit)


if __name__ == "__main__":
    unittest.main()
