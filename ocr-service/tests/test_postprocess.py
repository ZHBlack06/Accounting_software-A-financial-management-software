"""postprocess 单测：三类脏输出修复 + 字段规整 + id 生成。"""

from __future__ import annotations

import json

from app.pipeline.postprocess import (
    compute_id,
    extract_first_json,
    fix_single_quotes,
    fix_unquoted_keys,
    normalize_amount,
    normalize_result,
    normalize_timestamp,
    normalize_transaction_type,
    parse_model_json,
    remove_trailing_commas,
    strip_code_fence,
)


class TestCodeFence:
    def test_plain_json_untouched(self) -> None:
        assert strip_code_fence('{"a": 1}') == '{"a": 1}'

    def test_json_fence_removed(self) -> None:
        raw = '```json\n{"a": 1}\n```'
        assert strip_code_fence(raw) == '{"a": 1}'

    def test_bare_fence_removed(self) -> None:
        raw = '```\n{"a": 1}\n```'
        assert strip_code_fence(raw) == '{"a": 1}'


class TestTrailingComma:
    def test_object_trailing_comma(self) -> None:
        assert json.loads(remove_trailing_commas('{"a": 1,}')) == {"a": 1}

    def test_array_trailing_comma(self) -> None:
        assert json.loads(remove_trailing_commas('{"tags": ["a", "b",],}')) == {"tags": ["a", "b"]}

    def test_nested_trailing_commas(self) -> None:
        raw = '{"a": {"b": 1,}, "c": [1, 2,],}'
        assert json.loads(remove_trailing_commas(raw)) == {"a": {"b": 1}, "c": [1, 2]}

    def test_comma_inside_string_kept(self) -> None:
        raw = '{"remark": "午餐, 加购",}'
        assert json.loads(remove_trailing_commas(raw)) == {"remark": "午餐, 加购"}


class TestQuoteRepair:
    def test_single_quotes(self) -> None:
        raw = "{'amount': 35.00, 'merchant': '麦当劳'}"
        assert json.loads(fix_single_quotes(raw)) == {"amount": 35.00, "merchant": "麦当劳"}

    def test_unquoted_keys(self) -> None:
        raw = '{amount: 35.00, merchant: "麦当劳"}'
        assert json.loads(fix_unquoted_keys(raw)) == {"amount": 35.00, "merchant": "麦当劳"}

    def test_unquoted_keys_with_single_quotes_full_pipeline(self) -> None:
        raw = "{amount: 35.00, merchant: '麦当劳'}"
        assert parse_model_json(raw) == {"amount": 35.00, "merchant": "麦当劳"}


class TestParseModelJson:
    def test_clean_json(self) -> None:
        data = parse_model_json('{"amount": 35.00}')
        assert data == {"amount": 35.00}

    def test_fenced_with_trailing_comma_and_single_quotes(self) -> None:
        raw = "好的，识别结果如下：\n```json\n{'amount': 35.00, 'merchant': '麦当劳',}\n```"
        data = parse_model_json(raw)
        assert data == {"amount": 35.00, "merchant": "麦当劳"}

    def test_unquoted_keys_dirty(self) -> None:
        raw = "{amount: 35.00, timestamp: '2026-10-03 12:30:15', tags: [],}"
        data = parse_model_json(raw)
        assert data["amount"] == 35.00
        assert data["timestamp"] == "2026-10-03 12:30:15"
        assert data["tags"] == []

    def test_json_embedded_in_prose(self) -> None:
        raw = (
            "识别完成。结果：{\"amount\": 35.00, \"merchant\": \"麦当劳\"} "
            "如需重新识别请上传 clearer 图片。"
        )
        data = parse_model_json(raw)
        assert data == {"amount": 35.00, "merchant": "麦当劳"}

    def test_first_json_wins_when_multiple(self) -> None:
        raw = '{"amount": 35.00} 还有第二段 {"amount": 99.00}'
        data = parse_model_json(raw)
        assert data == {"amount": 35.00}

    def test_invalid_raises(self) -> None:
        try:
            parse_model_json("完全不是 JSON 的输出")
            raise AssertionError("应当抛出 ValueError")
        except ValueError:
            pass


class TestExtractFirstJson:
    def test_bracket_scan(self) -> None:
        text = '前置说明 {"a": {"b": [1, 2]}} 后置说明'
        assert extract_first_json(text) == '{"a": {"b": [1, 2]}}'

    def test_string_with_braces(self) -> None:
        text = '{"remark": "含}花括号"}'
        assert extract_first_json(text) == '{"remark": "含}花括号"}'


class TestNormalizeFields:
    def test_amount_variants(self) -> None:
        assert normalize_amount("¥1,234.56") == (1234.56, 123456)
        assert normalize_amount(35.005)[1] == 3501  # 四舍五入到分
        assert normalize_amount(None) == (None, None)
        assert normalize_amount("--") == (None, None)
        assert normalize_amount("35.00元") == (35.0, 3500)

    def test_timestamp_variants(self) -> None:
        assert normalize_timestamp("2026-10-03 12:30:15") == "2026-10-03 12:30:15"
        assert normalize_timestamp("2026/10/3 12:30") == "2026-10-03 12:30:00"
        assert normalize_timestamp("2026年10月3日 12时30分") == "2026-10-03 12:30:00"
        assert normalize_timestamp("20261003123015") == "2026-10-03 12:30:15"
        assert normalize_timestamp("2026-02-30 00:00:00") is None  # 非法日期
        assert normalize_timestamp(None) is None

    def test_transaction_type_map(self) -> None:
        assert normalize_transaction_type("支出") == "expense"
        assert normalize_transaction_type("收入") == "income"
        assert normalize_transaction_type("EXPENSE") == "expense"
        assert normalize_transaction_type("转账") is None
        assert normalize_transaction_type(None) is None

    def test_compute_id_checksum(self) -> None:
        # 202610031230150000 各位数字之和 = 26 → 26 % 11 = 4
        assert compute_id("2026-10-03 12:30:15") == "2026100312301500004"
        # 校验码 10 → 'X'：构造数字和为 10 的 18 位（1+2+3+4+0*14 = 10）
        assert compute_id("1234-00-00 00:00:00")[-1] == "X"

    def test_normalize_result_mock_output(self) -> None:
        engine_output = {
            "id": None,
            "source": "pc_local_screenshot",
            "timestamp": "2026-10-03 12:30:15",
            "account": "微信零钱",
            "transaction_type": "expense",
            "amount": 35.0,
            "category": "餐饮",
            "sub_category": None,
            "merchant": "麦当劳",
            "region": None,
            "tags": [],
            "remark": None,
            "confidence": {"amount": 0.3, "timestamp": 0.3, "merchant": 0.3, "category": 0.3},
            "raw_text": "微信支付 麦当劳 35.00元 2026-10-03 12:30:15",
            "model_used": "mock",
            "need_review": True,
            "warnings": ["未配置模型，使用启发式降级识别（mock 引擎），结果仅供参考"],
        }
        result = normalize_result(engine_output)
        # PRD 7.3 字段齐备
        for key in (
            "id", "source", "timestamp", "account", "transaction_type", "amount",
            "category", "sub_category", "merchant", "region", "tags", "remark",
            "confidence", "raw_text", "model_used", "need_review",
        ):
            assert key in result, f"缺少 PRD 7.3 字段 {key}"
        # §5.8 契约字段
        assert result["transaction_time"] == "2026-10-03 12:30:15"
        assert result["amount_cents"] == 3500
        assert result["account_id"] is None
        assert result["category_id"] is None
        assert result["id"] == "2026100312301500004"
        assert result["need_review"] is True

    def test_normalize_result_model_payload_dirty(self) -> None:
        engine_output = {
            "raw_text": "```json\n{'amount': '35.00元', 'transaction_type': '支出',"
                        " 'timestamp': '2026/10/3 12:30:15', 'tags': ['必要'],}\n```",
            "model_used": "ollama:qwen2.5vl:7b",
            "payload": "```json\n{'amount': '35.00元', 'transaction_type': '支出',"
                       " 'timestamp': '2026/10/3 12:30:15', 'tags': ['必要'],}\n```",
        }
        result = normalize_result(engine_output)
        assert result["amount"] == 35.0
        assert result["amount_cents"] == 3500
        assert result["transaction_type"] == "expense"
        assert result["transaction_time"] == "2026-10-03 12:30:15"
        assert result["tags"] == ["必要"]
        assert result["model_used"] == "ollama:qwen2.5vl:7b"

    def test_normalize_result_missing_fields(self) -> None:
        result = normalize_result({"raw_text": "", "model_used": "mock"})
        assert result["amount"] is None
        assert result["amount_cents"] is None
        assert result["transaction_type"] is None
        assert result["need_review"] is True
        assert any("字段缺失" in warning for warning in result["warnings"])

    def test_normalize_result_low_confidence_warns(self) -> None:
        payload = (
            '{"timestamp":"2026-10-03 12:30:15","transaction_type":"expense",'
            '"amount":35.00,"confidence":{"amount":0.4,"timestamp":0.9,'
            '"merchant":0.9,"category":0.9}}'
        )
        result = normalize_result({"raw_text": payload, "payload": payload, "model_used": "ollama:qwen2.5vl:7b"})
        assert result["need_review"] is True
        assert any("低置信度" in warning for warning in result["warnings"])
        assert any("amount" in warning for warning in result["warnings"])
