#!/usr/bin/env python3
"""一次性生成公开首页二维码 web/static/feishu-qr.png。

不参与构建、测试或运行时；只在维护二维码资产时手动执行。依赖
``qrcode[pil]``，故意不写入 ``tools/requirements*.txt``：

    python3 -m venv /tmp/opencode/qrvenv
    /tmp/opencode/qrvenv/bin/pip install 'qrcode[pil]'
    /tmp/opencode/qrvenv/bin/python tools/gen_qr.py

生成后必须用独立解码器校验，输出必须与页面按钮完全一致：

    zbarimg -q --raw web/static/feishu-qr.png
    # https://applink.feishu.cn/T996FhMkTERp
"""

from __future__ import annotations

import argparse
from pathlib import Path

# 必须与 internal/httpserver/handlers_home.go 的 publicFeishuAppURL 保持一致。
DEFAULT_URL = "https://applink.feishu.cn/T996FhMkTERp"
DEFAULT_OUT = Path(__file__).resolve().parent.parent / "web" / "static" / "feishu-qr.png"


def main() -> None:
    parser = argparse.ArgumentParser(description="生成公开首页飞书入口二维码（一次性工具）")
    parser.add_argument("--url", default=DEFAULT_URL, help="二维码内容，默认与页面按钮同一 applink")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="输出 PNG 路径")
    args = parser.parse_args()

    try:
        import qrcode
        from qrcode.constants import ERROR_CORRECT_M
    except ImportError as exc:  # pragma: no cover - 仅手动运行时触发
        raise SystemExit(
            "需要 qrcode[pil]：pip install 'qrcode[pil]'（一次性工具，不入项目依赖）"
        ) from exc

    qr = qrcode.QRCode(error_correction=ERROR_CORRECT_M, box_size=12, border=4)
    qr.add_data(args.url)
    qr.make(fit=True)
    img = qr.make_image(fill_color="black", back_color="white").convert("1")
    args.out.parent.mkdir(parents=True, exist_ok=True)
    img.save(args.out, format="PNG", optimize=True)
    print(f"wrote {args.out} ({img.size[0]}x{img.size[1]}) url={args.url}")
    print(f"verify: zbarimg -q --raw {args.out}")


if __name__ == "__main__":
    main()
