"""mock 引擎单测：确定性启发式（金额/时间/收支/账户/商户/分类）。"""

from __future__ import annotations

import asyncio

from PIL import Image

from app.engines.mock import (
    MockEngine,
    parse_account,
    parse_amount,
    parse_category,
    parse_datetime,
    parse_direction,
    parse_merchant,
    parse_source,
    split_tokens,
)
from app.pipeline.preprocess import load_image, preprocess
from tests.fixtures import FIXTURE_TEXT, ensure_fixture


def _run(coro):
    return asyncio.run(coro)


class TestHeuristics:
    def test_amount_with_yuan(self) -> None:
        assert parse_amount("微信支付 麦当劳 35.00元") == 35.0

    def test_amount_with_rmb_prefix(self) -> None:
        assert parse_amount("合计 ¥1,234.56") == 1234.56

    def test_amount_plain_decimal(self) -> None:
        assert parse_amount("消费 12.5 元宝") == 12.5

    def test_amount_none(self) -> None:
        assert parse_amount("没有金额") is None

    def test_datetime_full(self) -> None:
        assert parse_datetime("2026-10-03 12:30:15") == "2026-10-03 12:30:15"

    def test_datetime_no_seconds(self) -> None:
        assert parse_datetime("2026/10/3 12:30") == "2026-10-03 12:30:00"

    def test_datetime_chinese(self) -> None:
        assert parse_datetime("2026年10月3日 12时30分15秒") == "2026-10-03 12:30:15"

    def test_direction_expense(self) -> None:
        assert parse_direction("微信支付 支出 35.00") == "expense"

    def test_direction_income(self) -> None:
        assert parse_direction("收款到账 红包 8.88元") == "income"

    def test_direction_unknown(self) -> None:
        assert parse_direction("麦当劳 35.00元") is None

    def test_account_wechat(self) -> None:
        assert parse_account("微信支付 麦当劳") == "微信支付"

    def test_account_alipay(self) -> None:
        assert parse_account("支付宝 余额宝") == "支付宝余额"

    def test_merchant_by_suffix(self) -> None:
        tokens = split_tokens("微信支付 麦当劳静安寺餐厅 35.00元")
        assert parse_merchant("微信支付 麦当劳静安寺餐厅 35.00元", tokens) == "麦当劳静安寺餐厅"

    def test_merchant_by_brand(self) -> None:
        tokens = split_tokens(FIXTURE_TEXT)
        assert parse_merchant(FIXTURE_TEXT, tokens) == "麦当劳"

    def test_merchant_none(self) -> None:
        tokens = split_tokens("35.00元 2026-10-03")
        assert parse_merchant("35.00元 2026-10-03", tokens) is None

    def test_category_food(self) -> None:
        assert parse_category("麦当劳 35.00元") == "餐饮"

    def test_category_transport(self) -> None:
        assert parse_category("滴滴出行 23.50元") == "交通"

    def test_category_none(self) -> None:
        assert parse_category("杂项 10.00") is None

    def test_source(self) -> None:
        assert parse_source("微信支付") == "wechat"
        assert parse_source("支付宝") == "alipay"
        assert parse_source("小票") == "unknown"


class TestMockEngine:
    def test_load_unload_health(self) -> None:
        engine = MockEngine()
        _run(engine.load())
        assert _run(engine.health()) is True
        _run(engine.unload())
        assert _run(engine.health()) is False

    def test_recognize_fixture(self) -> None:
        """夹具 PNG（含 ocr_text 元数据）→ 结构化结果。"""
        fixture = ensure_fixture()
        image = preprocess(load_image(path=str(fixture)))
        engine = MockEngine()
        _run(engine.load())
        result = _run(engine.recognize(image, hint="wechat"))
        assert result["amount"] == 35.0
        assert result["timestamp"] == "2026-10-03 12:30:15"
        assert result["transaction_type"] == "expense"
        assert result["account"] == "微信支付"
        assert result["merchant"] == "麦当劳"
        assert result["category"] == "餐饮"
        assert result["model_used"] == "mock"
        assert result["need_review"] is True
        assert any("启发式降级" in warning for warning in result["warnings"])

    def test_recognize_empty_image(self) -> None:
        """无文本元数据的图片 → 全空字段 + 警告。"""
        image = Image.new("RGB", (100, 100), color="white")
        image.ocr_text = ""  # type: ignore[attr-defined]
        engine = MockEngine()
        _run(engine.load())
        result = _run(engine.recognize(image, hint="receipt"))
        assert result["amount"] is None
        assert result["transaction_type"] is None
        assert any("文本元数据" in warning for warning in result["warnings"])
