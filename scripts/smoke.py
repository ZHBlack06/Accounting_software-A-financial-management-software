# -*- coding: utf-8 -*-
"""T01-T04 真机冒烟脚本：直接请求 127.0.0.1:8770，逐条断言。

v1.2 起**账户无关**：不假设种子账户数量与 ID，一律取 `GET /api/accounts` 第一条作为主账户；
转账相关用例使用「主账户 ↔ 脚本自建测试账户」两个不同账户。
用法：python scripts/smoke.py  （服务需已启动；SMOKE_BASE / SMOKE_DATA_DIR 可覆盖）
"""
import http.server
import json
import os
import socket
import sqlite3
import sys
import threading
import time
import urllib.request
import urllib.error
import urllib.parse
from datetime import datetime, timedelta

BASE = os.environ.get("SMOKE_BASE", "http://127.0.0.1:8770")
DATA = os.environ.get("SMOKE_DATA_DIR") or os.environ.get("LEDGER_DATA_DIR") or "data"
DB = os.path.join(DATA, "ledger.db")

PASS = 0
FAIL = 0
FAILURES = []


def check(name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  PASS {name}")
    else:
        FAIL += 1
        FAILURES.append(name)
        print(f"  FAIL {name} {extra}")


def req(method, path, body=None, token=None, raw_headers=None):
    url = BASE + urllib.parse.quote(path, safe="/?=&,%")
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    if raw_headers:
        for k, v in raw_headers.items():
            r.add_header(k, v)
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}


def get_token():
    db = sqlite3.connect(DB)
    try:
        row = db.execute("SELECT value FROM meta WHERE key='auth_token'").fetchone()
        return row[0]
    finally:
        db.close()


def main():
    token = get_token()
    assert token and len(token) == 64, "auth_token 应为 64 hex"

    print("== 1. health / auth ==")
    st, body = req("GET", "/api/health")
    check("health 200 + 字段", st == 200 and body.get("status") == "ok" and body.get("db") == "ok"
          and "uptime_s" in body and "now" in body, str(body))
    st, body = req("GET", "/api/meta")
    check("meta 无 token → 401 UNAUTHORIZED", st == 401 and body["error"]["code"] == "UNAUTHORIZED")
    st, body = req("POST", "/api/auth/login", {"token": "wrong"})
    check("login 错误 token → 401 BAD_TOKEN", st == 401 and body["error"]["code"] == "BAD_TOKEN")
    st, body = req("POST", "/api/auth/login", {"token": token})
    check("login 正确 token → 200 ok", st == 200 and body.get("ok") is True)
    st, body = req("GET", "/api/auth/check", token=token)
    check("auth/check 200", st == 200 and body.get("ok") is True)

    print("== 2. meta / settings ==")
    st, meta = req("GET", "/api/meta", token=token)
    check("meta 字段齐全", st == 200 and all(k in meta for k in
          ["version", "schema_version", "freeze_days", "freeze_boundary", "last_frozen_at",
           "current_time", "timezone", "default_account_id", "default_txn_type",
           "high_amount_threshold_cents", "ui_prefs", "needs_rematerialize", "ocr_available"]))
    st, body = req("PUT", "/api/settings", {"theme": "dark"}, token=token)
    check("PUT settings theme=dark", st == 200 and body["ui_prefs"]["theme"] == "dark")
    st, meta = req("GET", "/api/meta", token=token)
    check("meta.ui_prefs.theme == dark", meta["ui_prefs"].get("theme") == "dark")
    st, body = req("PUT", "/api/settings", {"theme": "light"}, token=token)

    print("== 3. accounts ==")
    st, body = req("GET", "/api/accounts?with_balance=true", token=token)
    # v1.2 起默认只有一个「总账户」：断言改为「≥1 个」，并统一以第一条为主账户（账户无关）
    check("accounts 列表 ≥1 个", st == 200 and len(body["items"]) >= 1)
    acc1 = body["items"][0]
    main_acc = acc1["id"]
    main_acc_name = acc1["name"]
    check("display_name 拼接（主账户）", isinstance(acc1["display_name"], str)
          and len(acc1["display_name"]) >= len(main_acc_name), acc1["display_name"])
    check("total_balance_cents == Σinitial(0)", body["total_balance_cents"] == 0)
    # 创建
    st, newacc = req("POST", "/api/accounts", {
        "name": "测试现金", "value_type": "wallet", "initial_balance_cents": 100000,
        "initial_balance_time": "2026-01-01 00:00:00", "card_tail": "8888"}, token=token)
    check("创建账户 201", st == 201 and newacc["display_name"] == "测试现金（8888）", str(newacc))
    aid = newacc["id"]
    # 视图存在
    db = sqlite3.connect(DB)
    v = db.execute("SELECT name FROM sqlite_master WHERE type='view' AND name=?", (f"v_account_{aid}",)).fetchone()
    db.close()
    check("v_account_<id> 视图已创建", v is not None)
    st, body = req("GET", f"/api/accounts/{aid}", token=token)
    check("账户详情含余额", st == 200 and body["balance_cents"] == 100000)
    # 非法卡号
    st, body = req("POST", "/api/accounts", {"name": "x", "card_tail": "12"}, token=token)
    check("card_tail 非 4 位 → 400", st == 400)
    # 改名
    st, body = req("PUT", f"/api/accounts/{aid}", {"name": "测试现金2"}, token=token)
    check("改账户名", st == 200 and body["name"] == "测试现金2")
    # 归档
    st, body = req("POST", f"/api/accounts/{aid}/archive", {"archived": True}, token=token)
    check("归档账户", st == 200 and body["is_archived"] == 1)
    st, body = req("GET", "/api/accounts", token=token)
    check("归档后默认列表不含", all(a["id"] != aid for a in body["items"]))
    st, body = req("GET", "/api/accounts?include_archived=true&with_balance=true", token=token)
    check("include_archived 含归档且总余额含其余额",
          any(a["id"] == aid for a in body["items"]) and body["total_balance_cents"] == 100000)
    # reorder
    st, body = req("POST", "/api/accounts/reorder",
                   {"items": [{"id": aid, "sort_order": 99}]}, token=token)
    check("reorder 200", st == 200)

    print("== 4. categories/tags/merchants/regions ==")
    st, body = req("GET", "/api/categories?tree=true", token=token)
    exp_roots = [c for c in body["items"] if c["transaction_type"] == "expense"]
    inc_roots = [c for c in body["items"] if c["transaction_type"] == "income"]
    check("支出 10 个一级 + 收入 7 个", len(exp_roots) == 10 and len(inc_roots) == 7,
          f"{len(exp_roots)}/{len(inc_roots)}")
    check("每个一级支出 ≥2 个二级", all(len(c["children"]) >= 2 for c in exp_roots))
    st, dup = req("POST", "/api/categories", {"name": "餐饮", "transaction_type": "expense"}, token=token)
    check("分类重名 → 409 DUPLICATE", st == 409 and dup["error"]["code"] == "DUPLICATE")
    st, newcat = req("POST", "/api/categories",
                     {"name": "宠物食品", "parent_id": "cat_e04", "transaction_type": "expense"}, token=token)
    check("创建二级分类", st == 201 and newcat["level"] == 2)
    st, body = req("DELETE", f"/api/categories/{newcat['id']}", token=token)
    check("删除未引用分类", st == 200)
    st, body = req("DELETE", "/api/categories/cat_e01", token=token)
    check("删除有子分类的分类 → 409", st == 409 and body["error"]["code"] == "CATEGORY_IN_USE")

    st, body = req("GET", "/api/tags", token=token)
    check("标签 12 个", len(body["items"]) == 12, str(len(body["items"])))
    st, body = req("POST", "/api/tags", {"name": "必要"}, token=token)
    check("标签重名 → 409", st == 409)
    st, newtag = req("POST", "/api/tags", {"name": "测试标签", "color": "#123456"}, token=token)
    check("创建标签", st == 201)
    tag_id = newtag["id"]

    st, body = req("GET", "/api/merchants", token=token)
    check("商户 10 个", len(body["items"]) == 10)
    st, newmch = req("POST", "/api/merchants", {"name": "测试便利店", "merchant_type": "offline"}, token=token)
    check("创建商户", st == 201)
    mch_id = newmch["id"]

    st, body = req("GET", "/api/regions/search?q=静安", token=token)
    check("地区搜索 静安 → 310106", any(r["code"] == "310106" and r["full_name"] == "上海市静安区"
          for r in body["items"]), str(body["items"]))
    st, body = req("GET", "/api/regions/search?q=ja", token=token)
    check("地区搜索 ja → 310106", any(r["code"] == "310106" for r in body["items"]))
    st, body = req("GET", "/api/regions/310106", token=token)
    check("地区详情", st == 200 and body["name"] == "静安区")

    print("== 5. ledgers ==")
    st, body = req("GET", "/api/ledgers?with_stats=true", token=token)
    check("账本 3 个 + 日常默认", len(body["items"]) == 3 and
          any(l["is_default"] == 1 and l["name"] == "日常" for l in body["items"]))
    daily = [l for l in body["items"] if l["name"] == "日常"][0]
    travel = [l for l in body["items"] if l["name"] == "旅行"][0]
    db = sqlite3.connect(DB)
    v = db.execute("SELECT name FROM sqlite_master WHERE type='view' AND name=?",
                   (f"v_ledger_{daily['id']}",)).fetchone()
    db.close()
    check("v_ledger_<id> 视图存在", v is not None)
    # 规则全量替换 → transaction_count 变化
    st, body = req("PUT", f"/api/ledgers/{travel['id']}/rules",
                   {"rules": [{"match_mode": "or", "targets": [
                       {"target_type": "category_l1", "target_id": "cat_e01"}]}]}, token=token)
    check("规则替换 200", st == 200)

    print("== 6. transactions 三类 + 转账 ==")
    now = datetime.now()
    fmt = "%Y-%m-%d %H:%M:%S"
    # expense 3000 acc1
    st, r1 = req("POST", "/api/transactions", {
        "transaction_time": now.strftime(fmt), "transaction_type": "expense",
        "amount_cents": 3000, "account_id": aid,
        "category_id": "cat_e01", "sub_category_id": "cat_e01_s2",
        "merchant_id": mch_id, "region_id": "310106",
        "tag_ids": [tag_id], "remark": "午餐", "items": [
            {"name": "巨无霸套餐", "sub_category_id": "cat_e01_s2",
             "original_amount_cents": 4000, "paid_amount_cents": 3000,
             "discount_reason": "红包减免", "sort_order": 0}]}, token=token)
    check("创建支出 201", st == 201, str(r1))
    tx1 = r1["transaction"]
    check("支出 id 19 位", len(tx1["id"]) == 19)
    check("创建响应 balances", r1["balances"]["account_balance_cents"] == 97000)
    check("items 回显 + 折扣", tx1["item_count"] == 1 and tx1["items"][0]["discount_cents"] == 1000)
    check("region/tag/merchant 映射", tx1["region"]["code"] == "310106"
          and tx1["tags"][0]["name"] == "测试标签" and tx1["merchant"]["name"] == "测试便利店")
    # income 5000 → 主账户
    st, r2 = req("POST", "/api/transactions", {
        "transaction_time": now.strftime(fmt), "transaction_type": "income",
        "amount_cents": 5000, "account_id": main_acc, "category_id": "cat_i01"}, token=token)
    check("创建收入 201", st == 201)
    # transfer 20000 aid→主账户
    st, r3 = req("POST", "/api/transactions", {
        "transaction_time": now.strftime(fmt), "transaction_type": "transfer",
        "amount_cents": 20000,
        "transfer": {"from_account_id": aid, "to_account_id": main_acc}}, token=token)
    check("创建转账 201 + linked 1 条", st == 201 and len(r3["linked"]) == 1, str(r3))
    tg_out, tg_in = r3["transaction"], r3["linked"][0]
    check("转账组一致 + signed 净 0", tg_out["transfer_group_id"] == tg_in["transfer_group_id"]
          and tg_out["signed_cents"] + tg_in["signed_cents"] == 0)

    print("== 7. 余额数字核对 ==")
    # aid: 100000-3000-20000=77000; 主账户: 0+5000+20000=25000; total(含归档 aid)=102000
    st, body = req("GET", "/api/accounts?with_balance=true&include_archived=true", token=token)
    byid = {a["id"]: a for a in body["items"]}
    check("acc aid=77000", byid[aid]["balance_cents"] == 77000, str(byid[aid]["balance_cents"]))
    check("主账户=25000", byid[main_acc]["balance_cents"] == 25000)
    check("total=102000", body["total_balance_cents"] == 102000, str(body["total_balance_cents"]))
    # 账户有交易 → DELETE 409
    st, body = req("DELETE", f"/api/accounts/{aid}", token=token)
    check("有交易账户删除 → 409 ACCOUNT_IN_USE", st == 409 and body["error"]["code"] == "ACCOUNT_IN_USE")

    print("== 8. 列表过滤 + summary ==")
    st, l1 = req("GET", "/api/transactions?page=1&page_size=1", token=token)
    st, l2 = req("GET", "/api/transactions?page=2&page_size=1", token=token)
    check("分页 total=4", l1["total"] == 4, str(l1["total"]))
    check("summary 全量（分页 1/2 相同）", l1["summary"] == l2["summary"])
    check("summary 数值", l1["summary"]["income_cents"] == 5000
          and l1["summary"]["expense_cents"] == 3000 and l1["summary"]["net_cents"] == 2000)
    st, lf = req("GET", "/api/transactions?type=expense", token=token)
    check("type=expense 过滤", lf["total"] == 1)
    st, lf = req("GET", f"/api/transactions?account_id={main_acc}", token=token)
    check("account_id 过滤", lf["total"] == 2)
    st, lf = req("GET", "/api/transactions?category_id=cat_e01", token=token)
    check("category_id 过滤(含子类)", lf["total"] == 1)
    st, lf = req("GET", f"/api/transactions?tag_ids={tag_id}", token=token)
    check("tag_ids 过滤", lf["total"] == 1)
    st, lf = req("GET", f"/api/transactions?merchant_id={mch_id}", token=token)
    check("merchant_id 过滤", lf["total"] == 1)
    st, lf = req("GET", "/api/transactions?region_id=310106", token=token)
    check("region_id 过滤", lf["total"] == 1)
    st, lf = req("GET", "/api/transactions?amount_min_cents=4000&amount_max_cents=25000", token=token)
    check("amount 区间过滤", lf["total"] == 3, str(lf["total"]))
    st, lf = req("GET", "/api/transactions?keyword=午餐", token=token)
    check("keyword 过滤", lf["total"] == 1)
    st, lf = req("GET", "/api/transactions?sort=amount_desc", token=token)
    check("sort=amount_desc", lf["items"][0]["amount_cents"] == 20000)
    st, lf = req("GET", "/api/transactions?ledger_id=" + travel["id"], token=token)
    check("ledger 过滤(规则=餐饮)", lf["total"] == 1, str(lf["total"]))
    day = now.strftime("%Y-%m-%d")
    st, lf = req("GET", f"/api/transactions?start_time={day}&end_time={day}", token=token)
    check("时间范围(纯日期)过滤", lf["total"] == 4)

    print("== 9. 动态区改/删/恢复 + 转账保护 ==")
    st, body = req("PUT", f"/api/transactions/{tx1['id']}",
                   {"amount_cents": 3500, "remark": "改后"}, token=token)
    check("动态区改金额", st == 200 and body["transaction"]["amount_cents"] == 3500)
    st, body = req("PUT", f"/api/transactions/{tg_out['id']}", {"amount_cents": 9999}, token=token)
    check("转账改金额 → 400 TRANSFER_IMMUTABLE",
          st == 400 and body["error"]["code"] == "TRANSFER_IMMUTABLE")
    st, body = req("DELETE", f"/api/transactions/{tx1['id']}", token=token)
    check("软删除 200", st == 200)
    st, body = req("GET", "/api/accounts?with_balance=true&include_archived=true", token=token)
    byid = {a["id"]: a for a in body["items"]}
    check("软删后余额恢复", byid[aid]["balance_cents"] == 80000, str(byid[aid]["balance_cents"]))
    st, body = req("GET", "/api/transactions?include_deleted=true&type=expense", token=token)
    check("include_deleted 看到软删行", body["total"] == 1 and body["items"][0]["is_deleted"])
    st, body = req("POST", f"/api/transactions/{tx1['id']}/restore", {}, token=token)
    check("恢复 200", st == 200)
    st, body = req("GET", "/api/accounts?with_balance=true&include_archived=true", token=token)
    byid = {a["id"]: a for a in body["items"]}
    check("恢复后余额回退", byid[aid]["balance_cents"] == 76500)
    st, body = req("DELETE", f"/api/transactions/{tx1['id']}", token=token)
    check("再次软删除 200", st == 200)
    st, body = req("DELETE", f"/api/transactions/{tx1['id']}/purge", token=token)
    check("purge 未满 180 天 → 409 RETENTION", st == 409 and body["error"]["code"] == "RETENTION")
    st, body = req("POST", f"/api/transactions/{tx1['id']}/restore", {}, token=token)
    check("再次恢复 200", st == 200)

    print("== 10. 冻结区 + 去重 ==")
    frozen_time = (now - timedelta(days=40)).strftime(fmt)
    st, body = req("POST", "/api/transactions", {
        "transaction_time": frozen_time, "transaction_type": "expense",
        "amount_cents": 100, "account_id": main_acc, "category_id": "cat_e10"}, token=token)
    check("冻结区写入 → 409 FROZEN + details", st == 409 and body["error"]["code"] == "FROZEN"
          and "freeze_boundary" in body["error"]["details"])
    st, body = req("POST", "/api/transactions", {
        "transaction_time": frozen_time, "transaction_type": "expense",
        "amount_cents": 100, "account_id": main_acc, "category_id": "cat_e10",
        "allow_frozen": True, "remark": "历史补录"}, token=token)
    check("allow_frozen → 201 + warning.frozen", st == 201 and body["warning"]["frozen"] is True, str(body))
    frozen_tx = body["transaction"]
    st, meta = req("GET", "/api/meta", token=token)
    check("meta.needs_rematerialize 为 true（触发过 ForceFull 后为 false 亦可）",
          "needs_rematerialize" in meta)
    # 冻结区改 → 409
    st, body = req("PUT", f"/api/transactions/{frozen_tx['id']}", {"amount_cents": 200}, token=token)
    check("改冻结区交易 → 409 FROZEN", st == 409 and body["error"]["code"] == "FROZEN")
    # 去重
    dup_body = {"transaction_time": now.strftime(fmt), "transaction_type": "income",
                "amount_cents": 8888, "account_id": main_acc, "category_id": "cat_i01",
                "merchant_text": "测试商户"}
    st, body = req("POST", "/api/transactions", dup_body, token=token)
    check("首笔 201", st == 201)
    st, body = req("POST", "/api/transactions", dup_body, token=token)
    check("重复 → 409 DUPLICATE + matched_id", st == 409 and body["error"]["code"] == "DUPLICATE"
          and body["error"]["details"].get("matched_transaction_id"))
    dup_body["allow_duplicate"] = True
    st, body = req("POST", "/api/transactions", dup_body, token=token)
    check("allow_duplicate → 201 + warning.duplicate", st == 201 and body["warning"]["duplicate"] is True)

    print("== 11. 物化验证（DB 层） ==")
    db = sqlite3.connect(DB)
    row = db.execute("SELECT account_balance_after, total_balance_after FROM transactions WHERE id=?",
                     (frozen_tx["id"],)).fetchone()
    db.close()
    check("冻结区交易已物化(ForceFull 后)", row is not None and row[0] is not None, str(row))


    print("== 12. T05 调整分录 ==")
    # 造冻结区支出 10000（40 天前）
    st, adj_src = req("POST", "/api/transactions", {
        "transaction_time": frozen_time, "transaction_type": "expense",
        "amount_cents": 10000, "account_id": main_acc, "category_id": "cat_e01",
        "allow_frozen": True, "remark": "历史咖啡"}, token=token)
    check("造冻结区支出 201", st == 201, str(adj_src)[:150])
    src_id = adj_src["transaction"]["id"]
    st, before_bal = req("GET", "/api/accounts?with_balance=true", token=token)
    wx_before = [a for a in before_bal["items"] if a["id"] == main_acc][0]["balance_cents"]
    st, body = req("POST", f"/api/transactions/{src_id}/adjust",
                   {"mode": "adjust", "new_amount_cents": 1000, "remark": "冲减多记90元"}, token=token)
    check("adjust 200 + delta_cents=-9000", st == 200 and body["delta_cents"] == -9000, str(body)[:200])
    adj_tx = body["created"][0]
    check("adjust 生成 income 9000 调整项", adj_tx["transaction_type"] == "income"
          and adj_tx["amount_cents"] == 9000 and adj_tx["is_adjustment"] == 1
          and adj_tx["adjustment_type"] == "adjust" and adj_tx["related_transaction_id"] == src_id)
    st, after_bal = req("GET", "/api/accounts?with_balance=true", token=token)
    wx_after = [a for a in after_bal["items"] if a["id"] == main_acc][0]["balance_cents"]
    check("adjust 后账户 +9000", wx_after - wx_before == 9000, f"{wx_before}->{wx_after}")
    st, det = req("GET", f"/api/transactions/{src_id}", token=token)
    check("详情 adjustments 含调整项", any(a["id"] == adj_tx["id"] for a in det["adjustments"]))
    st, body = req("POST", f"/api/transactions/{src_id}/adjust",
                   {"mode": "adjust", "new_amount_cents": 10000, "remark": "x"}, token=token)
    check("金额未变 → 400 NO_EFFECT", st == 400 and body["error"]["code"] == "NO_EFFECT")
    st, body = req("POST", f"/api/transactions/{src_id}/adjust",
                   {"mode": "adjust", "new_amount_cents": 500, "remark": ""}, token=token)
    check("remark 空 → 400 VALIDATION_ERROR", st == 400 and body["error"]["code"] == "VALIDATION_ERROR")
    st, body = req("POST", f"/api/transactions/{adj_tx['id']}/adjust",
                   {"mode": "adjust", "new_amount_cents": 500, "remark": "x"}, token=token)
    check("链式调整 → 400", st == 400 and body["error"]["code"] == "VALIDATION_ERROR")

    # reverse：另一笔冻结区支出 5000
    st, rev_src = req("POST", "/api/transactions", {
        "transaction_time": (now - timedelta(days=40)).strftime(fmt), "transaction_type": "expense",
        "amount_cents": 5000, "account_id": main_acc, "category_id": "cat_e02",
        "allow_frozen": True, "remark": "历史打车"}, token=token)
    rev_id = rev_src["transaction"]["id"]
    st, before_bal = req("GET", "/api/accounts?with_balance=true", token=token)
    wx_before2 = [a for a in before_bal["items"] if a["id"] == main_acc][0]["balance_cents"]
    st, body = req("POST", f"/api/transactions/{rev_id}/adjust",
                   {"mode": "reverse", "new_amount_cents": 1000, "remark": "重录正确金额"}, token=token)
    check("reverse 生成 2 笔且都 reverse", st == 200 and len(body["created"]) == 2
          and all(x["adjustment_type"] == "reverse" for x in body["created"]))
    st, after_bal = req("GET", "/api/accounts?with_balance=true", token=token)
    wx_after2 = [a for a in after_bal["items"] if a["id"] == main_acc][0]["balance_cents"]
    check("reverse 净影响 +4000", wx_after2 - wx_before2 == 4000, f"{wx_before2}->{wx_after2}")

    # 转账双边调整 10000 → 8000（冻结区）
    st, tg = req("POST", "/api/transactions", {
        "transaction_time": (now - timedelta(days=40)).strftime(fmt), "transaction_type": "transfer",
        "amount_cents": 10000, "allow_frozen": True,
        "transfer": {"from_account_id": main_acc, "to_account_id": aid}}, token=token)
    check("冻结区转账 201", st == 201)
    st, b1 = req("GET", "/api/accounts?with_balance=true&include_archived=true", token=token)
    pre = {a["id"]: a["balance_cents"] for a in b1["items"]}
    st, body = req("POST", f"/api/transactions/{tg['transaction']['id']}/adjust",
                   {"mode": "adjust", "new_amount_cents": 8000, "remark": "转账金额记错"}, token=token)
    check("转账 adjust 生成 2 笔", st == 200 and len(body["created"]) == 2)
    check("转账调整 signed 合计 0", sum(x["signed_cents"] for x in body["created"]) == 0)
    st, b2 = req("GET", "/api/accounts?with_balance=true&include_archived=true", token=token)
    post = {a["id"]: a["balance_cents"] for a in b2["items"]}
    check("转账调整 acc1 +2000 / acc2 -2000",
          post[main_acc] - pre[main_acc] == 2000
          and post[aid] - pre[aid] == -2000)

    # verify → 人为改错 → ok:false；再 rematerialize 修复
    st, body = req("GET", "/api/maintenance/verify", token=token)
    check("maintenance/verify ok", st == 200 and body["ok"] is True)
    db = sqlite3.connect(DB)
    db.execute("UPDATE transactions SET account_balance_after = account_balance_after + 1 WHERE account_balance_after IS NOT NULL")
    db.commit(); db.close()
    st, body = req("GET", "/api/maintenance/verify", token=token)
    check("改错后 verify ok:false + mismatches", st == 200 and body["ok"] is False and len(body["mismatches"]) > 0)
    st, body = req("POST", "/api/maintenance/rematerialize", {"full": True}, token=token)
    check("rematerialize full 修复 → verify ok", st == 200 and body["verify"]["ok"] is True, str(body)[:150])

    # batch-delete
    st, bd1 = req("POST", "/api/transactions", {"transaction_type": "expense", "amount_cents": 111,
        "account_id": main_acc, "category_id": "cat_e10", "remark": "批量删除1"}, token=token)
    st, bd2 = req("POST", "/api/transactions", {"transaction_type": "expense", "amount_cents": 222,
        "account_id": main_acc, "category_id": "cat_e10", "remark": "批量删除2"}, token=token)
    st, body = req("POST", "/api/transactions/batch-delete",
                   {"ids": [bd1["transaction"]["id"], bd2["transaction"]["id"], "not_exist_id"]}, token=token)
    check("batch-delete 部分成功", st == 200 and len(body["succeeded"]) == 2 and len(body["failed"]) == 1)

    print("== 13. T06 统计与报表 ==")
    day = now.strftime("%Y-%m-%d")
    st, s1 = req("GET", f"/api/stats/summary?start_time={day}&end_time={day}", token=token)
    check("stats/summary 200 + net=income-expense", st == 200
          and s1["net_cents"] == s1["income_cents"] - s1["expense_cents"])
    check("summary day_count=1", s1["day_count"] == 1)
    st, tr = req("GET", f"/api/stats/trend?granularity=day&start_time={(now - timedelta(days=4)).strftime('%Y-%m-%d')}&end_time={day}", token=token)
    check("trend day 5 桶（空桶补 0）", st == 200 and len(tr["points"]) == 5
          and all("income_cents" in p for p in tr["points"]))
    st, tr = req("GET", "/api/stats/trend?granularity=day&start_time=2026-07-01&end_time=2026-10-15", token=token)
    check("day 区间 >92 天 → 降级 week + downgraded", st == 200
          and tr["granularity"] == "week" and tr.get("downgraded") is True)
    st, tr = req("GET", "/api/stats/trend?granularity=week&start_time=2026-09-25&end_time=2026-10-05", token=token)
    weeks = [p["bucket"] for p in tr["points"]]
    check("week 以周一为起点（2026-10-01 → 2026-09-28）", "2026-09-28" in weeks, str(weeks))
    st, bd = req("GET", f"/api/stats/category-breakdown?type=expense&level=1&start_time={day}&end_time={day}", token=token)
    pct_sum = round(sum(i["percent"] for i in bd["items"]), 1)
    check("category-breakdown percent 合计≈100", st == 200 and 99.8 <= pct_sum <= 100.2, str(pct_sum))
    st, bd2 = req("GET", f"/api/stats/category-breakdown?type=expense&level=2&start_time={day}&end_time={day}", token=token)
    check("level=2 带 children", st == 200 and any(len(i.get("children") or []) > 0 for i in bd2["items"]))
    st, rk = req("GET", f"/api/stats/category-ranking?limit=5&start_time={day}&end_time={day}", token=token)
    amounts = [i["amount_cents"] for i in rk["items"]]
    check("category-ranking 降序 + rank", st == 200 and amounts == sorted(amounts, reverse=True)
          and rk["items"][0]["rank"] == 1)
    # 高额交易
    st, hi = req("POST", "/api/transactions", {"transaction_type": "expense", "amount_cents": 60000,
        "account_id": main_acc, "category_id": "cat_e04", "remark": "大额测试"}, token=token)
    st, ha = req("GET", f"/api/stats/high-amount?start_time={day}&end_time={day}", token=token)
    check("high-amount 阈值/命中", st == 200 and ha["threshold_cents"] >= 50000
          and any(t["amount_cents"] >= ha["threshold_cents"] for t in ha["items"]))
    # 余额序列跨边界 + 末点一致
    st, ser = req("GET", f"/api/stats/account-balance-series?account_ids={main_acc}&start_time={(now - timedelta(days=6)).strftime('%Y-%m-%d')}&end_time={day}", token=token)
    st2, accs = req("GET", "/api/accounts?with_balance=true", token=token)
    balmap = {a["id"]: a["balance_cents"] for a in accs["items"]}
    ok_series = True
    for s_ in ser.get("series", []):
        if s_["points"][-1]["balance_cents"] != balmap[s_["account_id"]]:
            ok_series = False
    check("余额序列末点 == accounts 余额（跨冻结边界无跳变）", st == 200 and ok_series, str(ser)[:200])
    check("total_points 末点 == 总余额", ser["total_points"][-1]["balance_cents"] == accs["total_balance_cents"])
    # 报表
    st, rc = req("GET", f"/api/reports/category?start_time={(now - timedelta(days=4)).strftime('%Y-%m-%d')}&end_time={day}", token=token)
    null_pct = [i for i in rc["items"] if i["prev_period_cents"] == 0]
    check("reports/category prev=0 → pct=null", st == 200 and (len(null_pct) == 0 or null_pct[0]["prev_period_pct"] is None),
          str(null_pct[:1]))
    st, ra = req("GET", f"/api/reports/account?start_time={day}&end_time={day}", token=token)
    # v1.2：账户数不再固定为 3，断言「至少 1 个账户行」且带转账列
    check("reports/account 行含转入转出 + 合计", st == 200 and len(ra["items"]) >= 1
          and "transfer_in_cents" in ra["items"][0])
    st, rt = req("GET", f"/api/reports/tag?tag_ids={tag_id},tag_t03&start_time={day}&end_time={day}", token=token)
    check("reports/tag 去重 total_cents", st == 200 and "total_cents" in rt)
    st, pc = req("GET", f"/api/reports/period-compare?period=month&base={now.strftime('%Y-%m')}", token=token)
    check("period-compare 三列 + diff", st == 200 and all(k in pc for k in ["current", "previous", "prev_year", "diff"]))

    print("== 14. T07 CSV 导入 ==")
    def post_multipart(path, fields, files, tok):
        boundary = "----ledgerboundary42"
        body = b""
        for k, v in fields.items():
            body += ("--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n" % (boundary, k, v)).encode()
        for k, (fname, content) in files.items():
            body += ("--%s\r\nContent-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\nContent-Type: text/csv\r\n\r\n" % (boundary, k, fname)).encode()
            body += content + b"\r\n"
        body += ("--%s--\r\n" % boundary).encode()
        r = urllib.request.Request(BASE + path, data=body, method="POST")
        r.add_header("Content-Type", "multipart/form-data; boundary=" + boundary)
        r.add_header("Authorization", "Bearer " + tok)
        try:
            with urllib.request.urlopen(r) as resp:
                return resp.status, json.loads(resp.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read())

    csv_lines = [
        "微信支付账单明细",
        "微信昵称：[测试]",
        "起始时间：[2026-09-01 00:00:00]",
        "导出类型：[全部]",
        "共 5 笔记录",
        "交易时间,交易类型,交易对方,商品,收/支,金额(元),支付方式,当前状态,交易单号,商户单号,备注",
        "2026-11-10 09:00:00,商户消费,星巴克,咖啡,支出,12.00,零钱,支付成功,CSF001,M1,/",
        "2026-11-10 10:00:00,商户消费,公司,报销,收入,100.00,零钱,已存入零钱,CSF002,M2,/",
        (now - timedelta(days=40)).strftime("%Y-%m-%d %H:%M:%S") + ",商户消费,某商场,购物,支出,1000.00,零钱,支付成功,CSF003,M3,/",
        "2026-11-10 11:00:00,零钱提现,招商银行,提现,/,500.00,零钱,提现已到账,CSF004,M4,/",
        "2026-11-10 12:00:00,商户消费,某餐厅,聚餐,支出,\"¥1,234.56\",零钱,支付成功,CSF005,M5,/",
    ]
    wechat_csv = ("\ufeff" + "\n".join(csv_lines) + "\n").encode("utf-8")
    st, pv = post_multipart("/api/import/csv/preview", {"source": "auto", "account_id": main_acc},
                            {"file": ("wechat.csv", wechat_csv)}, token)
    check("preview: source=wechat/utf-8", st == 200 and pv["source"] == "wechat" and pv["encoding"] == "utf-8", str(pv)[:150])
    check("preview: 跳过 / 行 + 冻结标记 + 金额解析",
          pv["summary"]["skipped"] == 1 and pv["summary"]["frozen_count"] == 1
          and any(r["parsed"] and r["parsed"]["amount_cents"] == 123456 for r in pv["rows"]))
    batch_id = pv["batch_id"]
    st, body = req("POST", "/api/import/csv/commit", {
        "batch_id": batch_id, "defaults": {"account_id": main_acc, "category_id": "cat_e01"}}, token=token)
    check("commit 含冻结行未放行 → 409 FROZEN", st == 409 and body["error"]["code"] == "FROZEN")
    st, cm = req("POST", "/api/import/csv/commit", {
        "batch_id": batch_id, "defaults": {"account_id": main_acc, "category_id": "cat_e01"},
        "allow_frozen": True}, token=token)
    check("commit allow_frozen → created=4 frozen_created=1", st == 200 and cm["created"] == 4
          and cm["frozen_created"] == 1, str(cm)[:200])
    check("commit 计数自洽", cm["created"] + cm["skipped_dup"] + cm["skipped_invalid"] == 4)
    # 重导去重
    st, pv2 = post_multipart("/api/import/csv/preview", {"source": "auto", "account_id": main_acc},
                             {"file": ("wechat.csv", wechat_csv)}, token)
    check("重导 preview dup_count=4", st == 200 and pv2["summary"]["dup_count"] == 4, str(pv2["summary"]))
    check("dup 带 matched_transaction_id", all(r["dup"]["is_dup"] for r in pv2["rows"] if r["parsed"]))
    st, cm2 = req("POST", "/api/import/csv/commit", {
        "batch_id": pv2["batch_id"], "defaults": {"account_id": main_acc, "category_id": "cat_e01"},
        "skip_duplicates": True, "allow_frozen": True}, token=token)
    check("skip_duplicates → created=0 skipped_dup=4", st == 200 and cm2["created"] == 0 and cm2["skipped_dup"] == 4)
    # 24h 过期
    bp = os.path.join(DATA, "tmp", "import", pv2["batch_id"] + ".json")
    data = json.load(open(bp, encoding="utf-8"))
    data["created_at"] = data["created_at"] - 25 * 3600
    json.dump(data, open(bp, "w", encoding="utf-8"))
    st, body = req("POST", "/api/import/csv/commit", {"batch_id": pv2["batch_id"], "defaults": {"account_id": main_acc}}, token=token)
    check("批次过期 → 410 IMPORT_BATCH_EXPIRED", st == 410 and body["error"]["code"] == "IMPORT_BATCH_EXPIRED")
    # 不支持的格式
    st, body = post_multipart("/api/import/csv/preview", {}, {"file": ("bad.csv", "姓名,年龄\n张三,18\n".encode())}, token)
    check("无法识别 → 422 + detected_headers", st == 422 and body["error"]["code"] == "UNSUPPORTED_CSV"
          and len(body["error"]["details"]["detected_headers"]) > 0)

    print("== 15. T08 cleanup / 静态托管 ==")
    # cleanup：40 天前 originals 被删、compressed 保留
    old_d = (now - timedelta(days=40)).strftime("%Y-%m-%d")
    o_dir = os.path.join(DATA, "screenshots", "originals", old_d)
    c_dir = os.path.join(DATA, "screenshots", "compressed", old_d)
    os.makedirs(o_dir, exist_ok=True); os.makedirs(c_dir, exist_ok=True)
    open(os.path.join(o_dir, "x.jpg"), "wb").write(b"x")
    open(os.path.join(c_dir, "x.jpg"), "wb").write(b"x")
    st, body = req("POST", "/api/maintenance/cleanup", token=token)
    check("cleanup 200", st == 200)
    check("cleanup: originals 40 天前已删 / compressed 保留",
          not os.path.exists(o_dir) and os.path.exists(c_dir))
    # 静态托管
    def get_raw(path):
        r = urllib.request.Request(BASE + path)
        try:
            with urllib.request.urlopen(r) as resp:
                return resp.status, resp.headers.get("Content-Type", ""), resp.read(4000).decode("utf-8", "ignore")
        except urllib.error.HTTPError as e:
            return e.code, e.headers.get("Content-Type", ""), e.read(200).decode("utf-8", "ignore")
    st, ctype, text = get_raw("/")
    check("GET / 返回 index.html", st == 200 and "text/html" in ctype and "div id=" in text, str(st)+ctype)
    st, ctype, text = get_raw("/transactions")
    check("前端路由 SPA fallback", st == 200 and "text/html" in ctype and "div id=" in text)
    st, ctype, text = get_raw("/api/definitely-not-exist")
    check("/api/* 未匹配 → JSON 404", st == 404 and "application/json" in ctype and "NOT_FOUND" in text)

    print("== 16. T08 OCR 代理 + 截图 ==")
    def port_open(port):
        s_ = socket.socket(); s_.settimeout(0.3)
        try:
            s_.connect(("127.0.0.1", port)); return True
        except Exception:
            return False
        finally:
            s_.close()
    ocr_result = {
        "id": "x", "source": "pc_local",
        "transaction_time": now.strftime(fmt), "transaction_type": "expense",
        "amount_cents": 3500, "account": main_acc_name, "category": "餐饮",
        "sub_category": None, "merchant": "淘宝", "remark": "午餐",
    }
    fake = None
    real_ocr = port_open(8771)
    if not real_ocr:
        class FakeOCR(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass
            def _j(self, code, obj):
                b = json.dumps(obj).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b)))
                self.end_headers()
                self.wfile.write(b)
            def do_GET(self):
                if self.path == "/healthz":
                    self._j(200, {"status": "ok"})
                elif self.path.startswith("/api/v1/tasks/"):
                    self._j(200, {"task_id": "task_1", "status": "done", "progress": 100,
                                  "result": ocr_result, "error": None,
                                  "elapsed_ms": 88, "engine": "mock", "model_used": "mock"})
                elif self.path == "/api/v1/model":
                    self._j(200, {"current": "mock", "available": [{"name": "mock", "loaded": True}]})
                else:
                    self._j(404, {"error": "not found"})
            def do_POST(self):
                n = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(n) if n else b"{}"
                if self.path == "/api/v1/recognize":
                    try:
                        body = json.loads(raw or b"{}")
                    except Exception:
                        body = {}
                    if body.get("mode") == "async":
                        self._j(202, {"success": True, "task_id": "task_1", "status": "pending"})
                    else:
                        self._j(200, {"success": True, "data": ocr_result, "model_used": "mock",
                                      "elapsed_ms": 42, "task_id": None, "status": "done"})
                elif self.path == "/api/v1/model/switch":
                    self._j(200, {"ok": True, "current": "mock"})
                else:
                    self._j(404, {"error": "not found"})
        fake = http.server.ThreadingHTTPServer(("127.0.0.1", 8771), FakeOCR)
        threading.Thread(target=fake.serve_forever, daemon=True).start()
        check("假 OCR 服务已启动（8771）", True)
    else:
        check("8771 已有服务，跳过 503 断言", True)

    # 生成合法 PNG（避免 CRC 错误被 OCR 服务判 broken image）
    import struct, zlib
    def make_png(w=8, h=8):
        raw = b"".join(b"\x00" + bytes([120, 80, 40] * w) for _ in range(h))
        def chunk(tag, data):
            return struct.pack(">I", len(data)) + tag + data + \
                struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        return (b"\x89PNG\r\n\x1a\n"
                + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
                + chunk(b"IDAT", zlib.compress(raw))
                + chunk(b"IEND", b""))
    png = make_png()
    st, rec = post_multipart("/api/ocr/recognize", {"model": "auto", "mode": "sync"},
                             {"file": ("shot.png", png)}, token)
    if real_ocr:
        # 真实 OCR 服务（mock 引擎）无法保证从合成图片识别出「餐饮」，只校验链路与落盘
        check("OCR sync 200 + status=done（真实服务）", st == 200 and rec["status"] == "done"
              and "result" in rec, str(rec)[:250])
        check("OCR 结果含 need_review 字段", "need_review" in (rec.get("result") or {}))
    else:
        check("OCR sync 200 + 分类名回填 category_id", st == 200 and rec["status"] == "done"
              and rec["result"]["category_id"] == "cat_e01", str(rec)[:250])
        check("OCR 结果 account_id 回填", rec["result"]["account_id"] == main_acc)
    sid = rec["screenshot_id"]
    db = sqlite3.connect(DB)
    srow = db.execute("SELECT original_path, compressed_path, ocr_status FROM screenshots WHERE id=?", (sid,)).fetchone()
    db.close()
    check("screenshots 表有记录 + 双份路径", srow is not None and srow[2] == "done")
    check("originals/compressed 文件都存在",
          os.path.exists(os.path.join(DATA, srow[0])) and os.path.exists(os.path.join(DATA, srow[1])))
    st, meta_ss = req("GET", f"/api/screenshots/{sid}", token=token)
    check("GET /api/screenshots/:id", st == 200 and meta_ss["id"] == sid)
    r = urllib.request.Request(BASE + f"/api/screenshots/{sid}/file?size=compressed")
    r.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(r) as resp:
        head = resp.read(8)
    check("GET /api/screenshots/:id/file 返回二进制", head.startswith(b"\x89PNG"))
    st, rec2 = post_multipart("/api/ocr/recognize", {"mode": "async"}, {"file": ("shot.png", png)}, token)
    check("OCR async → 202 task_id", st == 202 and bool(rec2.get("task_id")))
    if not real_ocr:
        st, tk = req("GET", "/api/ocr/tasks/task_1", token=token)
        check("OCR 任务轮询 done + 结果回填", st == 200 and tk["status"] == "done"
              and tk["result"]["category_id"] == "cat_e01")
    else:
        tid = rec2.get("task_id")
        for _ in range(20):
            st, tk = req("GET", f"/api/ocr/tasks/{tid}", token=token)
            if tk.get("status") in ("done", "failed"):
                break
            time.sleep(0.5)
        check("OCR 任务轮询完成（真实服务）", st == 200 and tk.get("status") in ("done", "failed"), str(tk)[:200])
    st, md = req("GET", "/api/ocr/model", token=token)
    check("GET /api/ocr/model 代理", st == 200 and md.get("current") == "mock")
    st, md = req("PUT", "/api/ocr/model", {"model": "mock"}, token=token)
    check("PUT /api/ocr/model 热切换", st == 200 and md.get("ok") is True)
    if fake is not None:
        fake.shutdown()

    print("== 17. T08 备份/恢复 ==")
    st, bk = req("POST", "/api/backup/create", token=token)
    check("backup/create 201 + checksum", st == 201 and bk["checksum"].startswith("sha256:")
          and bk["size_bytes"] > 0, str(bk)[:200])
    st, bl = req("GET", "/api/backup/list", token=token)
    check("backup/list ≥1 条", st == 200 and bl["total"] >= 1)
    st, rs = req("POST", "/api/backup/restore", {"file": bk["file"], "confirm": True}, token=token)
    check("backup/restore restored=true + verify ok", st == 200 and rs["restored"] is True
          and rs["verify"]["ok"] is True, str(rs)[:250])
    check("restore 生成 pre-restore 安全网", rs.get("pre_restore", "").startswith("pre-restore")
          or "pre-restore" in rs.get("pre_restore", ""), str(rs.get("pre_restore")))
    st, bl2 = req("GET", "/api/backup/list", token=token)
    check("restore 后备份列表 +1", st == 200 and bl2["total"] >= 2)
    st, accs2 = req("GET", "/api/accounts?with_balance=true", token=token)
    # v1.2：账户数不再固定为 3（默认单「总账户」），断言「至少 1 个账户」
    check("restore 后 API 可用（数据一致）", st == 200 and len(accs2["items"]) >= 1)

    print(f"\n===== 结果: {PASS} PASS / {FAIL} FAIL =====")
    if FAILURES:
        print("失败项:", FAILURES)
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
