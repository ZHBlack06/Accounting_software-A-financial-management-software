"""纯文本规则校验（PRD 7.6 子集，ARCHITECTURE §8.3 边界）。

Python 侧只做不依赖数据库的纯文本规则：
  ① 金额格式：正数、最多两位小数、上限合理；
  ② 时间格式：'YYYY-MM-DD HH:mm:ss' ⇔ 14 位数字且为合法日期；
  ③ 收支方向：transaction_type 必须为 expense/income，且与 raw_text
     中的收支关键词一致；
  ④ 汇总行过滤：raw_text 含"合计/总计/汇总/共N笔"时提示疑似汇总行；
  ⑤ 重复行去重：raw_text 中多行含相同金额+时间时提示疑似重复行。

需要数据库的三条（账户存在性 / 分类合法性 / 重复检测入库 / 商户平台 /
冻结边界）由 Go 端在入账前校验，Python 不连数据库（见 ARCHITECTURE §8.3 注）。

任一规则不通过 → need_review=True + warnings。
"""

from __future__ import annotations

import re
from datetime import datetime
from typing import Any

AMOUNT_UPPER_BOUND = 1_000_000_000.0  # 单笔金额合理性上限（元）
AMOUNT_LOWER_BOUND = 0.0

SUMMARY_LINE_KEYWORDS = ("合计", "总计", "汇总", "共", "笔数", "累计")
TRANSACTION_LINE_SPLIT_RE = re.compile(r"[\n\r]+")
STRIP_PUNCT_RE = re.compile(r"[\s,，。；;:：|]+")


def _is_valid_datetime_str(value: str | None) -> bool:
    """时间格式规则：YYYY-MM-DD HH:mm:ss 且为合法日历日期。"""
    if not value:
        return False
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}", value):
        return False
    try:
        datetime.strptime(value, "%Y-%m-%d %H:%M:%S")
        return True
    except ValueError:
        return False


def check_amount(result: dict[str, Any]) -> list[str]:
    """规则①：金额格式（正数、最多两位小数、上限）。"""
    warnings: list[str] = []
    amount = result.get("amount")
    cents = result.get("amount_cents")
    if amount is None or cents is None:
        return ["金额缺失或无法解析，需人工确认"]
    if not isinstance(amount, (int, float)):
        return ["金额不是数值类型，需人工确认"]
    yuan = float(amount)
    if yuan <= AMOUNT_LOWER_BOUND:
        warnings.append(f"金额必须为正数（当前 {yuan}）")
    if round(yuan * 100) != cents:
        warnings.append("金额小数位超过两位，需人工确认")
    if yuan > AMOUNT_UPPER_BOUND:
        warnings.append(f"金额超出合理上限 {AMOUNT_UPPER_BOUND:.0f} 元（当前 {yuan}），疑似误识别")
    return warnings


def check_timestamp(result: dict[str, Any]) -> list[str]:
    """规则②：时间格式（14 位数字 ⇄ 合法日期）。"""
    timestamp = result.get("timestamp") or result.get("transaction_time")
    if not timestamp:
        return ["时间缺失，需人工确认"]
    if not _is_valid_datetime_str(timestamp):
        return [f"时间格式非法（应为 YYYY-MM-DD HH:mm:ss，当前 {timestamp!r}）"]
    digits = re.sub(r"\D", "", timestamp)
    if len(digits) != 14:
        return [f"时间 14 位数字形态非法（当前 {digits!r}）"]
    return []


def check_direction(result: dict[str, Any]) -> list[str]:
    """规则③：收支方向与交易类型一致。"""
    warnings: list[str] = []
    transaction_type = result.get("transaction_type")
    if transaction_type not in ("expense", "income"):
        warnings.append(f"收支方向缺失或非法（应为 expense/income，当前 {transaction_type!r}）")
        return warnings
    raw_text = str(result.get("raw_text") or "")
    if not raw_text:
        return warnings
    expense_hit = [kw for kw in ("支出", "付款", "支付", "消费", "转出") if kw in raw_text]
    income_hit = [kw for kw in ("收入", "收款", "入账", "红包", "转入") if kw in raw_text]
    if transaction_type == "expense" and income_hit and not expense_hit:
        warnings.append(f"收支方向与文本关键词矛盾（类型 expense，但文本含收入关键词 {income_hit}）")
    if transaction_type == "income" and expense_hit and not income_hit:
        warnings.append(f"收支方向与文本关键词矛盾（类型 income，但文本含支出关键词 {expense_hit}）")
    return warnings


def check_summary_line(result: dict[str, Any]) -> list[str]:
    """规则④：汇总行过滤（识别结果疑似账单汇总行而非单笔交易）。"""
    raw_text = str(result.get("raw_text") or "")
    if not raw_text:
        return []
    lines = [line.strip() for line in TRANSACTION_LINE_SPLIT_RE.split(raw_text) if line.strip()]
    for line in lines:
        if any(keyword in line for keyword in SUMMARY_LINE_KEYWORDS):
            if re.search(r"\d+(?:\.\d{1,2})?", line):
                return ["识别结果疑似来自账单汇总行（含 合计/总计/共N笔 等字样），请确认为单笔交易"]
    return []


def _line_signature(line: str, amount: Any) -> str:
    """交易行指纹：金额 + 行内出现的日期时间数字。"""
    normalized = STRIP_PUNCT_RE.sub("", line)
    dates = re.findall(r"\d{4}\d{2}\d{2}\d{2}\d{2}", re.sub(r"\D", "", normalized))
    return f"{amount}|{'|'.join(dates)}"


def check_duplicate_lines(result: dict[str, Any]) -> list[str]:
    """规则⑤：重复行去重（同一金额+时间出现在多行 → 疑似重复）。"""
    raw_text = str(result.get("raw_text") or "")
    amount = result.get("amount")
    if not raw_text or amount is None:
        return []
    lines = [line.strip() for line in TRANSACTION_LINE_SPLIT_RE.split(raw_text) if line.strip()]
    if len(lines) < 2:
        return []
    amount_str = str(amount)
    signatures: dict[str, int] = {}
    for line in lines:
        if amount_str in line or str(result.get("amount_cents")) in line:
            signature = _line_signature(line, amount_str)
            signatures[signature] = signatures.get(signature, 0) + 1
    duplicates = [sig for sig, count in signatures.items() if count > 1]
    if duplicates:
        return [f"检测到疑似重复交易行（相同金额+时间出现多次），请确认是否重复入账"]
    return []


def validate(result: dict[str, Any]) -> tuple[list[str], bool]:
    """执行全部纯文本规则。

    Returns:
        (追加的 warnings 列表, need_review 是否必须为 True)
    """
    warnings: list[str] = []
    warnings.extend(check_amount(result))
    warnings.extend(check_timestamp(result))
    warnings.extend(check_direction(result))
    warnings.extend(check_summary_line(result))
    warnings.extend(check_duplicate_lines(result))
    return warnings, bool(warnings)
