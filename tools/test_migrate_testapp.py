#!/usr/bin/env python3
"""Unit tests for the testapp migration mapper.

Run from the repository root:
    python3 -m unittest discover -s tools -p 'test_*.py' -v

These tests intentionally do not need psycopg/boto3/Pillow; only pure mapping,
phone allocation and reconciliation logic is exercised here.
"""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import migrate_testapp as m  # noqa: E402


class FieldHelperTests(unittest.TestCase):
    def test_text_segments_and_selects(self):
        fields = {
            "名称": [{"text": "第一行", "type": "text"}, {"text": "第二行", "type": "text"}],
            "单选": "单身",
            "数字": 100,
            "空": None,
        }
        self.assertEqual(m.field_text(fields, "名称"), "第一行\n第二行")
        self.assertEqual(m.field_text(fields, "单选"), "单身")
        self.assertEqual(m.field_text(fields, "数字"), "100")
        self.assertEqual(m.field_text(fields, "空"), "")
        self.assertEqual(m.field_text(fields, "不存在", "fallback"), "fallback")

    def test_multi_select_list(self):
        fields = {
            "性格": ["喜欢安静", "比较主动"],
            "单个": "价值",
            "空": None,
        }
        self.assertEqual(m.field_list(fields, "性格"), ["喜欢安静", "比较主动"])
        self.assertEqual(m.field_list(fields, "单个"), ["价值"])
        self.assertEqual(m.field_list(fields, "空"), [])

    def test_numbers_and_dates(self):
        fields = {
            "上限": [{"text": "无", "type": "text"}],
            "人数": 18,
            "生日": 805219200000,  # 1995-07-09 Asia/Shanghai
            "创建": "2026-09-28 22:10:55",
            "空日期": "",
        }
        self.assertEqual(m.field_int(fields, "上限"), 0)
        self.assertEqual(m.field_int(fields, "人数"), 18)
        self.assertEqual(str(m.ms_to_local_date(m.field_number_ms(fields, "生日"))), "1995-07-09")
        created = m.parse_local_timestamp(fields["创建"])
        self.assertIsNotNone(created)
        self.assertEqual(created.strftime("%Y-%m-%d %H:%M:%S"), "2026-09-28 22:10:55")
        self.assertEqual(created.utcoffset().total_seconds(), 8 * 3600)
        self.assertIsNone(m.parse_local_timestamp(fields["空日期"]))

    def test_attachments(self):
        fields = {
            "个人照片": [
                {"file_token": "tok-a", "name": "a.jpg"},
                {"file_token": "tok-b", "name": "b.jpg"},
                {"name": "without-token.jpg"},
            ]
        }
        attachments = m.field_attachments(fields, "个人照片")
        self.assertEqual([a["file_token"] for a in attachments], ["tok-a", "tok-b"])


class StatusAndIdTests(unittest.TestCase):
    def test_account_status_map(self):
        expected = {
            "待审核": "pending",
            "单身": "active",
            "已脱单": "matched",
            "审核不通过": "rejected",
            "已退出": "left",
            "村情六处": "observer",
        }
        for raw, mapped in expected.items():
            status, warning = m.map_account_status(raw)
            self.assertEqual(status, mapped)
            self.assertIsNone(warning)
        status, warning = m.map_account_status("未知状态")
        self.assertEqual(status, "pending")
        self.assertIn("unknown", warning)

    def test_id_card_validation(self):
        self.assertTrue(m.validate_id_card("610322198403122913"))
        self.assertTrue(m.validate_id_card("440305199001150336"))
        self.assertTrue(m.validate_id_card("441381199507173815"))
        self.assertFalse(m.validate_id_card("12"))
        self.assertFalse(m.validate_id_card("12345678"))
        self.assertFalse(m.validate_id_card("610322198403122914"))  # bad checksum

    def test_phone_masking(self):
        self.assertEqual(m.mask_phone("13800138000"), "138****8000")
        self.assertEqual(m.mask_phone("1"), "***")
        self.assertEqual(m.mask_phone(None), "")
        self.assertNotIn("13800138000", m.mask_phone("13800138000"))


class UserMappingTests(unittest.TestCase):
    def test_user_mapping(self):
        fields = {
            "姓名": [{"text": "侯冬冬", "type": "text"}],
            "昵称": [{"text": "猴哥", "type": "text"}],
            "性别": "男性",
            "生日": 447897600000,
            "身高（cm）": 172,
            "学历": "本科",
            "圣名": [{"text": "若瑟", "type": "text"}],
            "经常去的教堂": [{"text": "深圳南头堂", "type": "text"}],
            "现居/工作城市": [{"text": "广东-汕头", "type": "text"}],
            "手机号": "13800138000",
            "飞书用户ID": [{"text": "ou_test", "type": "text"}],
            "账号状态": "单身",
            "身份证号": [{"text": "441381199507173815", "type": "text"}],
            "我是一个怎样的人-性格": ["喜欢安静", "积极乐观"],
            "我是个怎样的人-MBTI人格": ["I", "N", "F", "P"],
            "理想中的TA-运动": ["爬山"],
            "个人照片": [{"file_token": "tok1", "name": "a.jpg"}],
            "注册时间": 1787327113000,
            "最近活跃": 1792463257488,
        }
        record = m.build_user_record(fields, "rec123", "U-0006", "user")
        self.assertEqual(record["key"], "U-0006")
        self.assertEqual(record["legacy_user_id"], "U-0006")
        self.assertEqual(record["legacy_open_id"], "ou_test")
        self.assertEqual(record["name"], "侯冬冬")
        self.assertEqual(record["nickname"], "猴哥")
        self.assertEqual(record["account_status"], "active")
        self.assertEqual(record["status"], "active")
        self.assertEqual(record["id_card_last4"], "3815")
        self.assertTrue(record["id_card_valid"])
        self.assertTrue(record["id_card_present"])
        self.assertEqual(record["self_traits"], ["喜欢安静", "积极乐观"])
        self.assertEqual(record["mbti"], ["I", "N", "F", "P"])
        self.assertEqual(record["partner_sports"], ["爬山"])
        self.assertEqual(record["height_cm"], 172)
        self.assertEqual(record["birthday"].isoformat(), "1984-03-12")
        self.assertEqual(record["legacy_phone"], "13800138000")
        self.assertEqual(record["photos"][0]["file_token"], "tok1")

    def test_name_falls_back_to_nickname(self):
        fields = {"昵称": [{"text": "压测0001", "type": "text"}]}
        record = m.build_user_record(fields, "rec1", "U-9999", "user")
        self.assertEqual(record["name"], "压测0001")
        self.assertEqual(record["account_status"], "pending")

    def test_observer_status(self):
        fields = {
            "编号": "C-001",
            "昵称": [{"text": "猴哥猴哥", "type": "text"}],
            "账号状态": "村情六处",
        }
        record = m.build_user_record(fields, "recC", "C-001", "observer")
        self.assertEqual(record["account_kind"], "observer")
        self.assertEqual(record["account_status"], "observer")
        self.assertEqual(record["name"], "猴哥猴哥")

    def test_invalid_id_only_keeps_suffix(self):
        fields = {"姓名": "X", "身份证号": "12", "手机号": "1"}
        record = m.build_user_record(fields, "rec2", "U-0001", "user")
        self.assertEqual(record["id_card_last4"], "")
        self.assertFalse(record["id_card_valid"])


class PhoneAllocationTests(unittest.TestCase):
    def records(self):
        return [
            {"key": "U-0001", "raw_phone": "17307553177"},
            {"key": "U-0002", "raw_phone": ""},
            {"key": "U-0003", "raw_phone": "12345678"},
            {"key": "U-0005", "raw_phone": "1"},
            {"key": "U-0006", "raw_phone": "13800138000"},
            {"key": "U-2789", "raw_phone": "13800138000"},
            {"key": "U-2791", "raw_phone": "13900139000"},
            {"key": "C-001", "raw_phone": ""},
        ]

    def test_reserved_duplicate_missing_and_invalid(self):
        assignments, errors = m.plan_phone_assignments(self.records(), {})
        self.assertEqual(errors, [])
        self.assertEqual(assignments["U-0006"]["assigned"], "13800138000")
        self.assertEqual(assignments["U-0006"]["reason"], "reserved_u0006")
        self.assertEqual(assignments["U-0001"]["assigned"], "17307553177")
        self.assertEqual(assignments["U-2791"]["assigned"], "13900139000")
        self.assertEqual(assignments["U-0002"]["reason"], "missing")
        self.assertEqual(assignments["U-0003"]["reason"], "invalid")
        self.assertEqual(assignments["U-0005"]["reason"], "invalid")
        self.assertEqual(assignments["U-2789"]["reason"], "duplicate")
        self.assertEqual(assignments["C-001"]["reason"], "missing")
        assigned = [value["assigned"] for value in assignments.values()]
        self.assertEqual(len(assigned), len(set(assigned)))
        for value in assignments.values():
            self.assertNotIn(value["assigned"], value["assigned_masked"])
            self.assertIn("****", value["assigned_masked"])

    def test_rerun_reuses_test_numbers_and_avoids_external_phone(self):
        records = self.records()
        first, _ = m.plan_phone_assignments(records, {})
        existing = {
            key: {"phone": value["assigned"]} for key, value in first.items()
        }
        existing["U-9999"] = {"phone": "13900000009"}
        second, errors = m.plan_phone_assignments(records, existing)
        self.assertEqual(errors, [])
        for key in first:
            self.assertEqual(second[key]["assigned"], first[key]["assigned"])
        assigned = {value["assigned"] for value in second.values()}
        self.assertNotIn("13900000009", assigned)

    def test_assignment_is_independent_of_input_order(self):
        records = self.records()
        forward, _ = m.plan_phone_assignments(records, {})
        backward, _ = m.plan_phone_assignments(list(reversed(records)), {})
        self.assertEqual(
            {key: value["assigned"] for key, value in forward.items()},
            {key: value["assigned"] for key, value in backward.items()},
        )

    def test_reserved_phone_owned_by_other_is_fatal(self):
        records = [{"key": "U-0006", "raw_phone": "13800138000"}]
        _, errors = m.plan_phone_assignments(
            records, {"U-9999": {"phone": "13800138000"}}
        )
        self.assertTrue(any("U-0006" in error for error in errors))


class ActivityAndSocialMappingTests(unittest.TestCase):
    def test_activity_mapping(self):
        row = {
            "record_id": "recA",
            "fields": {
                "活动ID": "A-0011",
                "活动名称": [{"text": "深圳南头堂", "type": "text"}],
                "报名人数上限": [{"text": "无", "type": "text"}],
                "当前报名人数": 18,
                "分组功能开启": "否",
                "一对一功能开启": "是",
                "签到功能开启": "是",
                "签到码": [{"text": "5520", "type": "text"}],
                "发布时间": 1788772625000,
                "开始时间": 1792198800000,
                "活动海报": [{"file_token": "poster1", "name": "p.jpg"}],
            },
        }
        record = m.build_activity_record(row)
        self.assertEqual(record["activity_no"], "A-0011")
        self.assertEqual(record["max_signup"], 0)
        self.assertFalse(record["group_enabled"])
        self.assertTrue(record["onetoone_enabled"])
        self.assertTrue(record["signin_enabled"])
        self.assertEqual(record["signin_code"], "5520")
        self.assertEqual(record["poster_tokens"], ["poster1"])

    def test_dedupe_signups_keeps_latest(self):
        def row(record_id, activity, oid, ts, status):
            return {
                "record_id": record_id,
                "fields": {
                    "活动ID": activity,
                    "报名人open_id": [{"text": oid, "type": "text"}],
                    "报名时间": ts,
                    "状态": status,
                },
            }

        rows = [
            row("r1", "A-0011", "ou_a", 1000, "已取消"),
            row("r2", "A-0011", "ou_a", 2000, "已报名"),
            row("r3", "A-0011", "ou_b", 1500, "已报名"),
        ]
        kept, skipped, _ = m.dedupe_signups(
            rows, {"A-0011": "A-0011"}, {"ou_a": "U-0001", "ou_b": "U-0002"}, {}
        )
        self.assertEqual(skipped, 1)
        self.assertEqual({r["record_id"] for r in kept}, {"r2", "r3"})

    def test_ledger_reconciliation(self):
        rows = [
            {"id": 1, "user_oid": "ou_a", "delta": 60, "balance_after": 60},
            {"id": 2, "user_oid": "ou_a", "delta": -20, "balance_after": 40},
            {"id": 3, "user_oid": "ou_a", "delta": 5, "balance_after": 45},
            {"id": 4, "user_oid": "ou_b", "delta": 10, "balance_after": 9},
        ]
        result = m.reconcile_ledger(rows, {"ou_a": "U-0001", "ou_b": "U-0002"})
        balances = {item["legacy_user_id"]: item for item in result["balances"]}
        self.assertTrue(balances["U-0001"]["match"])
        self.assertEqual(balances["U-0001"]["sum_delta"], 45)
        self.assertFalse(balances["U-0002"]["match"])
        self.assertEqual(len(result["running_mismatches"]), 1)
        self.assertEqual(result["running_mismatches"][0]["legacy_user_id"], "U-0002")

    def test_ledger_unresolved_user(self):
        result = m.reconcile_ledger(
            [{"id": 1, "user_oid": "ou_missing", "delta": 1, "balance_after": 1}],
            {},
        )
        self.assertEqual(result["unresolved_user_oids"], ["ou_missing"])
        self.assertEqual(result["balances"], [])


class PhotoProcessTests(unittest.TestCase):
    def test_thumbnail_and_limits(self):
        try:
            from PIL import Image
        except ImportError:
            self.skipTest("Pillow not installed")
        import io

        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "photo.jpg"
            image = Image.new("RGB", (1200, 800), (200, 30, 90))
            image.save(path, format="JPEG", quality=90)
            processed = m.process_photo_bytes(path)
            self.assertEqual(processed["content_type"], "image/jpeg")
            self.assertEqual((processed["width"], processed["height"]), (1200, 800))
            thumb = Image.open(io.BytesIO(processed["thumb"]))
            self.assertLessEqual(max(thumb.size), m.THUMB_SIZE)
            self.assertEqual(thumb.format, "JPEG")

    def test_rejects_non_image(self):
        try:
            import PIL  # noqa: F401
        except ImportError:
            self.skipTest("Pillow not installed")
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "not.jpg"
            path.write_bytes(b"not an image")
            with self.assertRaises(ValueError):
                m.process_photo_bytes(path)


class PhotoIndexTests(unittest.TestCase):
    def test_index_picks_token_files_and_prefers_jpeg(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "sub").mkdir()
            (root / "tok-a.png").write_bytes(b"png")
            (root / "sub" / "tok-a.jpg").write_bytes(b"jpg")
            (root / "tok-b.jpeg").write_bytes(b"jpeg")
            (root / "unrelated.jpg").write_bytes(b"x")
            found = m.index_photo_files(root, {"tok-a", "tok-b", "tok-c"})
            self.assertEqual(set(found), {"tok-a", "tok-b"})
            self.assertEqual(found["tok-a"].suffix, ".jpg")
            self.assertEqual(found["tok-b"].suffix, ".jpeg")


if __name__ == "__main__":
    unittest.main(verbosity=2)
