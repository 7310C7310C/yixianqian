#!/usr/bin/env python3
"""把本机未部署的前端资源替换进 staging 页面（接口仍走 staging）。

用途：批次7 A2（app.js → app.min.js）在**部署前**做 A/B 量测与回归，只需替换
文档外壳与主脚本，其余静态资源（Vue/Vant/render chunk/图片）与全部 API 仍来自
真实 staging。

用法（在 smoke/量测脚本里）：
    from local_assets import Arm, install
    arm = Arm(name="treatment", shell_html=..., script_name="app.min.js", script_path=Path(...))
    install(context, base_url, arm)

限制（务必写进报告）：
- `route.fulfill` 的响应不经过网络频宽模拟，`transferSize` 会是 0；所以本 helper
  只能用来比较 CPU/解析/执行差异，**不能用它报首载字节或 TTFB**。
- 外壳里的 `__ASSET_URL_VERSION__` 占位符不会被替换（staging 服务端才会替换）：
  只影响缓存键，不影响渲染；两臂同等对待。
- `window.__YXQ_AUTHENTICATED__=false;` 必须改写成 true，否则 SPA 走客户端登录流程。
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse

AUTH_FLAG = "window.__YXQ_AUTHENTICATED__=false;"
AUTH_FLAG_ON = "window.__YXQ_AUTHENTICATED__=true;"


@dataclass(frozen=True)
class Arm:
    name: str
    shell_html: str
    script_name: str      # 例如 "app.js" / "app.min.js"
    script_bytes: bytes

    @classmethod
    def from_files(cls, name: str, shell: Path, script: Path) -> "Arm":
        """shell 与 script 搭配；若 shell 引用的是另一个 app*.js，按 script 名改写。

        改写只动 script src / preload 的档名，让同一份壳可以分别跑 app.js（control）
        与 app.min.js（treatment），避免两臂 HTML 出现与压缩无关的差异。
        """
        html = shell.read_text(encoding="utf-8")
        if AUTH_FLAG not in html:
            raise SystemExit(f"{shell} 缺少 {AUTH_FLAG}，无法改写登录标记")
        if f'src="{script.name}' not in html:
            html = re.sub(r'src="app(?:\.min)?\.js', f'src="{script.name}', html)
            html = re.sub(r'href="app(?:\.min)?\.js', f'href="{script.name}', html)
        if f'src="{script.name}' not in html:
            raise SystemExit(f"{shell} 无法改写成引用 {script.name}")
        return cls(name=name, shell_html=html.replace(AUTH_FLAG, AUTH_FLAG_ON),
                   script_name=script.name, script_bytes=script.read_bytes())


def install(context, base_url: str, arm: Arm) -> None:
    """注册路由：/ 用本地外壳，/static/legacy/<script> 用本地脚本，其余走网络。"""
    root = base_url.rstrip("/")
    script_path = "/static/legacy/" + arm.script_name

    def handler(route, request):
        try:
            parsed = urlparse(request.url)
            if parsed.netloc != urlparse(root).netloc:
                route.fallback()
                return
            if request.resource_type == "document" and parsed.path.rstrip("/") in ("", "/app"):
                route.fulfill(status=200, content_type="text/html; charset=utf-8", body=arm.shell_html)
                return
            if parsed.path == script_path:
                route.fulfill(
                    status=200,
                    content_type="application/javascript; charset=utf-8",
                    body=arm.script_bytes,
                )
                return
        except Exception as exc:  # noqa: BLE001 - 路由异常要冒出来，不能静默放行
            route.abort()
            raise SystemExit(f"local_assets route failed for {request.url}: {exc}") from exc
        route.fallback()

    context.route("**/*", handler)
