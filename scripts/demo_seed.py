# -*- coding: utf-8 -*-
"""演示数据：在干净库上创建 8 笔近 20 天内的演示交易（服务需已启动）。"""
import json
import os
import sqlite3
import urllib.request
import urllib.error
from datetime import datetime, timedelta

BASE = os.environ.get("SMOKE_BASE", "http://127.0.0.1:8770")
DATA = os.environ.get("LEDGER_DATA_DIR", "data")


def token():
    db = sqlite3.connect(os.path.join(DATA, "ledger.db"))
    try:
        return db.execute("SELECT value FROM meta WHERE key='auth_token'").fetchone()[0]
    finally:
        db.close()


def post(path, body, tok):
    r = urllib.request.Request(BASE + path, data=json.dumps(body).encode(), method="POST")
    r.add_header("Content-Type", "application/json")
    r.add_header("Authorization", "Bearer " + tok)
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def main():
    tok = token()
    now = datetime.now()

    def days_ago(n, h=12, m=0):
        return (now - timedelta(days=n)).replace(hour=h, minute=m, second=0).strftime("%Y-%m-%d %H:%M:%S")

    txs = [
        # 18 天前：工资入账
        {"transaction_time": days_ago(18, 9), "transaction_type": "income", "amount_cents": 1500000,
         "account_id": "acc_cmb0001", "category_id": "cat_i01", "remark": "10 月工资"},
        # 15 天前：餐饮（含明细与标签）
        {"transaction_time": days_ago(15, 12, 30), "transaction_type": "expense", "amount_cents": 6800,
         "account_id": "acc_cmb0001", "category_id": "cat_e01", "sub_category_id": "cat_e01_s2",
         "merchant_id": "mch_m04", "region_id": "310106", "tag_ids": ["tag_t03"], "remark": "团队午餐",
         "items": [{"name": "四人套餐", "original_amount_cents": 7800, "paid_amount_cents": 6800,
                    "discount_reason": "会员立减", "sort_order": 0}]},
        # 12 天前：转账 招行→余额宝
        {"transaction_time": days_ago(12, 20), "transaction_type": "transfer", "amount_cents": 200000,
         "transfer": {"from_account_id": "acc_cmb0001", "to_account_id": "acc_ali0001"},
         "remark": "转入余额宝理财"},
        # 9 天前：交通
        {"transaction_time": days_ago(9, 8, 45), "transaction_type": "expense", "amount_cents": 3200,
         "account_id": "acc_wx00001", "category_id": "cat_e02", "sub_category_id": "cat_e02_s3",
         "merchant_text": "滴滴出行", "tag_ids": ["tag_t07"], "remark": "出差打车"},
        # 6 天前：购物
        {"transaction_time": days_ago(6, 21), "transaction_type": "expense", "amount_cents": 45900,
         "account_id": "acc_ali0001", "category_id": "cat_e04", "sub_category_id": "cat_e04_s3",
         "merchant_id": "mch_m02", "tag_ids": ["tag_t05"], "remark": "机械键盘"},
        # 4 天前：兼职收入
        {"transaction_time": days_ago(4, 15), "transaction_type": "income", "amount_cents": 80000,
         "account_id": "acc_wx00001", "category_id": "cat_i03", "remark": "设计私单尾款"},
        # 2 天前：居住
        {"transaction_time": days_ago(2, 10), "transaction_type": "expense", "amount_cents": 15600,
         "account_id": "acc_cmb0001", "category_id": "cat_e03", "sub_category_id": "cat_e03_s2",
         "remark": "水电燃气"},
        # 今天：早餐
        {"transaction_time": days_ago(0, 8, 20), "transaction_type": "expense", "amount_cents": 1500,
         "account_id": "acc_wx00001", "category_id": "cat_e01", "sub_category_id": "cat_e01_s1",
         "merchant_text": "楼下包子铺", "remark": "早餐"},
        # 补充近 3 天的小额流水（图表更好看）
        {"transaction_time": days_ago(1, 19, 10), "transaction_type": "expense", "amount_cents": 2600,
         "account_id": "acc_ali0001", "category_id": "cat_e05", "sub_category_id": "cat_e05_s1",
         "merchant_text": "万达影城", "remark": "电影票"},
        {"transaction_time": days_ago(2, 12, 40), "transaction_type": "expense", "amount_cents": 1800,
         "account_id": "acc_wx00001", "category_id": "cat_e02", "sub_category_id": "cat_e02_s2",
         "merchant_text": "公交", "remark": "公交月卡"},
        {"transaction_time": days_ago(3, 21, 30), "transaction_type": "expense", "amount_cents": 8900,
         "account_id": "acc_cmb0001", "category_id": "cat_e04", "sub_category_id": "cat_e04_s2",
         "merchant_id": "mch_m01", "tag_ids": ["tag_t04"], "remark": "夏季短袖"},
    ]
    for i, t in enumerate(txs):
        st, body = post("/api/transactions", t, tok)
        assert st == 201, f"第{i+1}笔失败: {st} {body}"
        print(f"  {i+1}. {t['transaction_time']} {t['transaction_type']} {t['amount_cents']} -> {body['transaction']['id']}")

    # 核对余额
    r = urllib.request.Request(BASE + "/api/accounts?with_balance=true")
    r.add_header("Authorization", "Bearer " + tok)
    with urllib.request.urlopen(r) as resp:
        body = json.loads(resp.read())
    for a in body["items"]:
        print(f"  {a['display_name']}: {a['balance_cents']}")
    print(f"  total: {body['total_balance_cents']}")
    print("演示数据创建完成（%d 笔）" % len(txs) + "")


if __name__ == "__main__":
    main()
