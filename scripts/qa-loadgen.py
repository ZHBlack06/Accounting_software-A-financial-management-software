# -*- coding: utf-8 -*-
"""QA 压测数据生成：向 data-qa 灌入 N 笔分散在近 180 天的交易（用于性能采样）。

用法：python scripts/qa-loadgen.py [N]
"""
import json
import os
import random
import sqlite3
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("QA_BASE", "http://127.0.0.1:8770")
DB = os.environ.get("QA_DB", "data-qa/ledger.db")
ACCOUNTS = ["acc_cmb0001", "acc_ali0001", "acc_wx00001"]
EXPENSE_CATS = ["cat_e01", "cat_e02", "cat_e03", "cat_e04", "cat_e05", "cat_e06", "cat_e09", "cat_e10"]


def token():
    db = sqlite3.connect(DB)
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
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def main():
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 1500
    tok = token()
    random.seed(20261110)
    now = int(time.time())
    ok = 0
    t0 = time.perf_counter()
    for i in range(n):
        days = random.randint(0, 179)
        secs = now - days * 86400 - random.randint(0, 86399)
        body = {
            "transaction_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(secs)),
            "transaction_type": "expense" if random.random() < 0.8 else "income",
            "amount_cents": random.randint(500, 50000),
            "account_id": random.choice(ACCOUNTS),
            "category_id": random.choice(EXPENSE_CATS) if random.random() < 0.8 else "cat_i01",
            "remark": f"loadgen-{i:05d}",
            "allow_frozen": True,
            "allow_duplicate": True,
        }
        st, _ = post("/api/transactions", body, tok)
        if st == 201:
            ok += 1
    dt = time.perf_counter() - t0
    print(f"created {ok}/{n} in {dt:.1f}s ({ok/dt:.0f} req/s)")
    return 0 if ok == n else 1


if __name__ == "__main__":
    sys.exit(main())
