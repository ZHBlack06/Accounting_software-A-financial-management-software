"""结构化提取提示词（ARCHITECTURE §8.3）：按 hint 选提示词。

要求模型"仅输出 JSON"，给出字段清单与示例。提示词与 PRD 7.3 字段一致。
"""

from __future__ import annotations

FIELD_SPEC = """字段清单（全部必答，无法识别填 null）：
{
  "timestamp":      "交易时间，格式 YYYY-MM-DD HH:mm:ss",
  "account":        "支付账户，如 微信零钱 / 支付宝余额 / 招商银行(1234)",
  "transaction_type": "expense 或 income（支出填 expense，收入填 income）",
  "amount":         "金额（数字，单位元，如 35.00）",
  "category":       "一级分类中文名，如 餐饮/交通/购物；无法判断填 null",
  "sub_category":   "二级分类中文名；无法判断填 null",
  "merchant":       "商户名，如 麦当劳静安寺店；无法判断填 null",
  "region":         "地区，如 310106 上海市静安区；无法判断填 null",
  "tags":           "标签数组，如 ["必要"]；无则 []",
  "remark":         "备注/商品说明摘要；无则 null",
  "confidence":     {"amount": 0-1, "timestamp": 0-1, "merchant": 0-1, "category": 0-1}
}"""

HINT_EXTRA: dict[str, str] = {
    "wechat": (
        "这是微信支付账单/交易截图。注意：\n"
        "- 账单列表截图中可能包含多行交易与底部汇总文字（如“共x笔、合计x元”），"
        "只提取最显著的一笔交易（金额字号最大或位于详情区的那笔），汇总行忽略。\n"
        "- 微信账单中“当前状态”为“已全额退款/退款成功”的行不是有效支出。"
    ),
    "alipay": (
        "这是支付宝账单/交易截图。注意：\n"
        "- 支付宝账单可能同时出现“收入金额/支出金额”两列，按收支方向择一。\n"
        "- 底部“合计/总计”汇总行不是交易，忽略。"
    ),
    "receipt": (
        "这是一张消费凭证/小票/交易详情截图。提取其中的一笔主要交易。"
    ),
}

SYSTEM_PROMPT = (
    "你是一个严谨的账单截图结构化提取助手。"
    "只依据图片中可见的文字提取字段，禁止编造图片中不存在的信息；"
    "无法识别的字段一律填 null。只输出一个 JSON 对象，不要输出任何解释文字、"
    "markdown 代码块或其他内容。"
)


def build_messages(hint: str) -> list[dict]:
    """构建 chat messages（system + user），供 ollama/openai 引擎使用。"""
    extra = HINT_EXTRA.get(hint, HINT_EXTRA["receipt"])
    user_content = (
        f"{extra}\n\n请提取图片中的交易信息，仅输出 JSON，字段如下：\n{FIELD_SPEC}\n\n"
        '输出示例：{"timestamp":"2026-10-03 12:30:15","account":"微信零钱",'
        '"transaction_type":"expense","amount":35.00,"category":"餐饮",'
        '"sub_category":null,"merchant":"麦当劳静安寺店","region":null,'
        '"tags":[],"remark":"午餐","confidence":{"amount":0.98,"timestamp":0.95,'
        '"merchant":0.92,"category":0.85}}'
    )
    return [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": user_content},
    ]
