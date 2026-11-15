"""rules 单测：PRD 7.6 纯文本规则（金额/时间/方向/汇总行/重复行）。"""

from __future__ import annotations

from app.pipeline.rules import (
    check_amount,
    check_direction,
    check_duplicate_lines,
    check_summary_line,
    check_timestamp,
    validate,
)


def _base_result(**overrides: object) -> dict:
    result = {
        "amount": 35.0,
        "amount_cents": 3500,
        "timestamp": "2026-10-03 12:30:15",
        "transaction_time": "2026-10-03 12:30:15",
        "transaction_type": "expense",
        "raw_text": "微信支付 麦当劳 35.00元 2026-10-03 12:30:15",
    }
    result.update(overrides)
    return result


class TestAmount:
    def test_valid_amount(self) -> None:
        assert check_amount(_base_result()) == []

    def test_zero_amount(self) -> None:
        warnings = check_amount(_base_result(amount=0, amount_cents=0))
        assert any("正数" in warning for warning in warnings)

    def test_negative_amount(self) -> None:
        warnings = check_amount(_base_result(amount=-35.0, amount_cents=-3500))
        assert any("正数" in warning for warning in warnings)

    def test_missing_amount(self) -> None:
        warnings = check_amount(_base_result(amount=None, amount_cents=None))
        assert any("金额缺失" in warning for warning in warnings)

    def test_absurd_amount(self) -> None:
        warnings = check_amount(_base_result(amount=2e9, amount_cents=200000000000))
        assert any("合理上限" in warning for warning in warnings)


class TestTimestamp:
    def test_valid(self) -> None:
        assert check_timestamp(_base_result()) == []

    def test_missing(self) -> None:
        warnings = check_timestamp(_base_result(timestamp=None, transaction_time=None))
        assert any("时间缺失" in warning for warning in warnings)

    def test_bad_format(self) -> None:
        warnings = check_timestamp(_base_result(timestamp="2026/10/3 12:30", transaction_time="2026/10/3 12:30"))
        assert any("格式非法" in warning for warning in warnings)

    def test_illegal_date(self) -> None:
        warnings = check_timestamp(_base_result(timestamp="2026-02-30 12:30:15", transaction_time="2026-02-30 12:30:15"))
        assert any("非法" in warning for warning in warnings)


class TestDirection:
    def test_consistent_expense(self) -> None:
        assert check_direction(_base_result()) == []

    def test_missing_type(self) -> None:
        warnings = check_direction(_base_result(transaction_type=None))
        assert any("收支方向缺失" in warning for warning in warnings)

    def test_invalid_type(self) -> None:
        warnings = check_direction(_base_result(transaction_type="transfer"))
        assert any("收支方向缺失或非法" in warning for warning in warnings)

    def test_contradiction(self) -> None:
        raw = "收款到账 红包 35.00元"
        warnings = check_direction(_base_result(transaction_type="expense", raw_text=raw))
        assert any("矛盾" in warning for warning in warnings)


class TestSummaryLine:
    def test_normal_text_passes(self) -> None:
        assert check_summary_line(_base_result()) == []

    def test_summary_line_detected(self) -> None:
        raw = "微信支付 麦当劳 35.00元\n本月合计 12 笔 共 890.50 元"
        warnings = check_summary_line(_base_result(raw_text=raw))
        assert any("汇总行" in warning for warning in warnings)


class TestDuplicateLines:
    def test_single_line_passes(self) -> None:
        assert check_duplicate_lines(_base_result()) == []

    def test_duplicate_lines_detected(self) -> None:
        raw = (
            "微信支付 麦当劳 35.00元 2026-10-03 12:30:15\n"
            "微信支付 麦当劳 35.00元 2026-10-03 12:30:15\n"
            "微信支付 肯德基 45.00元 2026-10-04 08:10:00"
        )
        warnings = check_duplicate_lines(_base_result(raw_text=raw))
        assert any("重复" in warning for warning in warnings)


class TestValidate:
    def test_all_valid(self) -> None:
        warnings, need_review = validate(_base_result())
        assert warnings == []
        assert need_review is False

    def test_any_failure_forces_review(self) -> None:
        warnings, need_review = validate(_base_result(amount=None, amount_cents=None))
        assert need_review is True
        assert len(warnings) >= 1
