#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""本地人脸检测工具（不联网，不调用任何第三方付费接口）。

用法：
    python3 tools/face_probe.py /path/to/photo.jpg
    python3 tools/face_probe.py /path/to/photo.png --model /path/to/face_detection_yunet.onnx

模型查找顺序：
    1. --model 参数；
    2. 环境变量 FACE_MODEL_PATH；
    3. 与本脚本同目录的 face_detection_yunet.onnx。
YuNet 模型缺失或加载失败时回退 OpenCV 自带 Haar 级联；两者都不可用则返回
error 字段，不抛未处理异常。

输出（单行 JSON）：
    {"has_face": true, "center": [0.5, 0.4], "box": [0.2, 0.25], "width": 1080, "height": 1440}
    {"has_face": false, "center": null, "box": null, "width": 1080, "height": 1440}
    {"has_face": false, "center": null, "box": null, "width": 0, "height": 0, "error": "decode_failed"}

坐标均为相对原图的比例（0~1），与 v1 的 get_face_center 输出一致。
has_face 沿用 v1 门禁语义：检出完整可信人脸且人脸框面积占比 >= 0.1%；
检出但面积过小的脸仍返回 center/box，供卡片人脸对准使用。
错误信息只使用固定错误码，不包含图片路径、对象键或凭证。

YuNet 模型来自 opencv_zoo（MIT License，Copyright (c) 2020 Shiqi Yu），
许可全文见同目录 face_detection_yunet.LICENSE。
"""

from __future__ import annotations

import argparse
import json
import math
import os
import sys

# v1 门禁阈值：人脸框面积占全图比例下限（0.1%）
MIN_FACE_AREA = 0.001
# 检测用缩略图最长边，与 v1 一致
MAX_DETECT_SIDE = 1080
# 框与图像交叠面积低于该比例视为严重裁切，结果不可信
MIN_VISIBLE_RATIO = 0.7
# YuNet 置信度阈值，与 v1 一致
YUNET_SCORE_THRESHOLD = 0.6


class ProbeError(Exception):
    """内部错误，code 为固定错误码，不携带路径。"""

    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


def _result(has_face: bool, center, box, width: int, height: int, error: str | None = None) -> dict:
    out = {
        "has_face": bool(has_face),
        "center": center,
        "box": box,
        "width": int(width),
        "height": int(height),
    }
    if error:
        out["error"] = error
    return out


def _error(code: str) -> dict:
    return _result(False, None, None, 0, 0, code)


def _target_size(width: int, height: int, max_side: int = MAX_DETECT_SIDE) -> tuple[int, int]:
    """返回按最长边限制缩放后的尺寸；不放大图片。"""
    if width <= 0 or height <= 0 or max_side <= 0:
        return max(1, width), max(1, height)
    longest = max(width, height)
    if longest <= max_side:
        return width, height
    scale = max_side / float(longest)
    return max(1, int(width * scale + 0.5)), max(1, int(height * scale + 0.5))


def _load_image(cv2, path: str):
    """读取 JPEG/PNG，按 EXIF 方向校正，返回 (BGR ndarray, 原图宽, 原图高)。

    ndarray 的最长边不超过 MAX_DETECT_SIDE：JPEG 用 Pillow 的 draft 在解码
    阶段就按 1/2、1/4、1/8 缩小，避免 12MP 原图完整解码；其余格式再缩图。
    坐标换算一律基于 ndarray 尺寸，因此比例与原图一致。
    """
    if not os.path.isfile(path):
        raise ProbeError("file_not_found")
    try:
        import numpy as np
        from PIL import Image, ImageOps
    except ImportError:
        # 没有 Pillow 时退化为 OpenCV 解码；不支持 EXIF 方向校正。
        img = cv2.imread(path, cv2.IMREAD_COLOR)
        if img is None:
            raise ProbeError("decode_failed")
        height, width = img.shape[:2]
        target_w, target_h = _target_size(width, height)
        if (target_w, target_h) != (width, height):
            img = cv2.resize(img, (target_w, target_h), interpolation=cv2.INTER_LINEAR)
        return img, width, height

    try:
        with Image.open(path) as raw:
            width, height = raw.size
            target_w, target_h = _target_size(width, height)
            if (target_w, target_h) != (width, height):
                # draft 只对 JPEG 生效；非 JPEG 会忽略后由 thumbnail 兜底。
                try:
                    raw.draft("RGB", (target_w, target_h))
                except Exception:
                    pass
            image = ImageOps.exif_transpose(raw).convert("RGB")
            if max(image.size) > MAX_DETECT_SIDE:
                image.thumbnail((MAX_DETECT_SIDE, MAX_DETECT_SIDE), Image.LANCZOS)
            array = np.asarray(image)
    except Exception:
        # Pillow 解码失败时再尝试 OpenCV，兼容少数 EXIF/ICC 异常图片。
        img = cv2.imread(path, cv2.IMREAD_COLOR)
        if img is None:
            raise ProbeError("decode_failed")
        height, width = img.shape[:2]
        target_w, target_h = _target_size(width, height)
        if (target_w, target_h) != (width, height):
            img = cv2.resize(img, (target_w, target_h), interpolation=cv2.INTER_LINEAR)
        return img, width, height
    return cv2.cvtColor(array, cv2.COLOR_RGB2BGR), width, height


def _resolve_model(explicit: str) -> str:
    candidates = [
        explicit or "",
        os.environ.get("FACE_MODEL_PATH", ""),
        os.path.join(os.path.dirname(os.path.abspath(__file__)), "face_detection_yunet.onnx"),
    ]
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return candidate
    return ""


def _make_detector(cv2, model_path: str):
    """返回 (kind, detector)；YuNet 优先，失败回退 Haar，均不可用返回 ("none", None)。"""
    if model_path:
        try:
            detector = cv2.FaceDetectorYN.create(
                model_path, "", (320, 320), score_threshold=YUNET_SCORE_THRESHOLD
            )
            return "yunet", detector
        except Exception:
            pass
    try:
        cascade_path = os.path.join(
            cv2.data.haarcascades, "haarcascade_frontalface_default.xml"
        )
        classifier = cv2.CascadeClassifier(cascade_path)
        if not classifier.empty():
            return "haar", classifier
    except Exception:
        pass
    return "none", None


def _detect_boxes(cv2, kind: str, detector, image) -> list[list[float]]:
    height, width = image.shape[:2]
    boxes: list[list[float]] = []
    if kind == "yunet":
        detector.setInputSize((width, height))
        _, faces = detector.detect(image)
        if faces is None:
            return boxes
        for face in faces:
            if len(face) < 4:
                continue
            score = float(face[14]) if len(face) >= 15 else 1.0
            boxes.append(
                [float(face[0]), float(face[1]), float(face[2]), float(face[3]), score]
            )
        return boxes

    gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    rects = detector.detectMultiScale(
        gray, scaleFactor=1.1, minNeighbors=5, minSize=(40, 40)
    )
    for (x, y, box_w, box_h) in rects:
        boxes.append([float(x), float(y), float(box_w), float(box_h), 1.0])
    return boxes


def _complete_boxes(boxes: list[list[float]], width: int, height: int) -> list[list[float]]:
    valid: list[list[float]] = []
    for box in boxes:
        x, y, box_w, box_h = box[:4]
        if box_w <= 0 or box_h <= 0:
            continue
        visible_w = max(0.0, min(x + box_w, width) - max(x, 0.0))
        visible_h = max(0.0, min(y + box_h, height) - max(y, 0.0))
        if (visible_w * visible_h) / (box_w * box_h) < MIN_VISIBLE_RATIO:
            continue
        valid.append(box)
    return valid


def _choose_box(boxes: list[list[float]], width: int, height: int):
    """按 v1 规则选择主体脸：置信度明显更高者优先，否则按面积×靠中心程度。"""
    if not boxes:
        return None
    best_score = max(boxes, key=lambda box: box[4])
    best_area = max(boxes, key=lambda box: box[2] * box[3])
    if best_score[4] - best_area[4] >= 0.12:
        return best_score

    def subject_weight(box: list[float]) -> float:
        center_x = (box[0] + box[2] / 2) / width
        center_y = (box[1] + box[3] / 2) / height
        distance = (center_x - 0.5) ** 2 + (center_y - 0.5) ** 2
        return math.exp(-distance / 0.16)

    return max(boxes, key=lambda box: box[2] * box[3] * subject_weight(box))


def detect(path: str, model: str = "") -> dict:
    try:
        import cv2
    except ImportError:
        return _error("opencv_unavailable")

    try:
        image, width, height = _load_image(cv2, path)
    except ProbeError as exc:
        return _error(exc.code)
    except Exception:
        return _error("decode_failed")

    if width < 40 or height < 40:
        return _result(False, None, None, width, height)

    # _load_image 已把检测图缩到 MAX_DETECT_SIDE 内；宽高仍返回原图尺寸。
    detect_image = image

    kind, detector = _make_detector(cv2, _resolve_model(model))
    if kind == "none":
        return _error("detector_unavailable")

    try:
        boxes = _detect_boxes(cv2, kind, detector, detect_image)
    except Exception:
        return _error("detect_failed")

    detect_height, detect_width = detect_image.shape[:2]
    valid = _complete_boxes(boxes, detect_width, detect_height)
    chosen = _choose_box(valid, detect_width, detect_height)
    if chosen is None:
        return _result(False, None, None, width, height)

    x, y, box_w, box_h = chosen[:4]
    center = [
        round((x + box_w / 2) / detect_width, 4),
        round((y + box_h / 2) / detect_height, 4),
    ]
    box = [round(box_w / detect_width, 4), round(box_h / detect_height, 4)]
    has_face = box[0] * box[1] >= MIN_FACE_AREA
    return _result(has_face, center, box, width, height)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="本地人脸检测（YuNet/Haar）")
    parser.add_argument("image", help="JPEG/PNG 图片路径")
    parser.add_argument("--model", default="", help="YuNet ONNX 模型路径")
    args = parser.parse_args(argv)

    try:
        payload = detect(args.image, args.model)
    except Exception:
        # 兜底：任何未预期异常都只返回固定错误码，不打印堆栈与路径。
        payload = _error("internal_error")
    print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
