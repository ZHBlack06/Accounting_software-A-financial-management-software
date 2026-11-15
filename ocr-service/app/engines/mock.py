"""Mock 引擎：无模型时的确定性兜底（ARCHITECTURE §8.2 / TASKS T16）。

本引擎不做真实像素识别（无 OCR 依赖），而是基于确定性启发式：
1. 若图片携带 PNG tEXt/iTXt 文本元数据（键 ocr_text / Description /
   Comment，由测试夹具或上游写入），则以其作为 raw_text；
2. 否则 raw_text 为空字符串，所有字段置 null 并给出降级警告。

启发式规则（正则 + 关键词，全部确定性）：
- 金额：优先 ¥/￥ 前缀或"元"后缀的 1-2 位小数数字；
- 时间：YYYY-MM-DD HH:mm[:ss] / YYYY/M/D H:m / YYYY年M月D日；
- 收支方向：支出|付款|支付|消费|转出 → expense；收入|收款|入账|红包 → income；
- 账户：含"微信" → 微信零钱；含"支付宝" → 支付宝余额；含"零钱" → 微信零钱；
- 商户：含 店/公司/超市/餐厅/商场/百货/便利店 等后缀或已知品牌词的
  最长非金额非日期文本段；
- 分类：小规模品牌→分类关键词表（如 麦当劳→餐饮）。

永远 need_review=True、confidence 全 0.3。
"""

from __future__ import annotations

import re

from PIL import Image

from app.engines.base import BaseEngine

# ---- 确定性启发式词表 -----------------------------------------------------

EXPENSE_KEYWORDS = ("支出", "付款", "支付", "消费", "花费", "转出", "买")
INCOME_KEYWORDS = ("收入", "收款", "入账", "红包", "转入", "退款", "返现")

SOURCE_ACCOUNT_MAP: list[tuple[tuple[str, ...], str]] = [
    (("微信零钱", "零钱"), "微信零钱"),
    (("微信支付", "微信"), "微信支付"),
    (("支付宝余额", "支付宝"), "支付宝余额"),
    (("银行卡", "储蓄卡", "信用卡"), "银行卡"),
]

MERCHANT_SUFFIXES = (
    "店", "公司", "超市", "餐厅", "商场", "百货", "便利店", "外卖",
    "咖啡", "酒店", "影院", "药房", "银行", "加油站", "食堂", "便利店",
)
MERCHANT_BRANDS = (
    "麦当劳", "肯德基", "星巴克", "瑞幸", "必胜客", "汉堡王", "海底捞",
    "美团", "饿了么", "京东", "淘宝", "天猫", "拼多多", "滴滴", "高德",
    "12306", "中国移动", "中国联通", "中国电信",
)

CATEGORY_KEYWORD_MAP: list[tuple[tuple[str, ...], str]] = [
    (("麦当劳", "肯德基", "餐厅", "外卖", "饿了么", "美团", "必胜客", "汉堡王",
      "星巴克", "瑞幸", "咖啡", "海底捞", "食堂", "食"), "餐饮"),
    (("滴滴", "高德", "出租车", "地铁", "公交", "加油", "12306", "火车", "机票"), "交通"),
    (("京东", "淘宝", "天猫", "拼多多", "超市", "便利店", "百货"), "购物"),
    (("中国移动", "中国联通", "中国电信", "话费", "流量"), "通讯"),
    (("电影", "影院", "游戏", "视频会员"), "娱乐"),
]

AMOUNT_WITH_MARK = re.compile(r"[¥￥]\s*(\d[\d,]*(?:\.\d{1,2})?)")
AMOUNT_WITH_UNIT = re.compile(r"(\d[\d,]*(?:\.\d{1,2})?)\s*[元块]")
AMOUNT_PLAIN = re.compile(r"(?<![\d.])(\d[\d,]*\.\d{1,2})(?![\d])")
DATETIME_ISO = re.compile(r"(\d{4})[-/年](\d{1,2})[-/月](\d{1,2})[日T\s]+(\d{1,2}):(\d{2})(?::(\d{2}))?")
DATETIME_CN_TIME = re.compile(r"(\d{4})[-/年](\d{1,2})[-/月](\d{1,2})[日T\s]*(\d{1,2})时(\d{1,2})分(?:(\d{1,2})秒)?")
TIME_ONLY = re.compile(r"(\d{1,2}):(\d{2})(?::(\d{2}))?")
DATE_ONLY = re.compile(r"(\d{4})[-/年](\d{1,2})[-/月](\d{1,2})日?")

MOCK_WARNING = "未配置模型，使用启发式降级识别（mock 引擎），结果仅供参考"
EMPTY_TEXT_WARNING = "图片未携带文本元数据，mock 引擎无法提取内容，请配置真实模型"


def _dedupe_keep_order(items: list[str]) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for item in items:
        if item and item not in seen:
            seen.add(item)
            out.append(item)
    return out


def split_tokens(text: str) -> list[str]:
    """按空白与常见分隔符切分文本段（保留中文连续段）。"""
    raw = re.split(r"[\s|,，、;；\t\n]+", text)
    return [t.strip() for t in raw if t.strip()]


def extract_raw_text(image: Image.Image) -> str:
    """从 PIL 图像对象提取文本元数据（确定性来源）。"""
    text_meta = getattr(image, "text", None)
    if isinstance(text_meta, dict):
        for key in ("ocr_text", "Description", "description", "Comment", "comment"):
            value = text_meta.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    hint_attr = getattr(image, "ocr_text", None)
    if isinstance(hint_attr, str) and hint_attr.strip():
        return hint_attr.strip()
    return ""


def parse_amount(text: str) -> float | None:
    """确定性提取金额（元）。优先带 ¥/￥ 前缀，其次带 元/块 后缀，最后裸小数。"""
    for pattern in (AMOUNT_WITH_MARK, AMOUNT_WITH_UNIT):
        matches = pattern.findall(text)
        if matches:
            candidates = [m.replace(",", "") for m in matches]
            # 取最后一个带标记的金额（账单详情页金额通常在末尾）
            return float(candidates[-1])
    matches = AMOUNT_PLAIN.findall(text)
    if matches:
        candidates = [m.replace(",", "") for m in matches]
        return float(candidates[0])
    return None


def parse_datetime(text: str) -> str | None:
    """确定性提取时间，规整为 YYYY-MM-DD HH:MM:SS。"""
    match = DATETIME_CN_TIME.search(text)
    if match:
        year, month, day, hour, minute, second = match.groups()
        return f"{int(year):04d}-{int(month):02d}-{int(day):02d} {int(hour):02d}:{int(minute):02d}:{int(second or 0):02d}"
    match = DATETIME_ISO.search(text)
    if match:
        year, month, day, hour, minute, second = match.groups()
        return f"{int(year):04d}-{int(month):02d}-{int(day):02d} {int(hour):02d}:{int(minute):02d}:{int(second or 0):02d}"
    date_match = DATE_ONLY.search(text)
    if date_match:
        year, month, day = date_match.groups()
        time_match = TIME_ONLY.search(text)
        hour, minute, second = ("0", "0", "0")
        if time_match:
            hour, minute, second = time_match.groups(default="0")
        return f"{int(year):04d}-{int(month):02d}-{int(day):02d} {int(hour):02d}:{int(minute):02d}:{int(second or 0):02d}"
    return None


def parse_direction(text: str) -> str | None:
    """确定性判断收支方向：expense / income / None（未知或矛盾）。"""
    has_expense = any(keyword in text for keyword in EXPENSE_KEYWORDS)
    has_income = any(keyword in text for keyword in INCOME_KEYWORDS)
    if has_expense and not has_income:
        return "expense"
    if has_income and not has_expense:
        return "income"
    return None


def parse_account(text: str) -> str | None:
    """确定性推断账户名。"""
    for keywords, account in SOURCE_ACCOUNT_MAP:
        if any(keyword in text for keyword in keywords):
            return account
    return None


def parse_merchant(text: str, tokens: list[str]) -> str | None:
    """取含商户后缀或品牌词的最长文本段作为商户。"""
    candidates: list[str] = []
    for token in tokens:
        if any(suffix in token for suffix in MERCHANT_SUFFIXES):
            candidates.append(token)
        elif any(brand in token for brand in MERCHANT_BRANDS):
            candidates.append(token)
    if candidates:
        return max(candidates, key=len)
    return None


def parse_category(text: str) -> str | None:
    """小规模品牌/关键词 → 一级分类（启发式，供人工确认）。"""
    for keywords, category in CATEGORY_KEYWORD_MAP:
        if any(keyword in text for keyword in keywords):
            return category
    return None


def parse_source(text: str) -> str:
    """识别截图来源（微信/支付宝/未知）。"""
    if "微信" in text:
        return "wechat"
    if "支付宝" in text:
        return "alipay"
    return "unknown"


class MockEngine(BaseEngine):
    """无模型兜底引擎：正则 + 关键词启发式，永远 need_review=True。"""

    def __init__(self) -> None:
        self.name = "mock"
        self._loaded = False

    async def load(self) -> None:
        self._loaded = True

    async def unload(self) -> None:
        self._loaded = False

    async def health(self) -> bool:
        return self._loaded

    async def recognize(self, image: Image.Image, hint: str) -> dict:
        raw_text = extract_raw_text(image)
        warnings: list[str] = [MOCK_WARNING]
        tokens = split_tokens(raw_text)

        amount = parse_amount(raw_text) if raw_text else None
        timestamp = parse_datetime(raw_text) if raw_text else None
        direction = parse_direction(raw_text) if raw_text else None
        account = parse_account(raw_text) if raw_text else None
        merchant = parse_merchant(raw_text, tokens) if raw_text else None
        category = parse_category(raw_text) if raw_text else None

        if not raw_text:
            warnings.append(EMPTY_TEXT_WARNING)
        if direction is None:
            warnings.append("未识别到收支方向关键词，transaction_type 置空待人工确认")
        if amount is None:
            warnings.append("未识别到金额字段")

        confidence = {"amount": 0.3, "timestamp": 0.3, "merchant": 0.3, "category": 0.3}
        return {
            "id": None,
            "source": "pc_local_screenshot",
            "timestamp": timestamp,
            "account": account,
            "transaction_type": direction,
            "amount": amount,
            "category": category,
            "sub_category": None,
            "merchant": merchant,
            "region": None,
            "tags": [],
            "remark": None,
            "confidence": confidence,
            "raw_text": raw_text,
            "model_used": "mock",
            "need_review": True,
            "warnings": _dedupe_keep_order(warnings),
        }
