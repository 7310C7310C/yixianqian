"""性能护栏自测：预算突变必红、nginx 指令删除必红。

运行：
  python3 -m unittest discover -s tools -p 'test_perf_guardrails.py' -v
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PERF = ROOT / "tools/perf"
sys.path.insert(0, str(PERF))

import check_budget  # noqa: E402
import check_nginx_guard  # noqa: E402


class BudgetTests(unittest.TestCase):
    def run_budget(self, *extra: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, str(PERF / "check_budget.py"), *extra],
            cwd=ROOT, capture_output=True, text=True,
        )

    def test_current_tree_passes(self) -> None:
        result = self.run_budget()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("PASS", result.stdout)
        self.assertIn("first_load_static_gzip", result.stdout)

    def test_over_budget_is_red(self) -> None:
        with tempfile.TemporaryDirectory(prefix="yxq-budget-") as tmp:
            path = Path(tmp) / "budgets.json"
            data = json.loads((PERF / "budgets.json").read_text(encoding="utf-8"))
            data["limits"]["app_min_gzip"]["baseline_bytes"] = 1
            data["limits"]["app_min_gzip"]["budget_bytes"] = 1
            path.write_text(json.dumps(data), encoding="utf-8")
            result = self.run_budget("--budgets", str(path))
            self.assertEqual(result.returncode, 1)
            self.assertIn("OVER app_min_gzip", result.stderr)

    def test_missing_metric_is_red(self) -> None:
        with tempfile.TemporaryDirectory(prefix="yxq-budget-") as tmp:
            path = Path(tmp) / "budgets.json"
            data = json.loads((PERF / "budgets.json").read_text(encoding="utf-8"))
            del data["limits"]["first_load_static_gzip"]
            path.write_text(json.dumps(data), encoding="utf-8")
            result = self.run_budget("--budgets", str(path))
            self.assertEqual(result.returncode, 1)
            self.assertIn("first_load_static_gzip", result.stderr)

    def test_update_flag_is_rejected(self) -> None:
        result = self.run_budget("--update")
        self.assertEqual(result.returncode, 2)
        self.assertIn("unrecognized arguments", result.stderr)


class NginxGuardTests(unittest.TestCase):
    def mutate(self, rel: str, old: str, new: str) -> list[tuple[str, str]]:
        with tempfile.TemporaryDirectory(prefix="yxq-nginx-") as tmp:
            root = Path(tmp)
            shutil.copytree(ROOT / "deploy/nginx", root / "deploy/nginx")
            target = root / "deploy/nginx" / rel
            text = target.read_text(encoding="utf-8")
            self.assertIn(old, text, f"突变片段不存在：{rel}: {old!r}")
            target.write_text(text.replace(old, new, 1), encoding="utf-8")
            _, failed = check_nginx_guard.run_checks(root)
            return failed

    def test_current_config_passes(self) -> None:
        passed, failed = check_nginx_guard.run_checks(ROOT)
        self.assertFalse(failed, failed)
        self.assertGreaterEqual(len(passed), 15)

    def test_removing_keepalive_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "keepalive 64;", "")
        self.assertTrue(any("keepalive" in label for label, _ in failed), failed)

    def test_changing_keepalive_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "keepalive 64;", "keepalive 32;")
        self.assertTrue(any("keepalive" in label for label, _ in failed), failed)

    def test_removing_static_cache_is_red(self) -> None:
        failed = self.mutate("snippets/relay-next-proxy.conf", "proxy_cache yxqstatic;", "")
        self.assertTrue(any("静态边缘缓存" in label for label, _ in failed), failed)

    def test_removing_encoding_key_is_red(self) -> None:
        failed = self.mutate(
            "snippets/relay-next-proxy.conf",
            'proxy_cache_key "$scheme$host$request_uri$yxq_static_enc";',
            'proxy_cache_key "$scheme$host$request_uri";',
        )
        self.assertTrue(any("静态边缘缓存" in label for label, _ in failed), failed)

    def test_removing_tls_ticket_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "ssl_session_tickets on;", "")
        self.assertTrue(any("TLS" in label for label, _ in failed), failed)

    def test_ssl_session_cache_none_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "ssl_session_cache shared:YXQNEXT:20m;", "ssl_session_cache none;")
        self.assertTrue(any("TLS" in label for label, _ in failed), failed)

    def test_proxy_ssl_session_reuse_off_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "proxy_ssl_session_reuse on;", "proxy_ssl_session_reuse off;")
        self.assertTrue(any("proxy_ssl_session_reuse" in label for label, _ in failed), failed)

    def test_removing_gzip_on_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "gzip on;", "")
        self.assertTrue(any("gzip on" in label for label, _ in failed), failed)

    def test_static_without_http11_is_red(self) -> None:
        failed = self.mutate(
            "snippets/relay-next-proxy.conf",
            'proxy_set_header Connection "";\n\n    proxy_hide_header Strict-Transport-Security;\n    proxy_http_version 1.1;',
            'proxy_set_header Connection "";\n\n    proxy_hide_header Strict-Transport-Security;',
        )
        self.assertTrue(any("静态边缘缓存" in label for label, _ in failed), failed)

    def test_removing_sse_buffering_exception_is_red(self) -> None:
        failed = self.mutate("snippets/relay-next-proxy.conf", "proxy_buffering off;", "")
        self.assertTrue(any("SSE" in label for label, _ in failed), failed)

    def test_removing_temp_file_cap_is_red(self) -> None:
        failed = self.mutate("next.nantou.love.conf", "proxy_max_temp_file_size 0;", "")
        self.assertTrue(any("源站" in label for label, _ in failed), failed)

    def test_removing_gzip_js_type_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", " text/javascript", "")
        self.assertTrue(any("gzip_types" in label for label, _ in failed), failed)

    def test_removing_hsts_is_red(self) -> None:
        failed = self.mutate(
            "relay-next.conf",
            'add_header Strict-Transport-Security "max-age=31536000" always;',
            "",
        )
        self.assertTrue(any("HSTS" in label for label, _ in failed), failed)

    def test_removing_connection_reuse_is_red(self) -> None:
        failed = self.mutate(
            "snippets/relay-next-proxy.conf",
            "proxy_set_header Connection $yxq_conn;",
            'proxy_set_header Connection "";',
        )
        self.assertTrue(any("keepalive" in label for label, _ in failed), failed)

    def test_api_edge_cache_is_red(self) -> None:
        failed = self.mutate(
            "relay-next.conf",
            "server {",
            'server { location /legacy/api/ { proxy_cache yxqstatic; '
            'proxy_cache_key "$scheme$host$cookie_yxq_session"; } ',
        )
        self.assertTrue(any("API 边缘缓存" in label for label, _ in failed), failed)

    def test_non_sse_buffering_off_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "location / {", "location / { proxy_buffering off; ")
        self.assertTrue(any("proxy_buffering" in label for label, _ in failed), failed)

    def test_server_level_cache_is_red(self) -> None:
        failed = self.mutate(
            "relay-next.conf",
            "server_name next.nantou.love;",
            "server_name next.nantou.love; proxy_cache yxqstatic;",
        )
        self.assertTrue(any("API 边缘缓存" in label for label, _ in failed), failed)

    def test_http_root_cache_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "upstream yxq_origin {", "proxy_cache yxqstatic;\nupstream yxq_origin {")
        self.assertTrue(any("API 边缘缓存" in label for label, _ in failed), failed)

    def test_http_root_buffering_off_is_red(self) -> None:
        failed = self.mutate("relay-next.conf", "upstream yxq_origin {", "proxy_buffering off;\nupstream yxq_origin {")
        self.assertTrue(any("proxy_buffering" in label for label, _ in failed), failed)


if __name__ == "__main__":
    unittest.main()
