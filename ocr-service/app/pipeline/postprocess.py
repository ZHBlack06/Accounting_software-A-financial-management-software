"""后处理（ARCHITECTURE §8.3）：

① 抽取第一个完整 JSON（括号配对扫描，容忍 ```json 包裹）
② 脏输出修复：代码块包裹 / 尾逗号 / 单引号 / 缺失键引号
③ 字段规整：金额 → 元浮点 + 整数分；时间 → 'YYYY-MM-DD HH:mm:ss'；
   收支 → expense/income；缺失字段填 null（不猜，交由 need_review）
④ id 缺失时按 19 位规则生成：yyyyMMddHHmmss(14) + "0000" + 校验码
   （前 18 位数字逐位求和 mod 11，10 → 'X'，与后端 ids 包一致）
"""

from __future__ import annotations

import re
from datetime import datetime
from typing import Any

CODE_FENCE_RE = re.compile(r"```(?:json|JSON)?\s*(.*?)\s*```", re.DOTALL)
TRAILING_COMMA_RE = re.compile(r",\s*([}\]])")
SINGLE_QUOTE_STR_RE = re.compile(r"'([^'\n]*)'")
UNQUOTED_KEY_RE = re.compile(r"([{,]\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*:)")

CHINESE_TYPE_MAP: list[tuple[tuple[str, ...], str]] = [
    (("支出", "付款", "消费", "花销", "买", "转出"), "expense"),
    (("收入", "收款", "入账", "红包", "退款", "转入"), "income"),
]

TIMESTAMP_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"(\d{4})[-/年.](\d{1,2})[-/月.](\d{1,2})[日T\s]+(\d{1,2}):(\d{1,2})(?::(\d{1,2}))?"),
    re.compile(r"(\d{4})[-/年.](\d{1,2})[-/月.](\d{1,2})[日T\s]*(\d{1,2})时(\d{1,2})分(?:(\d{1,2})秒)?"),
    re.compile(r"^(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(\d{2})$"),  # 14 位
    re.compile(r"(\d{4})[-/年.](\d{1,2})[-/月.](\d{1,2})[日]?"),
]


# ---------------------------------------------------------------------------
# ① JSON 抽取与修复
# ---------------------------------------------------------------------------

def strip_code_fence(text: str) -> str:
    """去掉 ```json ... ``` 代码块包裹；无围栏则原样返回。"""
    match = CODE_FENCE_RE.search(text)
    if match:
        return match.group(1).strip()
    return text.strip()


def remove_trailing_commas(text: str) -> str:
    """去掉对象/数组闭合前的尾逗号：{"a":1,} → {"a":1}。"""
    previous = None
    current = text
    while previous != current:
        previous = current
        current = TRAILING_COMMA_RE.sub(r"\1", current)
    return current


def fix_single_quotes(text: str) -> str:
    """把单引号字符串替换为双引号（仅在整体不是合法 JSON 前提下调用）。"""
    return SINGLE_QUOTE_STR_RE.sub(r'"\1"', text)


def fix_unquoted_keys(text: str) -> str:
    """给裸键名补双引号：{amount: 1} → {"amount": 1}。"""
    return UNQUOTED_KEY_RE.sub(r'\1"\2"\3', text)


def extract_first_json(text: str) -> str | None:
    """括号配对扫描，返回文本中第一个完整 JSON 对象的原文片段。

    扫描是字符串感知的（跳过 "..." 与 '...' 内部字符）。
    """
    source = strip_code_fence(text)
    start = -1
    depth = 0
    in_string = False
    quote_char = ""
    escaped = False
    for index, char in enumerate(source):
        if in_string:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote_char:
                in_string = False
            continue
        if char in ('"', "'"):
            if depth > 0:
                in_string = True
                quote_char = char
            continue
        if char == "{":
            if depth == 0:
                start = index
            depth += 1
        elif char == "}":
            if depth > 0:
                depth -= 1
                if depth == 0 and start >= 0:
                    return source[start : index + 1]
    return None


def parse_model_json(text: str) -> dict[str, Any]:
    """从模型原始输出解析 JSON dict，依次尝试原样/去围栏/三步修复/首对象扫描。

    Raises:
        ValueError: 全部尝试失败。
    """
    candidates: list[str] = []
    stripped = strip_code_fence(text)
    candidates.append(stripped)
    repaired = remove_trailing_commas(stripped)
    candidates.append(repaired)
    candidates.append(fix_unquoted_keys(repaired))
    candidates.append(fix_single_quotes(fix_unquoted_keys(repaired)))
    first_obj = extract_first_json(text)
    if first_obj is not None:
        candidates.append(first_obj)
        candidates.append(remove_trailing_commas(first_obj))
        candidates.append(fix_unquoted_keys(remove_trailing_commas(first_obj)))
        candidates.append(fix_single_quotes(fix_unquoted_keys(remove_trailing_commas(first_obj))))
    for candidate in candidates:
        try:
            data = _try_loads(candidate)
            if isinstance(data, dict):
                return data
        except ValueError:
            continue
    raise ValueError(f"无法从模型输出解析 JSON（前 200 字符：{text[:200]!r}）")


def _try_loads(candidate: str) -> Any:
    import json

    return json.loads(candidate)


# ---------------------------------------------------------------------------
# ② 字段规整
# ---------------------------------------------------------------------------

def normalize_amount(value: Any) -> tuple[float | None, int | None]:
    """金额 → (元浮点, 整数分)。非法/缺失返回 (None, None)。"""
    if value is None:
        return None, None
    if isinstance(value, bool):
        return None, None
    if isinstance(value, (int, float)):
        yuan = float(value)
    else:
        text = str(value).strip().replace(",", "").replace("¥", "").replace("￥", "").replace("元", "")
        if not text or text in {"--", "-", "null", "None"}:
            return None, None
        try:
            yuan = float(text)
        except ValueError:
            return None, None
    if yuan != yuan or yuan in (float("inf"), float("-inf")):  # NaN / Inf
        return None, None
    cents = round(yuan * 100)
    return yuan, int(cents)


def normalize_timestamp(value: Any) -> str | None:
    """时间 → 'YYYY-MM-DD HH:mm:ss'；支持常见变体与 14 位数字。"""
    if value is None:
        return None
    text = str(value).strip()
    if not text or text.lower() in {"null", "none"}:
        return None
    digits = re.sub(r"\D", "", text)
    for pattern in (TIMESTAMP_PATTERNS[0], TIMESTAMP_PATTERNS[1]):
        match = pattern.search(text)
        if match:
            year, month, day, hour, minute, second = match.groups(default="0")
            candidate = _build_timestamp(int(year), int(month), int(day), int(hour), int(minute), int(second or 0))
            if candidate:
                return candidate
    if re.fullmatch(r"\d{14}", digits):
        candidate = _build_timestamp(
            int(digits[0:4]), int(digits[4:6]), int(digits[6:8]),
            int(digits[8:10]), int(digits[10:12]), int(digits[12:14]),
        )
        if candidate:
            return candidate
    match = TIMESTAMP_PATTERNS[3].search(text)
    if match:
        year, month, day = match.groups()
        candidate = _build_timestamp(int(year), int(month), int(day), 0, 0, 0)
        if candidate:
            return candidate
    return None


def _build_timestamp(year: int, month: int, day: int, hour: int, minute: int, second: int) -> str | None:
    try:
        return datetime(year, month, day, hour, minute, second).strftime("%Y-%m-%d %H:%M:%S")
    except ValueError:
        return None


def normalize_transaction_type(value: Any) -> str | None:
    """收支方向 → expense / income；中文/英文/大小写均可。"""
    if value is None:
        return None
    text = str(value).strip().lower()
    if not text or text in {"null", "none"}:
        return None
    if text in {"expense", "expenditure", "pay", "payment"}:
        return "expense"
    if text in {"income", "receive", "refund"}:
        return "income"
    for keywords, direction in CHINESE_TYPE_MAP:
        if any(keyword in text for keyword in keywords):
            return direction
    return None


def normalize_tags(value: Any) -> list[str]:
    """标签 → 去重字符串数组。"""
    if value is None:
        return []
    if isinstance(value, str):
        parts = re.split(r"[,，、;；\s]+", value.strip())
        return [p for p in parts if p]
    if isinstance(value, (list, tuple)):
        return [str(item).strip() for item in value if str(item).strip()]
    return []


def normalize_confidence(value: Any) -> dict[str, float]:
    """confidence → 固定四键，缺省 0.3，范围钳制 [0,1]。"""
    result = {"amount": 0.3, "timestamp": 0.3, "merchant": 0.3, "category": 0.3}
    if isinstance(value, dict):
        for key in result:
            raw = value.get(key)
            if raw is None:
                # 兼容 PRD 7.3 里 timestamp 键名的同义词
                raw = value.get("transaction_time") if key == "timestamp" else None
            if raw is None:
                continue
            try:
                score = float(raw)
            except (TypeError, ValueError):
                continue
            result[key] = max(0.0, min(1.0, score))
    return result


def _clean_optional_str(value: Any) -> str | None:
    if value is None:
        return None
    text = str(value).strip()
    if not text or text.lower() in {"null", "none", "n/a", "未识别", "无"}:
        return None
    return text


def compute_id(timestamp: str | None, fallback_seed: str = "19700101000000") -> str:
    """按 19 位规则生成 id：yyyyMMddHHmmss(14) + "0000" + 校验码。

    校验码：前 18 位数字逐位求和 s，r = s % 11；r == 10 → 'X'。
    """
    compact = (timestamp or fallback_seed).replace("-", "").replace(":", "").replace(" ", "").replace("/", "")
    compact = re.sub(r"\D", "", compact)
    if len(compact) < 14:
        compact = (compact + "0" * 14)[:14]
    body = compact[:14] + "0000"
    total = sum(int(char) for char in body)
    remainder = total % 11
    check = "X" if remainder == 10 else str(remainder)
    return body + check


def normalize_result(
    engine_output: dict[str, Any],
    extra_warnings: list[str] | None = None,
) -> dict[str, Any]:
    """把引擎原始输出规整为 PRD 7.3 + §5.8 双契约字段。

    引擎输出约定：
    - mock 引擎：字段已规整；
    - 真实模型引擎：{"raw_text": 模型原始文本, "model_used": ..., "payload": 模型文本}
      payload 由本函数解析 JSON。
    """
    warnings = list(extra_warnings or [])
    model_used = str(engine_output.get("model_used") or "unknown")
    # 引擎自带 warnings（如 mock 的降级说明）并入最终结果
    engine_warnings = engine_output.get("warnings")
    if isinstance(engine_warnings, (list, tuple)):
        warnings.extend(str(item) for item in engine_warnings if item)

    # 真实模型：解析 payload 里的 JSON
    data: dict[str, Any]
    raw_text = str(engine_output.get("raw_text") or "")
    payload = engine_output.get("payload")
    if isinstance(payload, str) and payload.strip():
        try:
            data = parse_model_json(payload)
        except ValueError as exc:
            warnings.append(f"模型输出 JSON 解析失败，字段置空待人工录入：{exc}")
            data = {}
    else:
        data = engine_output

    amount_yuan, amount_cents = normalize_amount(data.get("amount"))
    timestamp = normalize_timestamp(data.get("timestamp") or data.get("transaction_time"))
    transaction_type = normalize_transaction_type(data.get("transaction_type"))
    tags = normalize_tags(data.get("tags"))
    confidence = normalize_confidence(data.get("confidence"))

    # 缺失字段填 null，不猜
    missing_fields: list[str] = []
    if timestamp is None:
        missing_fields.append("timestamp")
    if amount_cents is None:
        missing_fields.append("amount")
    if transaction_type is None:
        missing_fields.append("transaction_type")
    if missing_fields:
        warnings.append(f"字段缺失或无法解析：{', '.join(missing_fields)}（已置空，需人工确认）")

    # 真实模型的低置信度字段提示（mock 恒为 0.3，已由降级 warning 覆盖）
    if model_used != "mock":
        low_confidence = [key for key, score in confidence.items() if score < 0.6]
        if low_confidence:
            warnings.append(f"低置信度字段：{', '.join(low_confidence)}（需人工确认）")

    result_id = _clean_optional_str(data.get("id")) or compute_id(timestamp)
    need_review = bool(engine_output.get("need_review")) or bool(warnings) or bool(missing_fields)

    return {
        # ---- PRD 7.3 字段（名称完全一致） ----
        "id": result_id,
        "source": _clean_optional_str(data.get("source")) or "pc_local_screenshot",
        "timestamp": timestamp,
        "account": _clean_optional_str(data.get("account")),
        "transaction_type": transaction_type,
        "amount": amount_yuan,
        "category": _clean_optional_str(data.get("category")),
        "sub_category": _clean_optional_str(data.get("sub_category")),
        "merchant": _clean_optional_str(data.get("merchant")),
        "region": _clean_optional_str(data.get("region")),
        "tags": tags,
        "remark": _clean_optional_str(data.get("remark")),
        "confidence": confidence,
        "raw_text": raw_text,
        "model_used": model_used,
        "need_review": need_review,
        # ---- ARCHITECTURE §5.8 后端契约字段 ----
        "transaction_time": timestamp,
        "amount_cents": amount_cents,
        "account_id": None,   # 由 Go 端对照账户表回填
        "category_id": None,  # 由 Go 端对照分类表回填
        "merchant_id": None,
        "warnings": warnings,
    }
