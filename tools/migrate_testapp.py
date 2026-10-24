#!/usr/bin/env python3
"""Import the testapp Bitable/SQLite export into the v2 PostgreSQL schema.

This is a one-off migration tool, not runtime code.

Usage:
    python3 tools/migrate_testapp.py \
        --export-dir /tmp/yxq_export \
        --database-url "$DATABASE_URL" \
        --dry-run|--apply \
        [--photos-dir /tmp/yxq_photos] \
        [--report-out /tmp/migration-report.json]

The tool is idempotent: legacy record ids are the primary key for reruns.
Dry-run only reads PostgreSQL; it never uploads to R2 and never writes rows.

R2 credentials come from the same environment variables as the server:
R2_ENDPOINT, R2_REGION, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_BUCKET,
R2_SECURE. They are never printed or written to the report.

Photos are matched as <photos-dir>/<file_token>.<ext> (recursively).
If a token has no local file it is reported as missing and the migration
continues; text data is still imported.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sqlite3
import sys
import uuid
from collections import Counter
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Iterable
from urllib.parse import quote

try:
    from zoneinfo import ZoneInfo

    SHANGHAI: timezone | Any = ZoneInfo("Asia/Shanghai")
except Exception:  # pragma: no cover - minimal environments without tzdata
    SHANGHAI = timezone(timedelta(hours=8))

UTC = timezone.utc
NOW = lambda: datetime.now(UTC)

EXPORT_TABLES = [
    "users",
    "observers",
    "likes",
    "activities",
    "signups",
    "group_select",
    "group_result",
    "onetoone_result",
    "messages",
    "reports",
    "suggestions",
    "praise",
]

STATUS_MAP = {
    "待审核": "pending",
    "单身": "active",
    "已脱单": "matched",
    "审核不通过": "rejected",
    "已退出": "left",
    "村情六处": "observer",
}

MOBILE_RE = re.compile(r"^1[3-9]\d{9}$")
TEST_PHONE_RE = re.compile(r"^1390000\d{4}$")
RESERVED_PHONE = "13800138000"
RESERVED_USER = "U-0006"

MAX_PHOTO_BYTES = 10 * 1024 * 1024
MAX_PHOTO_DIM = 8000
# Keep in sync with internal/media.DefaultMaxPixels: 12.5 MP accepts the
# common iPhone 12 MP frame (4032x3024 = 12,192,768 pixels).
MAX_PHOTO_PIXELS = 12_500_000
THUMB_SIZE = 512

# Columns required for the import. A missing column is reported, never created.
REQUIRED_COLUMNS = {
    "users": {
        "id", "legacy_user_id", "legacy_open_id", "legacy_record_id", "phone",
        "name", "nickname", "gender", "birthday", "height_cm", "education",
        "baptismal_name", "church", "church_location", "church_group",
        "native_place", "city", "industry", "position", "income", "house",
        "driving", "house_note_have", "house_note_none", "driving_note_have",
        "driving_note_none", "marriage", "family", "live_with_parents",
        "personality", "self_traits", "self_hobbies", "self_sports", "mbti",
        "partner_criteria", "partner_traits", "partner_hobbies",
        "partner_sports", "wechat", "legacy_phone", "id_card_ciphertext",
        "id_card_last4", "id_card_valid", "account_kind", "observer_enabled", "account_status",
        "status", "contact_consent", "referral_source", "is_for_child",
        "wechat_payment", "registered_at", "last_active_at", "created_at",
        "updated_at",
    },
    "media": {
        "id", "user_id", "original_name", "object_key", "thumb_key",
        "content_type", "byte_size", "thumb_size", "width", "height", "kind",
        "legacy_file_token",
    },
    "profile_photos": {"user_id", "media_id", "sort_order", "is_cover"},
    "likes": {
        "legacy_record_id", "initiator_id", "target_id", "initiator_name",
        "target_name", "status", "like_type", "message", "month", "created_at",
    },
    "praises": {
        "legacy_record_id", "initiator_id", "target_id", "status",
        "object_type", "object_id", "day", "week", "created_at",
    },
    "messages": {
        "legacy_record_id", "target_user_id", "author_id", "author_name",
        "parent_id", "reply_to_id", "content", "status", "created_at",
    },
    "reports": {
        "legacy_record_id", "reporter_id", "target_user_id",
        "target_message_id", "reporter_name", "target_name", "reason",
        "status", "handled_by", "handled_at", "created_at",
    },
    "suggestions": {
        "legacy_record_id", "author_id", "author_name", "type", "content",
        "status", "created_at",
    },
    "activities": {
        "legacy_record_id", "activity_no", "name", "description", "location",
        "condition", "max_signup", "current_signup", "fee", "food", "status",
        "poster_media_id", "group_status", "male_per_group", "female_per_group",
        "group_enabled", "onetoone_status", "onetoone_enabled",
        "signin_enabled", "signin_code", "publish_at", "start_at", "end_at",
        "created_at", "updated_at",
    },
    "signups": {
        "legacy_record_id", "activity_id", "user_id", "nickname", "status",
        "attendance", "created_at",
    },
    "group_selections": {
        "legacy_record_id", "activity_id", "selector_id", "gender", "choices",
        "round", "updated_at",
    },
    "group_results": {
        "legacy_record_id", "activity_id", "group_no", "user_id", "user_name",
        "gender", "round", "created_at",
    },
    "onetoone_results": {
        "legacy_record_id", "activity_id", "rank", "user_id", "user_name",
        "gender", "target_id", "target_name", "round", "created_at",
    },
    "points_ledger": {
        "legacy_id", "user_id", "delta", "balance_after", "kind", "reason",
        "ref_type", "ref_id", "operator_id", "created_at", "idempotency_key",
    },
    "points_redemptions": {
        "legacy_id", "user_id", "item", "cost", "status", "params",
        "idempotency_key", "ledger_id", "refund_ledger_id", "refund_key",
        "ref_id", "created_at", "resolved_at", "note",
    },
    "invites": {
        "legacy_id", "inviter_id", "invitee_phone", "invitee_id", "status",
        "gender", "source", "created_at", "updated_at", "bound_at",
        "confirm_due_at", "confirmed_at", "reward_ledger_id", "note",
    },
    "historical_participants": {"phone", "name", "source", "added_at"},
    "app_config": {"key", "value", "updated_at", "operator_id"},
}


class MigrationError(RuntimeError):
    """Fatal migration error (bad export, missing schema, missing R2 config)."""


# ---------------------------------------------------------------------------
# Bitable value helpers
# ---------------------------------------------------------------------------


def format_number(value: float | int) -> str:
    if isinstance(value, bool):
        return "是" if value else "否"
    if float(value).is_integer():
        return str(int(value))
    return str(value)


def as_text(value: Any) -> str:
    """Flatten Bitable text segments/selects/attachments names into one string."""
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return "是" if value else "否"
    if isinstance(value, (int, float)):
        return format_number(value)
    if isinstance(value, list):
        parts: list[str] = []
        for item in value:
            if isinstance(item, dict):
                text = item.get("text")
                if text is not None:
                    parts.append(str(text))
                elif item.get("name") is not None:
                    parts.append(str(item["name"]))
            elif isinstance(item, str):
                parts.append(item)
            elif isinstance(item, (int, float)):
                parts.append(format_number(item))
        return "\n".join(parts)
    if isinstance(value, dict):
        if value.get("text") is not None:
            return str(value["text"])
        if value.get("name") is not None:
            return str(value["name"])
        if value.get("link") is not None:
            return str(value["link"])
        return ""
    return str(value)


def field_text(fields: dict[str, Any], key: str, default: str = "") -> str:
    text = as_text(fields.get(key)).strip()
    return text if text else default


def field_list(fields: dict[str, Any], key: str) -> list[str]:
    value = fields.get(key)
    if value is None:
        return []
    if isinstance(value, list):
        out: list[str] = []
        for item in value:
            if isinstance(item, dict):
                text = item.get("text")
                if text is None:
                    text = item.get("name")
                if text is not None and str(text) != "":
                    out.append(str(text))
            elif item is not None and str(item) != "":
                out.append(str(item))
        return out
    if isinstance(value, str):
        return [value] if value else []
    return [str(value)]


def field_int(fields: dict[str, Any], key: str, default: int = 0) -> int:
    value = fields.get(key)
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, (int, float)):
        return int(value)
    match = re.search(r"-?\d+", as_text(value).replace(",", ""))
    return int(match.group()) if match else default


def field_optional_int(fields: dict[str, Any], key: str) -> int | None:
    value = fields.get(key)
    if value is None or value == "":
        return None
    text = as_text(value).strip()
    if not text:
        return None
    return field_int(fields, key) or None


def field_number_ms(fields: dict[str, Any], key: str) -> float | None:
    value = fields.get(key)
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value)
    text = as_text(value).strip()
    if re.fullmatch(r"-?\d+(\.\d+)?", text):
        return float(text)
    return None


def ms_to_datetime(value: float | int | None) -> datetime | None:
    if value is None:
        return None
    return datetime.fromtimestamp(float(value) / 1000.0, tz=UTC)


def ms_to_local_date(value: float | int | None) -> date | None:
    dt = ms_to_datetime(value)
    return dt.astimezone(SHANGHAI).date() if dt else None


def parse_local_timestamp(value: Any) -> datetime | None:
    text = as_text(value).strip()
    if not text:
        return None
    for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%d"):
        try:
            return datetime.strptime(text, fmt).replace(tzinfo=SHANGHAI)
        except ValueError:
            continue
    return None


def parse_local_date(value: Any) -> date | None:
    dt = parse_local_timestamp(value)
    return dt.date() if dt else None


def field_attachments(fields: dict[str, Any], key: str) -> list[dict[str, Any]]:
    value = fields.get(key)
    if not isinstance(value, list):
        return []
    out: list[dict[str, Any]] = []
    for item in value:
        if isinstance(item, dict):
            token = item.get("file_token") or item.get("token")
            if token:
                out.append(item)
    return out


def clean_text(value: Any) -> str:
    return as_text(value).replace("\x00", "")


def json_text(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


# ---------------------------------------------------------------------------
# Validation / masking
# ---------------------------------------------------------------------------


def normalize_phone(raw: Any) -> str:
    text = re.sub(r"[\s\-()]", "", as_text(raw)).lstrip("'")
    if text.startswith("+86"):
        text = text[3:]
    elif text.startswith("86") and len(text) == 13:
        text = text[2:]
    return text


def is_valid_mobile(phone: str) -> bool:
    return bool(MOBILE_RE.match(phone))


def is_test_phone(phone: str) -> bool:
    return bool(TEST_PHONE_RE.match(phone))


def mask_phone(phone: Any) -> str:
    digits = re.sub(r"\D", "", as_text(phone))
    if len(digits) >= 7:
        return digits[:3] + "****" + digits[-4:]
    return "***" if digits else ""


def validate_id_card(value: str) -> bool:
    """Port of lib/bitable_client.py validate_id_card (format/checksum only)."""
    idc = (value or "").strip().upper().replace(" ", "")
    weights = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2]
    check_map = ["1", "0", "X", "9", "8", "7", "6", "5", "4", "3", "2"]
    prefixes = {
        f"{i:02d}"
        for i in list(range(11, 16)) + list(range(21, 24)) + list(range(31, 38))
        + list(range(41, 47)) + list(range(50, 55)) + list(range(61, 66))
        + [71, 81, 82, 91]
    }
    if len(idc) == 15:
        if not idc.isdigit():
            return False
        try:
            date(int("19" + idc[6:8]), int(idc[8:10]), int(idc[10:12]))
        except ValueError:
            return False
        return idc[:2] in prefixes
    if len(idc) != 18:
        return False
    if not idc[:17].isdigit() or idc[17] not in "0123456789X":
        return False
    if idc[:2] not in prefixes:
        return False
    try:
        born = date(int(idc[6:10]), int(idc[10:12]), int(idc[12:14]))
    except ValueError:
        return False
    today = date.today()
    if born > today:
        return False
    age = today.year - born.year - ((today.month, today.day) < (born.month, born.day))
    if age < 0 or age > 120:
        return False
    total = sum(int(idc[i]) * weights[i] for i in range(17))
    return idc[17] == check_map[total % 11]


def map_account_status(raw: Any) -> tuple[str, str | None]:
    text = field_text({"v": raw}, "v")
    mapped = STATUS_MAP.get(text)
    if mapped:
        return mapped, None
    return "pending", f"unknown account status {text!r}; mapped to pending"


def legacy_uid_from_label(value: Any) -> str:
    match = re.search(r"U-\d+", as_text(value))
    return match.group(0) if match else ""


def strip_uid_label(value: Any) -> str:
    text = as_text(value).strip()
    return re.sub(r"\s*[（(]\s*U-\d+\s*[）)]\s*$", "", text)


# ---------------------------------------------------------------------------
# Phone allocation
# ---------------------------------------------------------------------------


def plan_phone_assignments(
    records: list[dict[str, Any]],
    existing_by_key: dict[str, dict[str, Any]],
) -> tuple[dict[str, dict[str, Any]], list[str]]:
    """Assign a unique phone to every source record deterministically.

    Rules:
    - U-0006 keeps 13800138000.
    - Valid unique legacy phones are kept.
    - Duplicate/missing/invalid phones get 1390000xxxx test numbers.
    - A previously assigned test number is reused on rerun.
    """
    if not records:
        return {}, []

    errors: list[str] = []
    source_keys = {r["key"] for r in records}
    existing_phones = {
        key: (info.get("phone") or "") for key, info in existing_by_key.items()
    }
    # Phones owned by users outside this import must stay reserved.
    taken = {
        phone
        for key, phone in existing_phones.items()
        if key not in source_keys and phone
    }

    def order_key(record: dict[str, Any]) -> tuple[int, int, str]:
        key = record["key"]
        return (
            0 if key.startswith("U-") else 1,
            int(re.sub(r"\D", "", key) or 0),
            key,
        )

    ordered = sorted(records, key=order_key)
    reserved = [r for r in ordered if r["key"] == RESERVED_USER]
    ordered = reserved + [r for r in ordered if r["key"] != RESERVED_USER]

    used: set[str] = set()
    assignments: dict[str, dict[str, Any]] = {}
    counter = 0

    def next_test_phone() -> str:
        nonlocal counter
        while True:
            counter += 1
            if counter > 9999:
                raise MigrationError("test phone pool 1390000xxxx exhausted")
            candidate = f"1390000{counter:04d}"
            if candidate not in used and candidate not in taken:
                return candidate

    for record in ordered:
        key = record["key"]
        raw = record.get("raw_phone") or record.get("phone_raw") or ""
        normalized = normalize_phone(raw)
        existing_phone = normalize_phone(existing_phones.get(key, ""))
        reason = ""

        if key == RESERVED_USER and normalized == RESERVED_PHONE:
            if RESERVED_PHONE in taken:
                errors.append(
                    f"{RESERVED_PHONE[:3]}****{RESERVED_PHONE[-4:]} is owned by "
                    f"another existing user; cannot reserve it for {RESERVED_USER}"
                )
            assigned = RESERVED_PHONE
            reason = "reserved_u0006"
        elif (
            normalized
            and is_valid_mobile(normalized)
            and normalized not in used
            and normalized not in taken
        ):
            assigned = normalized
            reason = "kept"
        elif (
            existing_phone
            and is_test_phone(existing_phone)
            and existing_phone not in used
            and existing_phone not in taken
        ):
            assigned = existing_phone
            reason = "kept_existing_test_number"
        else:
            assigned = next_test_phone()
            if not normalized:
                reason = "missing"
            elif not is_valid_mobile(normalized):
                reason = "invalid"
            else:
                reason = "duplicate"

        used.add(assigned)
        assignments[key] = {
            "assigned": assigned,
            "reason": reason,
            "source_masked": mask_phone(raw),
            "assigned_masked": mask_phone(assigned),
        }

    return assignments, errors


# ---------------------------------------------------------------------------
# Source mapping
# ---------------------------------------------------------------------------


def build_user_record(
    fields: dict[str, Any],
    record_id: str,
    key: str,
    kind: str,
) -> dict[str, Any]:
    nickname = clean_text(fields.get("昵称")).strip()
    name = clean_text(fields.get("姓名")).strip() or nickname or key
    id_card = re.sub(r"\s", "", clean_text(fields.get("身份证号"))).upper()
    id_valid = validate_id_card(id_card)
    account_status, status_warning = map_account_status(fields.get("账号状态"))
    if kind == "observer":
        account_status = "observer"
        status_warning = None

    registered_at = ms_to_datetime(field_number_ms(fields, "注册时间"))
    updated_at = ms_to_datetime(field_number_ms(fields, "资料更新时间"))
    birthday = ms_to_local_date(field_number_ms(fields, "生日"))
    raw_phone = clean_text(fields.get("手机号")).strip()

    return {
        "key": key,
        "kind": kind,
        "legacy_user_id": key,
        "legacy_open_id": clean_text(fields.get("飞书用户ID")).strip(),
        "legacy_record_id": record_id,
        "phone_raw": raw_phone,
        "name": name,
        "nickname": nickname,
        "gender": clean_text(fields.get("性别")).strip(),
        "birthday": birthday,
        "height_cm": field_optional_int(fields, "身高（cm）"),
        "education": clean_text(fields.get("学历")).strip(),
        "baptismal_name": clean_text(fields.get("圣名")).strip(),
        "church": clean_text(fields.get("经常去的教堂")).strip(),
        "church_location": clean_text(fields.get("教堂所在市-区")).strip(),
        "church_group": clean_text(fields.get("参加的团体")).strip(),
        "native_place": clean_text(fields.get("家乡")).strip(),
        "city": clean_text(fields.get("现居/工作城市")).strip(),
        "industry": clean_text(fields.get("从事行业")).strip(),
        "position": clean_text(fields.get("职位")).strip(),
        "income": clean_text(fields.get("年收入")).strip(),
        "house": clean_text(fields.get("房产状况")).strip(),
        "driving": clean_text(fields.get("是否有车")).strip(),
        "marriage": clean_text(fields.get("你结过婚吗？")).strip(),
        "family": clean_text(fields.get("家庭成员情况")).strip(),
        "live_with_parents": clean_text(fields.get("婚后是否与父母同住")).strip(),
        "personality": clean_text(fields.get("我是一个怎样的人")).strip(),
        "self_traits": field_list(fields, "我是一个怎样的人-性格"),
        "self_hobbies": field_list(fields, "我是一个怎样的人-爱好"),
        "self_sports": field_list(fields, "我是一个怎样的人-运动"),
        "mbti": field_list(fields, "我是个怎样的人-MBTI人格"),
        "partner_criteria": clean_text(fields.get("理想中的TA")).strip(),
        "partner_traits": field_list(fields, "理想中的TA-性格"),
        "partner_hobbies": field_list(fields, "理想中的TA-爱好"),
        "partner_sports": field_list(fields, "理想中的TA-运动"),
        "wechat": clean_text(fields.get("微信号")).strip(),
        "legacy_phone": raw_phone,
        "id_card_last4": id_card[-4:] if len(id_card) >= 4 else "",
        "id_card_valid": id_valid,
        "id_card_present": bool(id_card),
        "account_kind": kind,
        "observer_enabled": kind == "observer",
        "account_status": account_status,
        "status": "active",
        "contact_consent": False,
        "referral_source": clean_text(fields.get("你是怎么知道这个App的？")).strip(),
        "is_for_child": clean_text(fields.get("您替子女注册吗？")).strip(),
        "wechat_payment": clean_text(fields.get("微信缴费")).strip(),
        "registered_at": registered_at,
        "last_active_at": ms_to_datetime(field_number_ms(fields, "最近活跃")),
        "created_at": registered_at or NOW(),
        "updated_at": updated_at or registered_at or NOW(),
        "photos": field_attachments(fields, "个人照片"),
        "status_warning": status_warning,
        "invite_code": clean_text(fields.get("邀请码")).strip(),
    }


def build_activity_record(record: dict[str, Any]) -> dict[str, Any]:
    fields = record.get("fields", {})
    activity_no = clean_text(fields.get("活动ID")).strip()
    name = clean_text(fields.get("活动名称")).strip()
    if not activity_no:
        activity_no = record["record_id"]
    if not name:
        name = activity_no
    publish_at = ms_to_datetime(field_number_ms(fields, "发布时间"))
    start_at = ms_to_datetime(field_number_ms(fields, "开始时间"))
    end_at = ms_to_datetime(field_number_ms(fields, "结束时间"))
    return {
        "key": activity_no,
        "legacy_record_id": record["record_id"],
        "activity_no": activity_no,
        "name": name,
        "description": clean_text(fields.get("活动描述")).strip(),
        "location": clean_text(fields.get("活动地点")).strip(),
        "condition": clean_text(fields.get("参与条件")).strip(),
        "max_signup": field_int(fields, "报名人数上限"),
        "current_signup": field_int(fields, "当前报名人数"),
        "fee": clean_text(fields.get("费用")).strip(),
        "food": clean_text(fields.get("食宿")).strip(),
        "status": clean_text(fields.get("活动状态")).strip() or "报名中",
        "poster_tokens": [a.get("file_token") for a in field_attachments(fields, "活动海报")],
        "group_status": clean_text(fields.get("分组状态")).strip(),
        "male_per_group": field_int(fields, "每组男生数"),
        "female_per_group": field_int(fields, "每组女生数"),
        "group_enabled": clean_text(fields.get("分组功能开启")).strip() == "是",
        "onetoone_status": clean_text(fields.get("一对一状态")).strip(),
        "onetoone_enabled": clean_text(fields.get("一对一功能开启")).strip() == "是",
        "signin_enabled": clean_text(fields.get("签到功能开启")).strip() == "是",
        "signin_code": clean_text(fields.get("签到码")).strip(),
        "publish_at": publish_at,
        "start_at": start_at,
        "end_at": end_at,
        "created_at": publish_at or NOW(),
        "updated_at": NOW(),
    }


def build_user_maps(
    user_records: list[dict[str, Any]],
    observer_records: list[dict[str, Any]],
) -> tuple[dict[str, str], dict[str, str], list[str]]:
    """Return (open_id -> key, legacy_user_id -> key, warnings)."""
    warnings: list[str] = []
    user_keys = {r["key"] for r in user_records}
    openid_to_key: dict[str, str] = {}
    for record in user_records:
        oid = record["legacy_open_id"]
        if oid:
            openid_to_key[oid] = record["key"]
    for record in observer_records:
        oid = record["legacy_open_id"]
        if not oid:
            continue
        if oid in openid_to_key:
            warnings.append(
                f"observer {record['key']} shares legacy_open_id with "
                f"{openid_to_key[oid]}; social/points references resolve to the user row"
            )
            continue
        openid_to_key[oid] = record["key"]
    uid_to_key = {r["key"]: r["key"] for r in user_records}
    uid_to_key.update({r["key"]: r["key"] for r in observer_records})
    return openid_to_key, uid_to_key | {k: k for k in user_keys}, warnings


def resolve_user_key(
    legacy_user_id: Any,
    open_id: Any,
    uid_to_key: dict[str, str],
    openid_to_key: dict[str, str],
) -> str | None:
    uid = clean_text(legacy_user_id).strip()
    if uid and uid in uid_to_key:
        return uid_to_key[uid]
    oid = clean_text(open_id).strip()
    if oid and oid in openid_to_key:
        return openid_to_key[oid]
    return None


def dedupe_signups(
    rows: list[dict[str, Any]],
    activity_no_to_key: dict[str, str],
    openid_to_key: dict[str, str],
    uid_to_key: dict[str, str],
) -> tuple[list[dict[str, Any]], int, list[str]]:
    """Keep the latest signup per (activity, user); the schema is unique there."""
    latest: dict[tuple[str, str], tuple[float, dict[str, Any]]] = {}
    errors: list[str] = []
    for row in rows:
        fields = row.get("fields", {})
        activity = clean_text(fields.get("活动ID")).strip()
        activity_key = activity_no_to_key.get(activity)
        user_key = resolve_user_key(
            "", fields.get("报名人open_id"), uid_to_key, openid_to_key
        )
        if not activity_key or not user_key:
            continue
        created = field_number_ms(fields, "报名时间") or 0
        dedupe_key = (activity_key, user_key)
        previous = latest.get(dedupe_key)
        if previous is None or created >= previous[0]:
            latest[dedupe_key] = (created, row)
    kept_ids = {id(item[1]) for item in latest.values()}
    deduped = [row for row in rows if id(row) in kept_ids]
    skipped = len(rows) - len(deduped)
    return deduped, skipped, errors


# ---------------------------------------------------------------------------
# Export loading
# ---------------------------------------------------------------------------


def load_json(path: Path) -> Any:
    if not path.is_file():
        raise MigrationError(f"missing export file: {path}")
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except json.JSONDecodeError as exc:
        raise MigrationError(f"invalid JSON in {path.name}: {exc}") from exc


def load_export(export_dir: Path) -> dict[str, Any]:
    tables: dict[str, list[dict[str, Any]]] = {}
    for name in EXPORT_TABLES:
        payload = load_json(export_dir / f"{name}.json")
        if not isinstance(payload, list):
            raise MigrationError(f"{name}.json must contain a JSON array")
        tables[name] = payload
    manifest_path = export_dir / "manifest.json"
    manifest = load_json(manifest_path) if manifest_path.is_file() else {}
    points_path = export_dir / "points.db"
    if not points_path.is_file():
        raise MigrationError(f"missing points database: {points_path}")
    return {
        "tables": tables,
        "manifest": manifest,
        "points_db": points_path,
    }


def open_points_db(path: Path) -> sqlite3.Connection:
    conn = sqlite3.connect(f"file:{quote(str(path))}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def read_sqlite_rows(conn: sqlite3.Connection, table: str) -> list[dict[str, Any]]:
    exists = conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name=?", (table,)
    ).fetchone()
    if not exists:
        return []
    return [dict(row) for row in conn.execute(f'SELECT * FROM "{table}"')]


# ---------------------------------------------------------------------------
# PostgreSQL helpers
# ---------------------------------------------------------------------------


def connect_db(database_url: str, dry_run: bool) -> Any:
    try:
        import psycopg
    except ImportError as exc:  # pragma: no cover - dependency guard
        raise MigrationError(
            "psycopg is required: python3 -m pip install -r tools/requirements.txt"
        ) from exc
    try:
        # Autocommit is required so that every explicit conn.transaction()
        # block is a real transaction instead of a savepoint inside an
        # implicit transaction that would be rolled back on connection close.
        conn = psycopg.connect(database_url, autocommit=True)
    except Exception as exc:  # noqa: BLE001 - sanitized below
        raise MigrationError(f"cannot connect to PostgreSQL: {safe_error(exc)}") from exc
    if dry_run:
        conn.execute("SET default_transaction_read_only = on")
    return conn


def safe_error(exc: BaseException) -> str:
    """Sanitize DB/driver messages before they reach logs or the report."""
    message = str(exc)
    diag = getattr(exc, "diag", None)
    if diag is not None:
        primary = getattr(diag, "message_primary", None)
        sqlstate = getattr(diag, "sqlstate", None)
        if primary:
            message = f"{sqlstate or ''} {primary}".strip()
        else:
            message = exc.__class__.__name__
    message = re.sub(r"\b1[3-9]\d{9}\b", lambda m: mask_phone(m.group(0)), message)
    message = re.sub(r"\b\d{17}[\dXx]\b", lambda m: m.group(0)[:3] + "***********" + m.group(0)[-4:], message)
    message = message.replace("\n", " ")
    return message[:400]


def schema_report(conn: Any) -> dict[str, Any]:
    missing: dict[str, list[str]] = {}
    for table, columns in REQUIRED_COLUMNS.items():
        rows = conn.execute(
            "SELECT a.attname FROM pg_attribute a "
            "JOIN pg_class c ON c.oid = a.attrelid "
            "JOIN pg_namespace n ON n.oid = c.relnamespace "
            "WHERE n.nspname = current_schema() AND c.relname = %s "
            "AND a.attnum > 0 AND NOT a.attisdropped",
            (table,),
        ).fetchall()
        present = {row[0] for row in rows}
        absent = sorted(columns - present)
        if absent:
            missing[table] = absent
    versions: list[int] = []
    try:
        versions = [
            int(row[0])
            for row in conn.execute("SELECT version FROM schema_migrations").fetchall()
        ]
    except Exception:
        versions = []
    return {"missing_columns": missing, "migrations": sorted(versions)}


def load_existing(conn: Any) -> dict[str, Any]:
    user_rows = conn.execute(
        "SELECT id, legacy_user_id, legacy_record_id, legacy_open_id, phone, "
        "account_kind FROM users"
    ).fetchall()
    users_by_id: dict[int, dict[str, Any]] = {}
    users_by_legacy: dict[str, dict[str, Any]] = {}
    users_by_record: dict[str, dict[str, Any]] = {}
    users_by_phone: dict[str, dict[str, Any]] = {}
    for row in user_rows:
        data = {
            "id": row[0],
            "legacy_user_id": row[1] or "",
            "legacy_record_id": row[2] or "",
            "legacy_open_id": row[3] or "",
            "phone": row[4] or "",
            "account_kind": row[5] or "user",
        }
        users_by_id[data["id"]] = data
        if data["legacy_user_id"]:
            users_by_legacy[data["legacy_user_id"]] = data
        if data["legacy_record_id"]:
            users_by_record[data["legacy_record_id"]] = data
        if data["phone"]:
            users_by_phone[data["phone"]] = data

    media_by_token = {
        row[1]: {"id": row[0], "user_id": row[2]}
        for row in conn.execute(
            "SELECT id, legacy_file_token, user_id FROM media "
            "WHERE legacy_file_token IS NOT NULL"
        ).fetchall()
    }
    links: dict[int, set[str]] = {}
    max_order: dict[int, int] = {}
    has_cover: set[int] = set()
    for row in conn.execute(
        "SELECT user_id, media_id, sort_order, is_cover FROM profile_photos"
    ).fetchall():
        links.setdefault(row[0], set()).add(row[1])
        max_order[row[0]] = max(max_order.get(row[0], -1), row[2])
        if row[3]:
            has_cover.add(row[0])
    return {
        "users_by_id": users_by_id,
        "users_by_legacy": users_by_legacy,
        "users_by_record": users_by_record,
        "users_by_phone": users_by_phone,
        "media_by_token": media_by_token,
        "profile_links": links,
        "profile_max_order": max_order,
        "profile_has_cover": has_cover,
    }


def legacy_map(conn: Any, table: str, column: str) -> dict[str, int]:
    rows = conn.execute(
        f"SELECT {column}, id FROM {table} WHERE {column} IS NOT NULL"
    ).fetchall()
    return {str(row[0]): row[1] for row in rows}


def upsert_by_existing(
    conn: Any,
    table: str,
    columns: list[str],
    params: dict[str, Any],
    json_columns: set[str],
    existing_id: int | None,
    id_column: str = "id",
) -> int:
    placeholders = ", ".join(
        f"%({column})s::jsonb" if column in json_columns else f"%({column})s"
        for column in columns
    )
    if existing_id is None:
        sql = (
            f"INSERT INTO {table} ({', '.join(columns)}) VALUES ({placeholders}) "
            f"RETURNING {id_column}"
        )
        row = conn.execute(sql, params).fetchone()
        return int(row[0])
    assignments = ", ".join(
        f"{column} = %({column})s::jsonb" if column in json_columns
        else f"{column} = %({column})s"
        for column in columns
        if column != id_column
    )
    conn.execute(
        f"UPDATE {table} SET {assignments} WHERE {id_column} = %(row_id)s",
        {**params, "row_id": existing_id},
    )
    return existing_id


@dataclass
class Stats:
    source: int
    inserted: int = 0
    updated: int = 0
    skipped: int = 0
    errors: list[dict[str, Any]] = None  # type: ignore[assignment]
    notes: list[str] = None  # type: ignore[assignment]
    error_count: int = 0

    def __post_init__(self) -> None:
        self.errors = []
        self.notes = []
        self.error_count = 0

    def error(self, message: str, **context: Any) -> None:
        self.error_count += 1
        if len(self.errors) < 100:
            self.errors.append({"message": safe_error_text(message), **context})

    def note(self, message: str) -> None:
        if len(self.notes) < 100:
            self.notes.append(message)

    def as_dict(self) -> dict[str, Any]:
        out = {
            "source": self.source,
            "inserted": self.inserted,
            "updated": self.updated,
            "skipped": self.skipped,
            "error_count": self.error_count,
            "errors": self.errors,
            "notes": self.notes,
        }
        return out


def safe_error_text(message: str) -> str:
    message = re.sub(r"\b1[3-9]\d{9}\b", lambda m: mask_phone(m.group(0)), message)
    message = re.sub(r"\b\d{17}[\dXx]\b", lambda m: m.group(0)[:3] + "***********" + m.group(0)[-4:], message)
    return message[:400]


USER_COLUMNS = [
    "legacy_user_id", "legacy_open_id", "legacy_record_id", "phone", "name",
    "nickname", "gender", "birthday", "height_cm", "education",
    "baptismal_name", "church", "church_location", "church_group",
    "native_place", "city", "industry", "position", "income", "house",
    "driving", "marriage", "family", "live_with_parents", "personality",
    "self_traits", "self_hobbies", "self_sports", "mbti", "partner_criteria",
    "partner_traits", "partner_hobbies", "partner_sports", "wechat",
    "legacy_phone", "id_card_last4", "id_card_valid", "account_kind", "observer_enabled",
    "account_status", "status", "contact_consent", "referral_source",
    "is_for_child", "wechat_payment", "registered_at", "last_active_at",
    "created_at", "updated_at",
]
USER_JSON_COLUMNS = {
    "self_traits", "self_hobbies", "self_sports", "mbti", "partner_traits",
    "partner_hobbies", "partner_sports",
}


def user_params(record: dict[str, Any], phone: str) -> dict[str, Any]:
    return {
        "legacy_user_id": record["legacy_user_id"],
        "legacy_open_id": record["legacy_open_id"],
        "legacy_record_id": record["legacy_record_id"],
        "phone": phone,
        "name": record["name"],
        "nickname": record["nickname"],
        "gender": record["gender"],
        "birthday": record["birthday"],
        "height_cm": record["height_cm"],
        "education": record["education"],
        "baptismal_name": record["baptismal_name"],
        "church": record["church"],
        "church_location": record["church_location"],
        "church_group": record["church_group"],
        "native_place": record["native_place"],
        "city": record["city"],
        "industry": record["industry"],
        "position": record["position"],
        "income": record["income"],
        "house": record["house"],
        "driving": record["driving"],
        "marriage": record["marriage"],
        "family": record["family"],
        "live_with_parents": record["live_with_parents"],
        "personality": record["personality"],
        "self_traits": json_text(record["self_traits"]),
        "self_hobbies": json_text(record["self_hobbies"]),
        "self_sports": json_text(record["self_sports"]),
        "mbti": json_text(record["mbti"]),
        "partner_criteria": record["partner_criteria"],
        "partner_traits": json_text(record["partner_traits"]),
        "partner_hobbies": json_text(record["partner_hobbies"]),
        "partner_sports": json_text(record["partner_sports"]),
        "wechat": record["wechat"],
        "legacy_phone": record["legacy_phone"],
        "id_card_last4": record["id_card_last4"],
        "id_card_valid": record["id_card_valid"],
        "account_kind": record["account_kind"],
        "observer_enabled": record["observer_enabled"],
        "account_status": record["account_status"],
        "status": record["status"],
        "contact_consent": record["contact_consent"],
        "referral_source": record["referral_source"],
        "is_for_child": record["is_for_child"],
        "wechat_payment": record["wechat_payment"],
        "registered_at": record["registered_at"],
        "last_active_at": record["last_active_at"],
        "created_at": record["created_at"],
        "updated_at": record["updated_at"],
    }


def process_users(
    conn: Any,
    user_records: list[dict[str, Any]],
    observer_records: list[dict[str, Any]],
    assignments: dict[str, dict[str, Any]],
    existing: dict[str, Any],
    apply: bool,
) -> tuple[Stats, Stats, dict[str, int]]:
    user_stats = Stats(len(user_records))
    observer_stats = Stats(len(observer_records))
    key_to_id: dict[str, int] = {}

    def existing_row(record: dict[str, Any]) -> dict[str, Any] | None:
        return (
            existing["users_by_record"].get(record["legacy_record_id"])
            or existing["users_by_legacy"].get(record["legacy_user_id"])
        )

    if not apply:
        for record in user_records:
            if existing_row(record):
                user_stats.updated += 1
            else:
                user_stats.inserted += 1
        for record in observer_records:
            if existing_row(record):
                observer_stats.updated += 1
            else:
                observer_stats.inserted += 1

    for record in user_records + observer_records:
        if record.get("status_warning"):
            (user_stats if record["kind"] == "user" else observer_stats).note(
                f"{record['key']}: {record['status_warning']}"
            )
        if record["birthday"] and record["birthday"].year < 1940:
            (user_stats if record["kind"] == "user" else observer_stats).note(
                f"{record['key']}: birthday is before 1940 ({record['birthday']}); kept as exported"
            )

    if not apply:
        for record in user_records + observer_records:
            row = existing_row(record)
            key_to_id[record["key"]] = row["id"] if row else -1
        return user_stats, observer_stats, key_to_id

    all_records = user_records + observer_records
    stats_for = lambda r: user_stats if r["kind"] == "user" else observer_stats

    with conn.transaction():
        existing_ids = [
            row["id"]
            for row in (existing_row(r) for r in all_records)
            if row is not None
        ]
        if existing_ids:
            # Free phones that may be handed to another source row in this run.
            conn.execute(
                "UPDATE users SET phone = 'tmp-migrate-' || id::text "
                "WHERE id = ANY(%s)",
                (existing_ids,),
            )
        for record in all_records:
            stats = stats_for(record)
            row = existing_row(record)
            try:
                with conn.transaction():
                    params = user_params(
                        record, assignments[record["key"]]["assigned"]
                    )
                    user_id = upsert_by_existing(
                        conn,
                        "users",
                        USER_COLUMNS,
                        params,
                        USER_JSON_COLUMNS,
                        row["id"] if row else None,
                    )
                key_to_id[record["key"]] = user_id
                if row:
                    stats.updated += 1
                else:
                    stats.inserted += 1
            except Exception as exc:  # noqa: BLE001 - row-level savepoint
                stats.error(
                    f"user upsert failed: {safe_error(exc)}",
                    legacy_user_id=record["key"],
                )
        if user_stats.error_count or observer_stats.error_count:
            # Existing users were moved to temporary phones to avoid unique
            # conflicts. Do not commit a partial user table with those phones.
            raise MigrationError(
                "user import had row errors; the users transaction was rolled back"
            )
    return user_stats, observer_stats, key_to_id


def process_activities(
    conn: Any, rows: list[dict[str, Any]], existing_ids: dict[str, int], apply: bool
) -> tuple[Stats, dict[str, int], dict[str, int]]:
    stats = Stats(len(rows))
    key_to_id: dict[str, int] = {}
    record_to_id: dict[str, int] = {}
    columns = [
        "legacy_record_id", "activity_no", "name", "description", "location",
        "condition", "max_signup", "current_signup", "fee", "food", "status",
        "group_status", "male_per_group", "female_per_group", "group_enabled",
        "onetoone_status", "onetoone_enabled", "signin_enabled", "signin_code",
        "publish_at", "start_at", "end_at", "created_at", "updated_at",
    ]
    for row in rows:
        record = build_activity_record(row)
        legacy_id = record["legacy_record_id"]
        existing_id = existing_ids.get(legacy_id)
        if record["poster_tokens"]:
            stats.note(
                f"{record['activity_no']}: poster {record['poster_tokens'][0]} not migrated "
                "(poster upload is out of scope in this tool)"
            )
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            key_to_id[record["key"]] = existing_id or -1
            record_to_id[legacy_id] = existing_id or -1
            continue
        try:
            with conn.transaction():
                params = {
                    **record,
                    "poster_media_id": None,
                }
                activity_id = upsert_by_existing(
                    conn,
                    "activities",
                    columns,
                    params,
                    set(),
                    existing_id,
                )
            key_to_id[record["key"]] = activity_id
            record_to_id[legacy_id] = activity_id
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"activity upsert failed: {safe_error(exc)}",
                activity_no=record["activity_no"],
            )
    return stats, key_to_id, record_to_id


def process_likes(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "initiator_id", "target_id", "initiator_name",
        "target_name", "status", "like_type", "message", "month", "created_at",
    ]
    uid_to_key = ctx["uid_to_key"]
    openid_to_key = ctx["openid_to_key"]
    key_to_id = ctx["key_to_id"]
    for row in rows:
        fields = row.get("fields", {})
        initiator = resolve_user_key(
            fields.get("发起用户ID"), fields.get("发起用户open_id"),
            uid_to_key, openid_to_key,
        )
        target = resolve_user_key(
            fields.get("目标用户ID"), fields.get("目标用户open_id"),
            uid_to_key, openid_to_key,
        )
        if not initiator or not target:
            stats.error(
                "unresolved like user",
                record_id=row["record_id"],
                initiator=clean_text(fields.get("发起用户ID")),
                target=clean_text(fields.get("目标用户ID")),
            )
            continue
        if initiator == target:
            stats.error("self like", record_id=row["record_id"])
            continue
        created_at = ms_to_datetime(field_number_ms(fields, "创建时间"))
        month = clean_text(fields.get("归属月份")).strip()
        if not month and created_at:
            month = created_at.astimezone(SHANGHAI).strftime("%Y-%m")
        params = {
            "legacy_record_id": row["record_id"],
            "initiator_id": key_to_id.get(initiator),
            "target_id": key_to_id.get(target),
            "initiator_name": clean_text(fields.get("发起用户昵称")).strip(),
            "target_name": clean_text(fields.get("目标用户昵称")).strip(),
            "status": clean_text(fields.get("状态")).strip() or "单向喜欢",
            "like_type": clean_text(fields.get("喜欢类型")).strip() or "匿名",
            "message": clean_text(fields.get("附言")).strip(),
            "month": month,
            "created_at": created_at or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        if not params["initiator_id"] or not params["target_id"]:
            stats.error("like user was not imported", record_id=row["record_id"])
            continue
        try:
            with conn.transaction():
                upsert_by_existing(
                    conn, "likes", columns, params, set(), existing_id
                )
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"like upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_praises(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "initiator_id", "target_id", "status",
        "object_type", "object_id", "day", "week", "created_at",
    ]
    uid_to_key = ctx["uid_to_key"]
    openid_to_key = ctx["openid_to_key"]
    key_to_id = ctx["key_to_id"]
    for row in rows:
        fields = row.get("fields", {})
        initiator = resolve_user_key(
            "", fields.get("点赞人open_id"), uid_to_key, openid_to_key
        )
        target = resolve_user_key(
            "", fields.get("被点赞人open_id"), uid_to_key, openid_to_key
        )
        if not initiator or not target:
            stats.error("unresolved praise user", record_id=row["record_id"])
            continue
        if initiator == target:
            stats.error("self praise", record_id=row["record_id"])
            continue
        created_at = parse_local_timestamp(fields.get("创建时间"))
        params = {
            "legacy_record_id": row["record_id"],
            "initiator_id": key_to_id.get(initiator),
            "target_id": key_to_id.get(target),
            "status": clean_text(fields.get("状态")).strip() or "有效",
            "object_type": clean_text(fields.get("点赞对象类型")).strip() or "profile",
            "object_id": clean_text(fields.get("点赞对象ID")).strip(),
            "day": clean_text(fields.get("归属日期")).strip(),
            "week": clean_text(fields.get("归属周")).strip(),
            "created_at": created_at or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        if not params["initiator_id"] or not params["target_id"]:
            stats.error("praise user was not imported", record_id=row["record_id"])
            continue
        try:
            with conn.transaction():
                upsert_by_existing(
                    conn, "praises", columns, params, set(), existing_id
                )
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"praise upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_messages(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> tuple[Stats, dict[str, int]]:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "target_user_id", "author_id", "author_name",
        "content", "status", "created_at",
    ]
    uid_to_key = ctx["uid_to_key"]
    openid_to_key = ctx["openid_to_key"]
    key_to_id = ctx["key_to_id"]
    legacy_to_new: dict[str, int] = dict(existing_ids)
    pending_parents: list[tuple[str, str, str]] = []

    for row in rows:
        fields = row.get("fields", {})
        author = resolve_user_key(
            fields.get("留言人用户ID"), fields.get("留言人open_id"),
            uid_to_key, openid_to_key,
        )
        target = resolve_user_key(
            "", fields.get("目标用户open_id"), uid_to_key, openid_to_key
        )
        if not author or not target:
            stats.error(
                "unresolved message user",
                record_id=row["record_id"],
                author=clean_text(fields.get("留言人用户ID")),
            )
            continue
        params = {
            "legacy_record_id": row["record_id"],
            "target_user_id": key_to_id.get(target),
            "author_id": key_to_id.get(author),
            "author_name": clean_text(fields.get("留言人昵称")).strip(),
            "content": clean_text(fields.get("内容")),
            "status": clean_text(fields.get("状态")).strip() or "正常",
            "created_at": ms_to_datetime(field_number_ms(fields, "创建时间")) or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            pending_parents.append(
                (row["record_id"], clean_text(fields.get("父留言ID")).strip(),
                 clean_text(fields.get("被回复留言ID")).strip())
            )
            continue
        if not params["target_user_id"] or not params["author_id"]:
            stats.error("message user was not imported", record_id=row["record_id"])
            continue
        try:
            with conn.transaction():
                message_id = upsert_by_existing(
                    conn, "messages", columns, params, set(), existing_id
                )
            legacy_to_new[row["record_id"]] = message_id
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            pending_parents.append(
                (row["record_id"], clean_text(fields.get("父留言ID")).strip(),
                 clean_text(fields.get("被回复留言ID")).strip())
            )
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"message upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )

    missing_parent_refs = 0
    if apply:
        with conn.transaction():
            for record_id, parent_legacy, reply_legacy in pending_parents:
                message_id = legacy_to_new.get(record_id)
                if message_id is None:
                    continue
                parent_id = legacy_to_new.get(parent_legacy) if parent_legacy else None
                reply_id = legacy_to_new.get(reply_legacy) if reply_legacy else None
                if parent_legacy and parent_id is None:
                    missing_parent_refs += 1
                if reply_legacy and reply_id is None:
                    missing_parent_refs += 1
                if message_id > 0:
                    conn.execute(
                        "UPDATE messages SET parent_id = %s, reply_to_id = %s "
                        "WHERE id = %s",
                        (parent_id, reply_id, message_id),
                    )
    else:
        source_ids = {row["record_id"] for row in rows}
        for _record_id, parent_legacy, reply_legacy in pending_parents:
            if parent_legacy and parent_legacy not in source_ids:
                missing_parent_refs += 1
            if reply_legacy and reply_legacy not in source_ids:
                missing_parent_refs += 1
    if missing_parent_refs:
        stats.note(f"{missing_parent_refs} parent/reply references not found; stored as NULL")
    return stats, legacy_to_new


def process_reports(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "reporter_id", "target_user_id",
        "target_message_id", "reporter_name", "target_name", "reason",
        "status", "handled_at", "created_at",
    ]
    uid_to_key = ctx["uid_to_key"]
    for row in rows:
        fields = row.get("fields", {})
        reporter_uid = legacy_uid_from_label(fields.get("举报人昵称"))
        target_uid = legacy_uid_from_label(fields.get("被举报人昵称"))
        reporter = resolve_user_key(reporter_uid, "", uid_to_key, {})
        target = resolve_user_key(target_uid, "", uid_to_key, {})
        status = clean_text(fields.get("处理状态")).strip() or "待处理"
        handled_at = (
            ms_to_datetime(field_number_ms(fields, "处理时间"))
            if status != "待处理" else None
        )
        params = {
            "legacy_record_id": row["record_id"],
            "reporter_id": ctx["key_to_id"].get(reporter) if reporter else None,
            "target_user_id": ctx["key_to_id"].get(target) if target else None,
            "target_message_id": None,
            "reporter_name": strip_uid_label(fields.get("举报人昵称")),
            "target_name": strip_uid_label(fields.get("被举报人昵称")),
            "reason": clean_text(fields.get("举报原因")).strip() or "(empty)",
            "status": status,
            "handled_at": handled_at,
            "created_at": ms_to_datetime(field_number_ms(fields, "举报时间")) or NOW(),
        }
        if not reporter or not target:
            stats.note(
                f"{row['record_id']}: reporter/target id not found; stored NULL "
                f"({reporter_uid or '?'} -> {target_uid or '?'})"
            )
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                upsert_by_existing(
                    conn, "reports", columns, params, set(), existing_id
                )
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"report upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_suggestions(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "author_id", "author_name", "type", "content",
        "status", "created_at",
    ]
    uid_to_key = ctx["uid_to_key"]
    for row in rows:
        fields = row.get("fields", {})
        author_uid = clean_text(fields.get("用户ID")).strip()
        author = resolve_user_key(author_uid, "", uid_to_key, {})
        if not author:
            stats.note(f"{row['record_id']}: author {author_uid or '?'} not found; stored NULL")
        params = {
            "legacy_record_id": row["record_id"],
            "author_id": ctx["key_to_id"].get(author) if author else None,
            "author_name": clean_text(fields.get("反馈人")).strip(),
            "type": clean_text(fields.get("类型")).strip(),
            "content": clean_text(fields.get("内容")),
            "status": "待处理",
            "created_at": ms_to_datetime(field_number_ms(fields, "提交时间")) or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                upsert_by_existing(
                    conn, "suggestions", columns, params, set(), existing_id
                )
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"suggestion upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_signups(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    source_count = len(rows)
    rows, duplicate_count, _ = dedupe_signups(
        rows,
        ctx["activity_no_to_key"],
        ctx["openid_to_key"],
        ctx["uid_to_key"],
    )
    stats = Stats(source_count)
    stats.skipped += duplicate_count
    if duplicate_count:
        stats.note(f"{duplicate_count} duplicate (activity,user) signups collapsed to the latest row")
    columns = [
        "legacy_record_id", "activity_id", "user_id", "nickname", "status",
        "attendance", "created_at",
    ]
    activity_key_to_id = ctx["activity_key_to_id"]
    key_to_id = ctx["key_to_id"]
    for row in rows:
        fields = row.get("fields", {})
        activity_key = ctx["activity_no_to_key"].get(
            clean_text(fields.get("活动ID")).strip()
        )
        user_key = resolve_user_key(
            "", fields.get("报名人open_id"), ctx["uid_to_key"], ctx["openid_to_key"]
        )
        if not activity_key or not user_key:
            stats.error("unresolved signup", record_id=row["record_id"])
            continue
        activity_id = activity_key_to_id.get(activity_key)
        user_id = key_to_id.get(user_key)
        if apply and (not activity_id or not user_id):
            stats.error("signup references missing activity/user", record_id=row["record_id"])
            continue
        params = {
            "legacy_record_id": row["record_id"],
            "activity_id": activity_id,
            "user_id": user_id,
            "nickname": clean_text(fields.get("报名人昵称")).strip(),
            "status": clean_text(fields.get("状态")).strip() or "已报名",
            "attendance": clean_text(fields.get("签到状态")).strip(),
            "created_at": ms_to_datetime(field_number_ms(fields, "报名时间")) or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                business = conn.execute(
                    "SELECT id FROM signups WHERE activity_id = %s AND user_id = %s",
                    (activity_id, user_id),
                ).fetchone()
                target_id = existing_id or (business[0] if business else None)
                upsert_by_existing(
                    conn, "signups", columns, params, set(), target_id
                )
            if existing_id or target_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"signup upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_group_selections(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "activity_id", "selector_id", "gender", "choices",
        "round", "updated_at",
    ]
    for row in rows:
        fields = row.get("fields", {})
        activity_key = ctx["activity_no_to_key"].get(
            clean_text(fields.get("活动ID")).strip()
        )
        selector = resolve_user_key(
            "", fields.get("选择人open_id"), ctx["uid_to_key"], ctx["openid_to_key"]
        )
        if not activity_key or not selector:
            stats.error("unresolved group selection", record_id=row["record_id"])
            continue
        choice_keys: list[str] = []
        missing = 0
        empty = 0
        for index in range(1, 8):
            oid = clean_text(fields.get(f"第{index}志愿")).strip()
            if not oid:
                empty += 1
                continue
            key = ctx["openid_to_key"].get(oid)
            if key:
                choice_keys.append(key)
            else:
                missing += 1
        if empty:
            stats.note(f"{row['record_id']}: {empty} empty choice slots")
        if missing:
            stats.note(
                f"{row['record_id']}: {missing} of 7 choices unresolved; omitted from array"
            )
        activity_id = ctx["activity_key_to_id"].get(activity_key)
        selector_id = ctx["key_to_id"].get(selector)
        if apply and (not activity_id or not selector_id):
            stats.error("group selection references missing activity/user", record_id=row["record_id"])
            continue
        choices = (
            [ctx["key_to_id"].get(key) for key in choice_keys]
            if apply else choice_keys
        )
        choices = [choice for choice in choices if choice is not None]
        params = {
            "legacy_record_id": row["record_id"],
            "activity_id": activity_id,
            "selector_id": selector_id,
            "gender": clean_text(fields.get("选择人性别")).strip(),
            "choices": json_text(choices),
            "round": field_int(fields, "轮次", 1) or 1,
            "updated_at": ms_to_datetime(field_number_ms(fields, "提交时间")) or NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                business = conn.execute(
                    "SELECT id FROM group_selections WHERE activity_id = %s AND selector_id = %s",
                    (activity_id, selector_id),
                ).fetchone()
                target_id = existing_id or (business[0] if business else None)
                upsert_by_existing(
                    conn, "group_selections", columns, params, {"choices"}, target_id
                )
            if target_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"group selection upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_group_results(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "activity_id", "group_no", "user_id", "user_name",
        "gender", "round", "created_at",
    ]
    for row in rows:
        fields = row.get("fields", {})
        activity_key = ctx["activity_no_to_key"].get(
            clean_text(fields.get("活动ID")).strip()
        )
        user_key = resolve_user_key(
            "", fields.get("用户open_id"), ctx["uid_to_key"], ctx["openid_to_key"]
        )
        if not activity_key or not user_key:
            stats.error("unresolved group result", record_id=row["record_id"])
            continue
        activity_id = ctx["activity_key_to_id"].get(activity_key)
        user_id = ctx["key_to_id"].get(user_key)
        if apply and (not activity_id or not user_id):
            stats.error("group result references missing activity/user", record_id=row["record_id"])
            continue
        group_no = field_int(fields, "组号", 0)
        params = {
            "legacy_record_id": row["record_id"],
            "activity_id": activity_id,
            "group_no": group_no,
            "user_id": user_id,
            "user_name": clean_text(fields.get("用户昵称")).strip(),
            "gender": clean_text(fields.get("用户性别")).strip(),
            "round": field_int(fields, "轮次", 1) or 1,
            "created_at": NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                business = conn.execute(
                    "SELECT id FROM group_results WHERE activity_id = %s "
                    "AND round = %s AND group_no = %s AND user_id = %s",
                    (activity_id, params["round"], group_no, user_id),
                ).fetchone()
                target_id = existing_id or (business[0] if business else None)
                upsert_by_existing(
                    conn, "group_results", columns, params, set(), target_id
                )
            if target_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"group result upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


def process_onetoone_results(
    conn: Any,
    rows: list[dict[str, Any]],
    ctx: dict[str, Any],
    existing_ids: dict[str, int],
    apply: bool,
) -> Stats:
    stats = Stats(len(rows))
    columns = [
        "legacy_record_id", "activity_id", "rank", "user_id", "user_name",
        "gender", "target_id", "target_name", "round", "created_at",
    ]
    for row in rows:
        fields = row.get("fields", {})
        activity_key = ctx["activity_no_to_key"].get(
            clean_text(fields.get("活动ID")).strip()
        )
        user_key = resolve_user_key(
            "", fields.get("用户open_id"), ctx["uid_to_key"], ctx["openid_to_key"]
        )
        target_key = resolve_user_key(
            "", fields.get("必聊对象open_id"), ctx["uid_to_key"], ctx["openid_to_key"]
        )
        if not activity_key or not user_key or not target_key:
            stats.error("unresolved onetoone result", record_id=row["record_id"])
            continue
        activity_id = ctx["activity_key_to_id"].get(activity_key)
        user_id = ctx["key_to_id"].get(user_key)
        target_id = ctx["key_to_id"].get(target_key)
        if apply and (not activity_id or not user_id or not target_id):
            stats.error("onetoone result references missing activity/user", record_id=row["record_id"])
            continue
        params = {
            "legacy_record_id": row["record_id"],
            "activity_id": activity_id,
            "rank": field_int(fields, "排名", 0),
            "user_id": user_id,
            "user_name": clean_text(fields.get("用户昵称")).strip(),
            "gender": clean_text(fields.get("用户性别")).strip(),
            "target_id": target_id,
            "target_name": clean_text(fields.get("必聊对象昵称")).strip(),
            "round": field_int(fields, "轮次", 1) or 1,
            "created_at": NOW(),
        }
        existing_id = existing_ids.get(row["record_id"])
        if not apply:
            if existing_id:
                stats.updated += 1
            else:
                stats.inserted += 1
            continue
        try:
            with conn.transaction():
                business = conn.execute(
                    "SELECT id FROM onetoone_results WHERE activity_id = %s "
                    "AND round = %s AND user_id = %s AND target_id = %s",
                    (activity_id, params["round"], user_id, target_id),
                ).fetchone()
                target_row_id = existing_id or (business[0] if business else None)
                upsert_by_existing(
                    conn, "onetoone_results", columns, params, set(), target_row_id
                )
            if target_row_id:
                stats.updated += 1
            else:
                stats.inserted += 1
        except Exception as exc:  # noqa: BLE001
            stats.error(
                f"onetoone upsert failed: {safe_error(exc)}",
                record_id=row["record_id"],
            )
    return stats


# ---------------------------------------------------------------------------
# Points
# ---------------------------------------------------------------------------


def reconcile_ledger(
    rows: Iterable[dict[str, Any]],
    openid_to_key: dict[str, str],
) -> dict[str, Any]:
    grouped: dict[str, list[dict[str, Any]]] = {}
    unresolved: list[str] = []
    for row in rows:
        oid = str(row.get("user_oid") or "")
        key = openid_to_key.get(oid)
        if not key:
            unresolved.append(oid)
            continue
        grouped.setdefault(key, []).append(row)

    balances: list[dict[str, Any]] = []
    running_mismatches: list[dict[str, Any]] = []
    for key, user_rows in sorted(grouped.items()):
        user_rows.sort(key=lambda r: int(r.get("id") or 0))
        running = 0
        for row in user_rows:
            running += int(row.get("delta") or 0)
            after = int(row.get("balance_after") or 0)
            if running != after:
                running_mismatches.append(
                    {
                        "legacy_user_id": key,
                        "legacy_id": str(row.get("id")),
                        "expected_balance_after": running,
                        "actual_balance_after": after,
                    }
                )
        total = sum(int(row.get("delta") or 0) for row in user_rows)
        last_after = int(user_rows[-1].get("balance_after") or 0)
        balances.append(
            {
                "legacy_user_id": key,
                "rows": len(user_rows),
                "sum_delta": total,
                "last_balance_after": last_after,
                "match": total == last_after,
            }
        )
    return {
        "balances": balances,
        "running_mismatches": running_mismatches,
        "unresolved_user_oids": sorted(set(unresolved)),
    }


def process_points(
    conn: Any,
    points_path: Path,
    ctx: dict[str, Any],
    apply: bool,
) -> dict[str, Any]:
    db = open_points_db(points_path)
    try:
        ledger_rows = read_sqlite_rows(db, "ledger")
        redemption_rows = read_sqlite_rows(db, "redemptions")
        invite_rows = read_sqlite_rows(db, "invites")
        historical_rows = read_sqlite_rows(db, "historical_participants")
        config_rows = read_sqlite_rows(db, "config")
    finally:
        db.close()

    reconciliation = reconcile_ledger(ledger_rows, ctx["openid_to_key"])
    result: dict[str, Any] = {
        "ledger": Stats(len(ledger_rows)).as_dict(),
        "redemptions": Stats(len(redemption_rows)).as_dict(),
        "invites": Stats(len(invite_rows)).as_dict(),
        "historical_participants": Stats(len(historical_rows)).as_dict(),
        "app_config": Stats(len(config_rows)).as_dict(),
        "reconciliation": reconciliation,
        "db_balances": [],
    }

    def resolve_oid(oid: Any) -> str | None:
        oid = str(oid or "")
        return ctx["openid_to_key"].get(oid)

    def dry_error(bucket: dict[str, Any], message: str, **context: Any) -> None:
        bucket["error_count"] += 1
        if len(bucket["errors"]) < 100:
            bucket["errors"].append({"message": safe_error_text(message), **context})

    if not apply:
        existing_ledger = set()
        existing_redemptions = set()
        existing_invites = set()
        if conn is not None:
            existing_ledger = set(legacy_map(conn, "points_ledger", "legacy_id"))
            existing_redemptions = set(
                legacy_map(conn, "points_redemptions", "legacy_id")
            )
            existing_invites = set(legacy_map(conn, "invites", "legacy_id"))
        for row in ledger_rows:
            if not resolve_oid(row.get("user_oid")):
                dry_error(
                    result["ledger"],
                    "unresolved ledger user",
                    legacy_id=str(row.get("id")),
                    open_id=str(row.get("user_oid") or ""),
                )
            elif str(row.get("id")) in existing_ledger:
                result["ledger"]["updated"] += 1
            else:
                result["ledger"]["inserted"] += 1
        for row in redemption_rows:
            if not resolve_oid(row.get("user_oid")):
                dry_error(
                    result["redemptions"],
                    "unresolved redemption user",
                    legacy_id=str(row.get("id")),
                )
            elif str(row.get("id")) in existing_redemptions:
                result["redemptions"]["updated"] += 1
            else:
                result["redemptions"]["inserted"] += 1
        for row in invite_rows:
            if not resolve_oid(row.get("inviter_oid")):
                dry_error(
                    result["invites"],
                    "unresolved invite inviter",
                    legacy_id=str(row.get("id")),
                )
            elif str(row.get("id")) in existing_invites:
                result["invites"]["updated"] += 1
            else:
                result["invites"]["inserted"] += 1
        result["historical_participants"]["inserted"] = len(historical_rows)
        result["app_config"]["inserted"] = len(config_rows)
        return result

    ledger_stats = Stats(len(ledger_rows))
    ledger_columns = [
        "legacy_id", "user_id", "delta", "balance_after", "kind", "reason",
        "ref_type", "ref_id", "operator_id", "created_at", "idempotency_key",
    ]
    existing_ledger_map = legacy_map(conn, "points_ledger", "legacy_id")
    legacy_ledger_to_new = dict(existing_ledger_map)

    with conn.transaction():
        for row in ledger_rows:
            key = resolve_oid(row.get("user_oid"))
            if not key or not ctx["key_to_id"].get(key):
                ledger_stats.error(
                    "unresolved ledger user",
                    legacy_id=str(row.get("id")),
                    open_id=str(row.get("user_oid") or ""),
                )
                continue
            legacy_id = str(row.get("id"))
            operator_key = resolve_oid(row.get("operator_oid"))
            params = {
                "legacy_id": legacy_id,
                "user_id": ctx["key_to_id"][key],
                "delta": int(row.get("delta") or 0),
                "balance_after": int(row.get("balance_after") or 0),
                "kind": str(row.get("kind") or "legacy"),
                "reason": str(row.get("reason") or ""),
                "ref_type": str(row.get("ref_type") or ""),
                "ref_id": str(row.get("ref_id") or ""),
                "operator_id": (
                    ctx["key_to_id"].get(operator_key) if operator_key else None
                ),
                "created_at": parse_local_timestamp(row.get("created_at")) or NOW(),
                "idempotency_key": str(row.get("idempotency_key") or "") or None,
            }
            existing_id = existing_ledger_map.get(legacy_id)
            if existing_id:
                ledger_stats.updated += 1
                continue
            if params["delta"] == 0:
                ledger_stats.error("zero delta ledger row skipped", legacy_id=legacy_id)
                continue
            try:
                with conn.transaction():
                    ledger_id = upsert_by_existing(
                        conn, "points_ledger", ledger_columns, params, set(), None
                    )
                ledger_stats.inserted += 1
                legacy_ledger_to_new[legacy_id] = ledger_id
            except Exception as exc:  # noqa: BLE001
                ledger_stats.error(
                    f"ledger insert failed: {safe_error(exc)}",
                    legacy_id=legacy_id,
                )
    result["ledger"] = ledger_stats.as_dict()

    redemption_stats = Stats(len(redemption_rows))
    redemption_columns = [
        "legacy_id", "user_id", "item", "cost", "status", "params",
        "idempotency_key", "ledger_id", "refund_ledger_id", "refund_key",
        "ref_id", "created_at", "resolved_at", "note",
    ]
    existing_redemption_map = legacy_map(conn, "points_redemptions", "legacy_id")
    with conn.transaction():
        for row in redemption_rows:
            key = resolve_oid(row.get("user_oid"))
            if not key or not ctx["key_to_id"].get(key):
                redemption_stats.error(
                    "unresolved redemption user",
                    legacy_id=str(row.get("id")),
                )
                continue
            legacy_id = str(row.get("id"))
            if legacy_id in existing_redemption_map:
                redemption_stats.updated += 1
                continue
            try:
                params_json = json.loads(row.get("params") or "{}")
            except (TypeError, json.JSONDecodeError):
                params_json = {}
                redemption_stats.note(f"{legacy_id}: invalid params JSON stored as {{}}")
            ledger_ref = row.get("ledger_id")
            refund_ref = row.get("refund_ledger_id")
            params = {
                "legacy_id": legacy_id,
                "user_id": ctx["key_to_id"][key],
                "item": str(row.get("item") or "legacy"),
                "cost": int(row.get("cost") or 0),
                "status": str(row.get("status") or "active"),
                "params": json_text(params_json),
                "idempotency_key": str(row.get("idempotency_key") or "")
                or f"legacy:redemption:{legacy_id}",
                "ledger_id": legacy_ledger_to_new.get(str(ledger_ref))
                if ledger_ref is not None else None,
                "refund_ledger_id": legacy_ledger_to_new.get(str(refund_ref))
                if refund_ref is not None else None,
                "refund_key": str(row.get("refund_key") or "") or None,
                "ref_id": str(row.get("ref_id") or ""),
                "created_at": parse_local_timestamp(row.get("created_at")) or NOW(),
                "resolved_at": parse_local_timestamp(row.get("resolved_at")),
                "note": str(row.get("note") or ""),
            }
            try:
                with conn.transaction():
                    upsert_by_existing(
                        conn,
                        "points_redemptions",
                        redemption_columns,
                        params,
                        {"params"},
                        None,
                    )
                redemption_stats.inserted += 1
            except Exception as exc:  # noqa: BLE001
                redemption_stats.error(
                    f"redemption insert failed: {safe_error(exc)}",
                    legacy_id=legacy_id,
                )
    result["redemptions"] = redemption_stats.as_dict()

    invite_stats = Stats(len(invite_rows))
    invite_columns = [
        "legacy_id", "inviter_id", "invitee_phone", "invitee_id", "status",
        "gender", "source", "created_at", "updated_at", "bound_at",
        "confirm_due_at", "confirmed_at", "reward_ledger_id", "note",
    ]
    existing_invite_map = legacy_map(conn, "invites", "legacy_id")
    with conn.transaction():
        for row in invite_rows:
            key = resolve_oid(row.get("inviter_oid"))
            if not key or not ctx["key_to_id"].get(key):
                invite_stats.error(
                    "unresolved invite inviter", legacy_id=str(row.get("id"))
                )
                continue
            legacy_id = str(row.get("id"))
            if legacy_id in existing_invite_map:
                invite_stats.updated += 1
                continue
            invitee_key = resolve_oid(row.get("invitee_oid"))
            reward_ref = row.get("reward_ledger_id")
            params = {
                "legacy_id": legacy_id,
                "inviter_id": ctx["key_to_id"][key],
                "invitee_phone": str(row.get("invitee_phone") or ""),
                "invitee_id": ctx["key_to_id"].get(invitee_key)
                if invitee_key else None,
                "status": str(row.get("status") or "pending"),
                "gender": str(row.get("gender") or ""),
                "source": str(row.get("source") or ""),
                "created_at": parse_local_timestamp(row.get("created_at")) or NOW(),
                "updated_at": parse_local_timestamp(row.get("updated_at"))
                or parse_local_timestamp(row.get("created_at")) or NOW(),
                "bound_at": parse_local_timestamp(row.get("bound_at")),
                "confirm_due_at": parse_local_timestamp(row.get("confirm_due_at")),
                "confirmed_at": parse_local_timestamp(row.get("confirmed_at")),
                "reward_ledger_id": legacy_ledger_to_new.get(str(reward_ref))
                if reward_ref is not None else None,
                "note": str(row.get("note") or ""),
            }
            try:
                with conn.transaction():
                    upsert_by_existing(
                        conn, "invites", invite_columns, params, set(), None
                    )
                invite_stats.inserted += 1
            except Exception as exc:  # noqa: BLE001
                invite_stats.error(
                    f"invite insert failed: {safe_error(exc)}",
                    legacy_id=legacy_id,
                )
    result["invites"] = invite_stats.as_dict()

    historical_stats = Stats(len(historical_rows))
    with conn.transaction():
        for row in historical_rows:
            params = {
                "phone": str(row.get("phone") or ""),
                "name": str(row.get("name") or ""),
                "source": str(row.get("source") or ""),
                "added_at": parse_local_timestamp(row.get("added_at")) or NOW(),
            }
            if not params["phone"]:
                historical_stats.error("historical participant without phone")
                continue
            conn.execute(
                "INSERT INTO historical_participants (phone, name, source, added_at) "
                "VALUES (%(phone)s, %(name)s, %(source)s, %(added_at)s) "
                "ON CONFLICT (phone) DO UPDATE SET name = EXCLUDED.name, "
                "source = EXCLUDED.source",
                params,
            )
            historical_stats.inserted += 1
    result["historical_participants"] = historical_stats.as_dict()

    config_stats = Stats(len(config_rows))
    with conn.transaction():
        for row in config_rows:
            operator_key = resolve_oid(row.get("operator_oid"))
            params = {
                "key": str(row.get("key") or ""),
                "value": str(row.get("value") or ""),
                "updated_at": parse_local_timestamp(row.get("updated_at")) or NOW(),
                "operator_id": ctx["key_to_id"].get(operator_key)
                if operator_key else None,
            }
            if not params["key"]:
                config_stats.error("config row without key")
                continue
            conn.execute(
                "INSERT INTO app_config (key, value, updated_at, operator_id) "
                "VALUES (%(key)s, %(value)s, %(updated_at)s, %(operator_id)s) "
                "ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, "
                "updated_at = EXCLUDED.updated_at, operator_id = EXCLUDED.operator_id",
                params,
            )
            config_stats.inserted += 1
    result["app_config"] = config_stats.as_dict()

    # Reconcile what actually landed in the database.
    if ctx["key_to_id"]:
        user_ids = [uid for uid in ctx["key_to_id"].values() if uid and uid > 0]
        if user_ids:
            rows = conn.execute(
                "SELECT user_id, SUM(delta) FROM points_ledger "
                "WHERE user_id = ANY(%s) GROUP BY user_id",
                (user_ids,),
            ).fetchall()
            id_to_key = {uid: key for key, uid in ctx["key_to_id"].items() if uid}
            result["db_balances"] = sorted(
                [
                    {"legacy_user_id": id_to_key.get(row[0], str(row[0])),
                     "sum_delta": int(row[1] or 0)}
                    for row in rows
                ],
                key=lambda item: item["legacy_user_id"],
            )
    return result


# ---------------------------------------------------------------------------
# Photos / R2
# ---------------------------------------------------------------------------


def index_photo_files(photos_dir: Path, tokens: set[str]) -> dict[str, Path]:
    if not photos_dir.is_dir():
        return {}
    preferred = {".jpg": 0, ".jpeg": 1, ".png": 2}
    found: dict[str, Path] = {}
    for path in photos_dir.rglob("*"):
        if not path.is_file():
            continue
        token = path.stem
        if token not in tokens:
            continue
        previous = found.get(token)
        if previous is None or preferred.get(path.suffix.lower(), 9) < preferred.get(
            previous.suffix.lower(), 9
        ):
            found[token] = path
    return found


def process_photo_bytes(path: Path) -> dict[str, Any]:
    try:
        from PIL import Image, ImageOps
    except ImportError as exc:  # pragma: no cover
        raise MigrationError(
            "Pillow is required for photo import: "
            "python3 -m pip install -r tools/requirements.txt"
        ) from exc
    import io

    data = path.read_bytes()
    if not data:
        raise ValueError("empty file")
    if len(data) > MAX_PHOTO_BYTES:
        raise ValueError(f"file exceeds {MAX_PHOTO_BYTES} bytes")
    try:
        with Image.open(io.BytesIO(data)) as probe:
            fmt = (probe.format or "").upper()
            width, height = probe.size
            if fmt not in ("JPEG", "PNG"):
                raise ValueError(f"unsupported image format {fmt or 'unknown'}")
            if width > MAX_PHOTO_DIM or height > MAX_PHOTO_DIM:
                raise ValueError(f"image dimensions {width}x{height} exceed limit")
            if width * height > MAX_PHOTO_PIXELS:
                raise ValueError(f"image pixels {width * height} exceed limit")
            probe.load()
            source = probe.copy()
    except ValueError:
        raise
    except Exception as exc:  # noqa: BLE001 - Pillow raises many types
        raise ValueError(f"cannot decode image: {exc}") from exc

    # Apply EXIF/eXIf orientation before re-encoding so stored dimensions and
    # pixels match what browsers showed for the legacy original.
    try:
        source = ImageOps.exif_transpose(source)
    except Exception:  # noqa: BLE001 - malformed metadata must not block import
        pass
    for key in ("exif", "icc_profile", "xmp", "XML:com.adobe.xmp", "pnginfo", "comment"):
        source.info.pop(key, None)

    width, height = source.size
    if width > MAX_PHOTO_DIM or height > MAX_PHOTO_DIM:
        raise ValueError(f"oriented dimensions {width}x{height} exceed limit")
    if width * height > MAX_PHOTO_PIXELS:
        raise ValueError(f"oriented pixels {width * height} exceed limit")

    # Re-encode so the uploaded object cannot carry EXIF/GPS/XMP/text chunks.
    # This means R2 bytes differ from the source file; reports use these bytes.
    original_buffer = io.BytesIO()
    if fmt == "JPEG":
        original = source if source.mode == "RGB" else source.convert("RGB")
        original.save(original_buffer, format="JPEG", quality=90)
        content_type = "image/jpeg"
    else:
        source.save(original_buffer, format="PNG")
        content_type = "image/png"
    original_bytes = original_buffer.getvalue()
    if len(original_bytes) > MAX_PHOTO_BYTES:
        raise ValueError(f"re-encoded file exceeds {MAX_PHOTO_BYTES} bytes")

    # Thumbnail: flatten alpha onto white, matching the server's JPEG thumb.
    if source.mode not in ("RGB", "L"):
        rgba = source.convert("RGBA")
        background = Image.new("RGBA", rgba.size, (255, 255, 255, 255))
        flat = Image.alpha_composite(background, rgba).convert("RGB")
    else:
        flat = source.convert("RGB")

    factor = min(1.0, THUMB_SIZE / float(max(flat.size)))
    thumb_size = (
        max(1, int(flat.size[0] * factor + 0.5)),
        max(1, int(flat.size[1] * factor + 0.5)),
    )
    try:
        from PIL import Image as PilImage
        thumb = flat.resize(thumb_size, PilImage.Resampling.LANCZOS)
    except AttributeError:  # pragma: no cover - Pillow < 9
        thumb = flat.resize(thumb_size, Image.LANCZOS)
    buffer = io.BytesIO()
    thumb.save(buffer, format="JPEG", quality=82)
    return {
        "data": original_bytes,
        "content_type": content_type,
        "width": width,
        "height": height,
        "thumb": buffer.getvalue(),
    }


class R2Store:
    def __init__(self) -> None:
        try:
            import boto3
        except ImportError as exc:  # pragma: no cover
            raise MigrationError(
                "boto3 is required for photo import: "
                "python3 -m pip install -r tools/requirements.txt"
            ) from exc
        endpoint = os.environ.get("R2_ENDPOINT", "").strip()
        bucket = os.environ.get("R2_BUCKET", "").strip()
        access = os.environ.get("R2_ACCESS_KEY_ID", "").strip()
        secret = os.environ.get("R2_SECRET_ACCESS_KEY", "").strip()
        if not endpoint or not bucket or not access or not secret:
            raise MigrationError(
                "photos are present but R2 config is incomplete "
                "(R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY)"
            )
        if "://" not in endpoint:
            endpoint = "https://" + endpoint
        self.bucket = bucket
        self.client = boto3.client(
            "s3",
            endpoint_url=endpoint,
            region_name=os.environ.get("R2_REGION", "auto") or "auto",
            aws_access_key_id=access,
            aws_secret_access_key=secret,
        )

    def put(self, key: str, data: bytes, content_type: str) -> None:
        self.client.put_object(
            Bucket=self.bucket,
            Key=key,
            Body=data,
            ContentType=content_type,
            CacheControl="private, max-age=0, no-store",
        )


def sanitize_filename(name: str, fallback: str) -> str:
    base = os.path.basename(name or "").replace("\x00", "")
    base = re.sub(r"[\\/]+", "_", base).strip()
    return (base or fallback)[:200]


def process_media(
    conn: Any,
    photos_dir: Path | None,
    users: list[dict[str, Any]],
    existing: dict[str, Any],
    apply: bool,
    files: dict[str, Path] | None = None,
    store: R2Store | None = None,
) -> dict[str, Any]:
    photo_refs = [
        (record, attachment)
        for record in users
        for attachment in record["photos"]
    ]
    tokens = {
        str(attachment.get("file_token"))
        for _record, attachment in photo_refs
        if attachment.get("file_token")
    }
    if files is None:
        files = index_photo_files(photos_dir, tokens) if photos_dir else {}
    stats = {
        "source_refs": len(photo_refs),
        "distinct_tokens": len(tokens),
        "files_found": len(files),
        "media_reused": 0,
        "uploaded": 0,
        "links_inserted": 0,
        "links_existing": 0,
        "missing": [],
        "missing_count": 0,
        "missing_distinct_tokens": 0,
        "errors": [],
        "error_count": 0,
        "notes": [],
    }
    if files:
        stats["notes"].append(
            "photos are re-encoded to strip EXIF/GPS/XMP; byte_size may differ "
            "from the source files"
        )
    missing_tokens: set[str] = set()
    if photos_dir is None:
        stats["notes"].append("no --photos-dir supplied; all photo files are missing")
    elif not photos_dir.is_dir():
        stats["notes"].append(f"photos dir does not exist: {photos_dir}")

    if apply and files and store is None:
        store = R2Store()

    for record, attachment in photo_refs:
        token = str(attachment.get("file_token") or "")
        user_id = None
        if apply:
            user_id = None
            if record.get("_db_id"):
                user_id = record["_db_id"]
            else:
                user_id = None
        existing_media = existing["media_by_token"].get(token) if token else None
        path = files.get(token) if token else None
        if not token:
            stats["error_count"] += 1
            stats["errors"].append(
                {"legacy_user_id": record["key"], "message": "attachment without file_token"}
            )
            continue
        if existing_media:
            stats["media_reused"] += 1
        elif path is None:
            stats["missing_count"] += 1
            missing_tokens.add(token)
            if len(stats["missing"]) < 5000:
                stats["missing"].append(
                    {
                        "legacy_user_id": record["key"],
                        "file_token": token,
                        "name": clean_text(attachment.get("name")),
                    }
                )
            continue
        if not apply:
            continue

        if user_id is None and record.get("_db_id"):
            user_id = record["_db_id"]
        if not user_id:
            stats["error_count"] += 1
            stats["errors"].append(
                {"legacy_user_id": record["key"], "message": "user was not imported"}
            )
            continue
        try:
            if existing_media:
                # Bitable allows one file_token to be reused by many records
                # (the load-test users share one photo). The media row has a
                # single owner, but profile_photos may reference it for every
                # user that legitimately lists the same token.
                media_id = existing_media["id"]
            else:
                processed = process_photo_bytes(path)
                media_id = str(uuid.uuid4())
                ext = "jpg" if processed["content_type"] == "image/jpeg" else "png"
                object_key = f"media/{user_id}/{media_id}.{ext}"
                thumb_key = f"media/{user_id}/{media_id}_thumb.jpg"
                assert store is not None
                with conn.transaction():
                    store.put(object_key, processed["data"], processed["content_type"])
                    store.put(thumb_key, processed["thumb"], "image/jpeg")
                    conn.execute(
                        "INSERT INTO media (id, user_id, original_name, object_key, "
                        "thumb_key, content_type, byte_size, thumb_size, width, "
                        "height, kind, legacy_file_token) VALUES "
                        "(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, 'profile', %s)",
                        (
                            media_id,
                            user_id,
                            sanitize_filename(
                                clean_text(attachment.get("name")),
                                f"{token}.{ext}",
                            ),
                            object_key,
                            thumb_key,
                            processed["content_type"],
                            len(processed["data"]),
                            len(processed["thumb"]),
                            processed["width"],
                            processed["height"],
                            token,
                        ),
                    )
                stats["uploaded"] += 1
                existing["media_by_token"][token] = {
                    "id": media_id, "user_id": user_id
                }
            linked = existing["profile_links"].setdefault(user_id, set())
            if media_id in linked:
                stats["links_existing"] += 1
                continue
            next_order = existing["profile_max_order"].get(user_id, -1) + 1
            is_cover = user_id not in existing["profile_has_cover"]
            with conn.transaction():
                conn.execute(
                    "INSERT INTO profile_photos (user_id, media_id, sort_order, "
                    "is_cover) VALUES (%s, %s, %s, %s) "
                    "ON CONFLICT (user_id, media_id) DO NOTHING",
                    (user_id, media_id, next_order, is_cover),
                )
            linked.add(media_id)
            existing["profile_max_order"][user_id] = next_order
            if is_cover:
                existing["profile_has_cover"].add(user_id)
            stats["links_inserted"] += 1
        except Exception as exc:  # noqa: BLE001
            stats["error_count"] += 1
            if len(stats["errors"]) < 100:
                stats["errors"].append(
                    {
                        "legacy_user_id": record["key"],
                        "file_token": token,
                        "message": safe_error(exc),
                    }
                )
    stats["missing_distinct_tokens"] = len(missing_tokens)
    if not apply:
        stats["uploaded"] = len(
            {
                token
                for token in files
                if token not in existing["media_by_token"]
            }
        )
    return stats


# ---------------------------------------------------------------------------
# Main pipeline
# ---------------------------------------------------------------------------


def build_user_records(tables: dict[str, list[dict[str, Any]]]) -> tuple[
    list[dict[str, Any]], list[dict[str, Any]], list[str]
]:
    user_records = [
        build_user_record(row.get("fields", {}), row["record_id"], key, "user")
        for row in tables["users"]
        for key in [clean_text(row.get("fields", {}).get("用户ID")).strip()]
        if key
    ]
    observer_records = [
        build_user_record(row.get("fields", {}), row["record_id"], key, "observer")
        for row in tables["observers"]
        for key in [clean_text(row.get("fields", {}).get("编号")).strip()]
        if key
    ]
    user_openids = {r["legacy_open_id"] for r in user_records if r["legacy_open_id"]}
    observer_openids = {r["legacy_open_id"] for r in observer_records if r["legacy_open_id"]}
    for record in user_records:
        if record["legacy_open_id"] in observer_openids:
            record["observer_enabled"] = True
    # A v1 observer row sharing a member open_id is the same person in two
    # roles; keep one users row and mark the observer capability on it.
    observer_records = [
        r for r in observer_records
        if not (r["legacy_open_id"] and r["legacy_open_id"] in user_openids)
    ]
    warnings: list[str] = []
    missing_user_ids = len(tables["users"]) - len(user_records)
    if missing_user_ids:
        warnings.append(f"{missing_user_ids} user rows have no 用户ID and were skipped")
    missing_observer_ids = len(tables["observers"]) - len(observer_records)
    if missing_observer_ids:
        warnings.append(
            f"{missing_observer_ids} observer rows have no 编号 and were skipped"
        )
    return user_records, observer_records, warnings


def activity_maps(
    records: list[dict[str, Any]],
) -> tuple[dict[str, str], dict[str, str]]:
    by_no = {r["activity_no"]: r["key"] for r in records}
    by_record = {r["legacy_record_id"]: r["key"] for r in records}
    return by_no, by_record


def run(args: argparse.Namespace) -> tuple[dict[str, Any], int]:
    export_dir = Path(args.export_dir)
    export = load_export(export_dir)
    tables = export["tables"]
    apply = args.apply
    mode = "apply" if apply else "dry-run"

    report: dict[str, Any] = {
        "generated_at": NOW().isoformat(),
        "mode": mode,
        "source": {
            "export_dir": str(export_dir),
            "manifest": export["manifest"],
            "file_counts": {name: len(rows) for name, rows in tables.items()},
        },
        "schema": {},
        "tables": {},
        "phones": {},
        "photos": {},
        "points": {},
        "warnings": [],
        "fatal": [],
    }
    manifest_tables = (export["manifest"] or {}).get("tables") or {}
    for name, rows in tables.items():
        expected = manifest_tables.get(name, {}).get("count")
        if expected is not None and expected != len(rows):
            report["warnings"].append(
                f"{name}: manifest count {expected} != file count {len(rows)}"
            )

    user_records, observer_records, build_warnings = build_user_records(tables)
    report["warnings"].extend(build_warnings)
    id_present = sum(
        1 for record in user_records + observer_records if record["id_card_present"]
    )
    id_valid = sum(
        1 for record in user_records + observer_records if record["id_card_valid"]
    )
    report["identity_cards"] = {
        "present": id_present,
        "valid": id_valid,
        "ciphertext_migrated": 0,
    }
    if id_present:
        report["warnings"].append(
            "identity card ciphertext was not migrated (no encryption key in this tool); "
            "only last4 + valid were stored"
        )
    openid_to_key, uid_to_key, map_warnings = build_user_maps(
        user_records, observer_records
    )
    report["warnings"].extend(map_warnings)

    photos_dir = Path(args.photos_dir) if args.photos_dir else None
    photo_tokens = {
        str(attachment.get("file_token"))
        for record in user_records + observer_records
        for attachment in record["photos"]
        if attachment.get("file_token")
    }
    photo_files = index_photo_files(photos_dir, photo_tokens) if photos_dir else {}
    photo_store = None
    if apply and photo_files:
        try:
            photo_store = R2Store()
        except MigrationError as exc:
            report["fatal"].append(str(exc))
            return report, 1

    conn = connect_db(args.database_url, dry_run=not apply)
    try:
        schema = schema_report(conn)
        report["schema"] = schema
        if schema["missing_columns"] or 2 not in schema["migrations"]:
            report["fatal"].append(
                "database schema is missing required columns or migration 0002; "
                "no changes were made"
            )
            return report, 1

        existing = load_existing(conn)
        existing_by_key: dict[str, dict[str, Any]] = {}
        for record in user_records + observer_records:
            row = (
                existing["users_by_record"].get(record["legacy_record_id"])
                or existing["users_by_legacy"].get(record["legacy_user_id"])
            )
            if row:
                existing_by_key[record["key"]] = row
        assignments, phone_errors = plan_phone_assignments(
            user_records + observer_records, existing_by_key
        )
        if phone_errors:
            report["fatal"].extend(phone_errors)
            return report, 1

        report["phones"] = {
            "total": len(assignments),
            "kept": sum(1 for a in assignments.values() if a["reason"] == "kept"),
            "reserved_u0006": sum(
                1 for a in assignments.values() if a["reason"] == "reserved_u0006"
            ),
            "allocated": sum(
                1 for a in assignments.values()
                if a["reason"] in ("missing", "invalid", "duplicate")
            ),
            "kept_existing_test_number": sum(
                1 for a in assignments.values()
                if a["reason"] == "kept_existing_test_number"
            ),
            "assignments": [
                {
                    "legacy_user_id": key,
                    "reason": value["reason"],
                    "source_phone": value["source_masked"],
                    "assigned_phone": value["assigned_masked"],
                }
                for key, value in sorted(
                    assignments.items(),
                    key=lambda item: (
                        0 if item[0].startswith("U-") else 1,
                        int(re.sub(r"\D", "", item[0]) or 0),
                    ),
                )
            ],
            "assignments_truncated": False,
        }
        if len(report["phones"]["assignments"]) > 5000:
            report["phones"]["assignments"] = report["phones"]["assignments"][:5000]
            report["phones"]["assignments_truncated"] = True

        try:
            user_stats, observer_stats, key_to_id = process_users(
                conn,
                user_records,
                observer_records,
                assignments,
                existing,
                apply,
            )
        except MigrationError as exc:
            report["fatal"].append(str(exc))
            return report, 1
        report["tables"]["users"] = user_stats.as_dict()
        report["tables"]["observers"] = observer_stats.as_dict()
        if apply:
            for record in user_records + observer_records:
                record["_db_id"] = key_to_id.get(record["key"])

        activity_records = [build_activity_record(row) for row in tables["activities"]]
        activity_stats, activity_key_to_id, activity_record_to_id = process_activities(
            conn,
            tables["activities"],
            legacy_map(conn, "activities", "legacy_record_id"),
            apply,
        )
        report["tables"]["activities"] = activity_stats.as_dict()
        activity_no_to_key, activity_rec_to_key = activity_maps(activity_records)
        if not apply:
            activity_key_to_id = {key: -1 for key in activity_no_to_key.values()}

        ctx = {
            "openid_to_key": openid_to_key,
            "uid_to_key": uid_to_key,
            "key_to_id": key_to_id,
            "activity_no_to_key": activity_no_to_key,
            "activity_key_to_id": activity_key_to_id,
        }

        report["tables"]["likes"] = process_likes(
            conn, tables["likes"], ctx,
            legacy_map(conn, "likes", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["praises"] = process_praises(
            conn, tables["praise"], ctx,
            legacy_map(conn, "praises", "legacy_record_id"), apply,
        ).as_dict()
        message_stats, _ = process_messages(
            conn, tables["messages"], ctx,
            legacy_map(conn, "messages", "legacy_record_id"), apply,
        )
        report["tables"]["messages"] = message_stats.as_dict()
        report["tables"]["reports"] = process_reports(
            conn, tables["reports"], ctx,
            legacy_map(conn, "reports", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["suggestions"] = process_suggestions(
            conn, tables["suggestions"], ctx,
            legacy_map(conn, "suggestions", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["signups"] = process_signups(
            conn, tables["signups"], ctx,
            legacy_map(conn, "signups", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["group_selections"] = process_group_selections(
            conn, tables["group_select"], ctx,
            legacy_map(conn, "group_selections", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["group_results"] = process_group_results(
            conn, tables["group_result"], ctx,
            legacy_map(conn, "group_results", "legacy_record_id"), apply,
        ).as_dict()
        report["tables"]["onetoone_results"] = process_onetoone_results(
            conn, tables["onetoone_result"], ctx,
            legacy_map(conn, "onetoone_results", "legacy_record_id"), apply,
        ).as_dict()

        report["points"] = process_points(conn, export["points_db"], ctx, apply)

        photo_stats = process_media(
            conn,
            photos_dir,
            user_records + observer_records,
            existing,
            apply,
            files=photo_files,
            store=photo_store,
        )
        report["photos"] = photo_stats
        duplicate_sources = Counter(
            attachment.get("file_token")
            for record in user_records + observer_records
            for attachment in record["photos"]
            if attachment.get("file_token")
        )
        report["photos"]["tokens_reused_across_users"] = sorted(
            token for token, count in duplicate_sources.items() if count > 1
        )[:50]
        if apply:
            report["database_counts"] = {
                table: conn.execute(f"SELECT count(*) FROM {table}").fetchone()[0]
                for table in (
                    "users", "media", "profile_photos", "activities", "likes",
                    "praises", "messages", "reports", "suggestions", "signups",
                    "group_selections", "group_results", "onetoone_results",
                    "points_ledger", "points_redemptions", "invites",
                )
            }
    finally:
        conn.close()

    for section in ("users", "observers", "activities", "likes", "praises",
                    "messages", "reports", "suggestions", "signups",
                    "group_selections", "group_results", "onetoone_results"):
        stats = report["tables"].get(section)
        if stats and stats.get("error_count"):
            report["warnings"].append(
                f"{section}: {stats['error_count']} row errors (see table report)"
            )
    if report["points"].get("reconciliation", {}).get("unresolved_user_oids"):
        report["warnings"].append(
            "points: some ledger user_oid values do not resolve to an imported user"
        )
    return report, 0


def write_report(report: dict[str, Any], path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as handle:
        json.dump(report, handle, ensure_ascii=False, indent=2, default=str)
        handle.write("\n")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def summary_line(report: dict[str, Any], report_path: Path) -> str:
    tables = report.get("tables", {})
    users = tables.get("users", {})
    observers = tables.get("observers", {})
    photos = report.get("photos", {})
    balances = report.get("points", {}).get("reconciliation", {}).get("balances", [])
    balance_text = ", ".join(
        f"{item['legacy_user_id']}={item['sum_delta']}" for item in balances
    ) or "none"
    return (
        f"mode={report.get('mode')} users={users.get('source', 0)} "
        f"inserted={users.get('inserted', 0)} updated={users.get('updated', 0)} "
        f"observers={observers.get('source', 0)} "
        f"photos_found={photos.get('files_found', 0)} "
        f"photos_missing={len(photos.get('missing', []))} "
        f"points_balance=[{balance_text}] report={report_path}"
    )


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Import the testapp export into yixianqian v2 PostgreSQL."
    )
    parser.add_argument("--export-dir", required=True, help="directory with exported JSON + points.db")
    parser.add_argument(
        "--database-url",
        default=os.environ.get("DATABASE_URL", ""),
        help="PostgreSQL URL (defaults to DATABASE_URL)",
    )
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--dry-run", dest="apply", action="store_false", help="read-only planning")
    mode.add_argument("--apply", dest="apply", action="store_true", help="write to PostgreSQL/R2")
    parser.set_defaults(apply=False)
    parser.add_argument("--photos-dir", default="", help="directory of <file_token>.<ext> photos")
    parser.add_argument(
        "--report-out",
        default="",
        help="report JSON path (default: migration-report-<mode>.json)",
    )
    args = parser.parse_args(argv)
    if not args.database_url:
        parser.error("--database-url or DATABASE_URL is required")
    if not args.report_out:
        args.report_out = f"migration-report-{'apply' if args.apply else 'dry-run'}.json"
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv if argv is not None else sys.argv[1:])
    try:
        report, code = run(args)
    except MigrationError as exc:
        print(f"migrate_testapp: {safe_error(exc)}", file=sys.stderr)
        return 2
    except Exception as exc:  # noqa: BLE001 - never leak raw connection strings
        print(f"migrate_testapp: unexpected error: {safe_error(exc)}", file=sys.stderr)
        return 2

    report_path = Path(args.report_out)
    try:
        write_report(report, report_path)
    except OSError as exc:
        print(f"migrate_testapp: cannot write report: {safe_error(exc)}", file=sys.stderr)
        return 2

    if report.get("fatal"):
        for message in report["fatal"]:
            print(f"migrate_testapp: FATAL: {message}", file=sys.stderr)
        print(f"report={report_path}", file=sys.stderr)
        return 1
    print(summary_line(report, report_path))
    return code


if __name__ == "__main__":
    raise SystemExit(main())
