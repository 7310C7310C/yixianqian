#!/usr/bin/env python3
"""一次性引导层/遮罩处理，供浏览器 smoke 共用。

staging 真实环境里 `.swipe-guide-mask`（首次进入牵线的左右滑动引导）在首屏
约 500ms 后才渲染，脚本点得快时会被它盖住，Playwright 报 click timeout，
真实原因却是「引导层拦截」。这里统一：

- `dismiss_guides(scope)`：循环点 `.swipe-guide-mask .sg-btn`、更新说明/活动引导的
  「知道了」、残留 `.van-overlay`，直到 `.swipe-guide-mask` 消失或超时；
- `wait_guides_hidden(scope)`：显式等待引导层消失（带超时）；
- `wait_clickable(scope, locator)`：trial click 确认目标没有被遮罩挡住；
- `click(scope, locator)`：点击前清引导并等无遮挡，失败重试后报出遮挡元素；
- `wait_gone(scope, selector)`：等浮层（如 `.embed-overlay`）按动画退出。

`scope` 可以是 Playwright 的 Page 或 Frame。`.phone-pw-mask`、`.embed-overlay`、
`.van-dialog`、`.van-popup` 是脚本自己要操作的浮层：`dismiss_guides` 不会点掉它们，
`wait_clickable` 也只在目标被别的元素挡住时超时。
"""

from __future__ import annotations

import time

try:
    from playwright.sync_api import Error as PlaywrightError
except ImportError:  # pragma: no cover - 缺依赖时由入口脚本给出安装提示
    PlaywrightError = Exception  # type: ignore[assignment,misc]

GUIDE_MASK = ".swipe-guide-mask"
GUIDE_BUTTON = ".swipe-guide-mask .sg-btn"
ACK_TEXTS = ("我知道了", "知道了")
# 脚本自己要操作的浮层：不当作一次性引导清掉，避免误关业务弹层。
MANAGED_OVERLAYS = (".phone-pw-mask", ".embed-overlay", ".van-dialog", ".van-popup")
STRAY_OVERLAY = ".van-overlay"
BLOCKERS = (GUIDE_MASK, STRAY_OVERLAY, *MANAGED_OVERLAYS)


def _visible(locator) -> bool:
    try:
        return locator.count() > 0 and locator.first.is_visible()
    except PlaywrightError:
        return False


def _click_quietly(locator, timeout_ms: int = 1500, position: dict | None = None) -> bool:
    try:
        locator.click(timeout=timeout_ms, position=position)
        return True
    except PlaywrightError:
        return False


def visible_blockers(scope) -> list[str]:
    """返回当前可见的遮罩/引导层描述，用于失败诊断。"""
    found: list[str] = []
    for selector in BLOCKERS:
        locator = scope.locator(selector)
        try:
            if not locator.count() or not locator.first.is_visible():
                continue
            text = (locator.first.inner_text() or "").strip().replace("\n", " ")[:60]
        except PlaywrightError:
            continue
        found.append(f"{selector}={text!r}" if text else selector)
    return found


def wait_guides_hidden(scope, timeout_ms: int = 6000) -> bool:
    """等待 `.swipe-guide-mask` 消失；超时返回 False（元素不存在也算消失）。"""
    try:
        scope.wait_for_selector(GUIDE_MASK, state="hidden", timeout=timeout_ms)
        return True
    except PlaywrightError:
        return False


def dismiss_guides(scope, timeout_ms: int = 6000) -> bool:
    """循环关闭一次性引导层，返回 `.swipe-guide-mask` 是否已消失。

    依次处理：滑动引导按钮、「知道了」（更新说明/活动引导）、残留在最底层的
    裸 `.van-overlay`（只有没有业务浮层时才点，避免误关确认框）。
    """
    deadline = time.monotonic() + timeout_ms / 1000
    while True:
        if time.monotonic() >= deadline:
            return not _visible(scope.locator(GUIDE_MASK))
        acted = False
        button = scope.locator(GUIDE_BUTTON)
        if _visible(button):
            acted = _click_quietly(button.first, 1500) or acted
        for text in ACK_TEXTS:
            ack = scope.get_by_text(text, exact=True)
            try:
                count = ack.count()
            except PlaywrightError:
                count = 0
            for index in range(min(count, 3)):
                if _visible(ack.nth(index)):
                    acted = _click_quietly(ack.nth(index), 1500) or acted
                    break
        if not any(_visible(scope.locator(selector)) for selector in MANAGED_OVERLAYS):
            overlay = scope.locator(STRAY_OVERLAY)
            if _visible(overlay):
                # 点遮罩左上角：避开居中弹窗，只关掉没有业务内容的引导遮罩。
                acted = _click_quietly(overlay.first, 1200, {"x": 4, "y": 4}) or acted

        remaining_ms = int((deadline - time.monotonic()) * 1000)
        if not _visible(scope.locator(GUIDE_MASK)):
            if not acted:
                return True
            scope.wait_for_timeout(120)
            continue
        if remaining_ms <= 0:
            return False
        if acted and wait_guides_hidden(scope, min(2000, remaining_ms)):
            continue
        scope.wait_for_timeout(150)


def wait_gone(scope, selector: str, timeout_ms: int = 10000) -> bool:
    """等待浮层从 DOM 移除（Vant/Vue 离场动画结束后再点下一步）。"""
    try:
        scope.wait_for_selector(selector, state="detached", timeout=timeout_ms)
        return True
    except PlaywrightError:
        return False


def wait_clickable(scope, locator, timeout_ms: int = 8000) -> None:
    """确认目标可点：先清引导，再用 trial click 验证没有遮挡。"""
    deadline = time.monotonic() + timeout_ms / 1000
    last_error = None
    while time.monotonic() < deadline:
        dismiss_guides(scope, timeout_ms=min(2500, max(500, int((deadline - time.monotonic()) * 1000))))
        try:
            locator.click(trial=True, timeout=1200)
            return
        except PlaywrightError as exc:
            last_error = exc
            scope.wait_for_timeout(150)
    raise AssertionError(
        f"{timeout_ms}ms 内目标仍被遮挡：{last_error}；遮挡={visible_blockers(scope) or '无'}"
    )


def click(scope, locator, *, timeout_ms: int = 12000, label: str = "") -> None:
    """点击前清引导并等待无遮挡；仍失败时重试并报出遮挡元素。"""
    deadline = time.monotonic() + timeout_ms / 1000
    last_error: Exception | None = None
    for _ in range(4):
        remaining_ms = int((deadline - time.monotonic()) * 1000)
        if remaining_ms <= 0:
            break
        try:
            wait_clickable(scope, locator, timeout_ms=min(4000, remaining_ms))
            locator.click(timeout=min(3000, max(500, remaining_ms)))
            return
        except (AssertionError, PlaywrightError) as exc:
            last_error = exc
            scope.wait_for_timeout(150)
    name = f"「{label}」" if label else "目标元素"
    raise AssertionError(
        f"点击{name}失败：{last_error}；遮挡={visible_blockers(scope) or '无'}"
    )
