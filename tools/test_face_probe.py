#!/usr/bin/env python3
"""人脸检测工具的纯逻辑单元测试。

这些测试不依赖 OpenCV/Pillow，CI 无需安装 cv2：
    python3 -m unittest discover -s tools -p 'test_*.py' -v
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import face_probe as probe  # noqa: E402


class FaceProbeLogicTest(unittest.TestCase):
    def test_result_shape(self):
        payload = probe._result(True, [0.5, 0.4], [0.2, 0.3], 800, 600)
        self.assertEqual(
            payload,
            {
                "has_face": True,
                "center": [0.5, 0.4],
                "box": [0.2, 0.3],
                "width": 800,
                "height": 600,
            },
        )
        self.assertNotIn("error", payload)

    def test_error_shape(self):
        payload = probe._error("decode_failed")
        self.assertFalse(payload["has_face"])
        self.assertIsNone(payload["center"])
        self.assertIsNone(payload["box"])
        self.assertEqual(payload["error"], "decode_failed")

    def test_complete_boxes_filters_heavy_crop(self):
        boxes = [
            [0.0, 0.0, 100.0, 100.0, 0.9],  # 完整
            [90.0, 0.0, 100.0, 100.0, 0.9],  # 只露出 10%，丢弃
        ]
        valid = probe._complete_boxes(boxes, 100, 100)
        self.assertEqual(len(valid), 1)
        self.assertEqual(valid[0][0], 0.0)

    def test_choose_box_prefers_clear_confidence(self):
        boxes = [
            [0.0, 0.0, 10.0, 10.0, 0.99],
            [0.0, 0.0, 80.0, 80.0, 0.70],
        ]
        chosen = probe._choose_box(boxes, 100, 100)
        self.assertIsNotNone(chosen)
        self.assertEqual(chosen[4], 0.99)

    def test_choose_box_uses_center_when_scores_close(self):
        boxes = [
            [0.0, 0.0, 35.0, 35.0, 0.80],  # 左上，面积大但远离中心
            [35.0, 35.0, 30.0, 30.0, 0.78],  # 中心，面积略小
        ]
        chosen = probe._choose_box(boxes, 100, 100)
        self.assertIsNotNone(chosen)
        self.assertEqual(chosen[2], 30.0)

    def test_choose_box_empty(self):
        self.assertIsNone(probe._choose_box([], 100, 100))


if __name__ == "__main__":
    unittest.main()
