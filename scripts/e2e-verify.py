# -*- coding: utf-8 -*-
"""T18 端到端回归矩阵（a–g）。

必须使用独立数据目录启动后端：
    LEDGER_DATA_DIR=./data-qa ./ledger.exe
用法：
    python scripts/e2e-verify.py            # 全部（不含 OCR 在线部分）
    python scripts/e2e-verify.py --ocr-up   # 追加 g2：OCR 服务在线后应 200/202
    python scripts/e2e-verify.py --ocr-down # 只跑 g1：OCR 不在线应 503

脚本只读/写 data-qa，绝不触碰 data/ledger.db。
"""
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

BASE = os.environ.get("QA_BASE", "http://127.0.0.1:8770")
DB = os.environ.get("QA_DB", "data-qa/ledger.db")

PASS = 0
FAIL = 0
RESULTS = []  # (matrix_item, name, ok, extra)


def check(item, name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  PASS [{item}] {name}")
    else:
        FAIL += 1
        print(f"  FAIL [{item}] {name} {extra}")
    RESULTS.append((item, name, bool(cond), extra))


def req(method, path, body=None, token=None, raw=False, ctype="application/json"):
    url = BASE + urllib.parse.quote(path, safe="/?=&,%:")
    data = body if isinstance(body, (bytes, type(None))) else (
        json.dumps(body).encode() if body is not None else None)
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Content-Type", ctype)
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r) as resp:
            payload = resp.read()
            return resp.status, (payload if raw else json.loads(payload or b"{}"))
    except urllib.error.HTTPError as e:
        payload = e.read()
        try:
            return e.code, (payload if raw else json.loads(payload or b"{}"))
        except Exception:
            return e.code, (payload if raw else {})


def get_token():
    db = sqlite3.connect(DB)
    try:
        row = db.execute("SELECT value FROM meta WHERE key='auth_token'").fetchone()
        return row[0], db
    finally:
        db.close()


def running_balances(account_id):
    """按 (transaction_time, created_seq) 重算账户逐笔余额与期末余额。"""
    db = sqlite3.connect(DB)
    try:
        initial = db.execute(
            "SELECT initial_balance_cents FROM accounts WHERE id=?", (account_id,)).fetchone()[0]
        rows = db.execute(
            "SELECT id, transaction_type, amount_cents, transfer_direction, deleted_at "
            "FROM transactions WHERE account_id=? "
            "ORDER BY transaction_time, created_seq", (account_id,)).fetchall()
    finally:
        db.close()
    run = int(initial or 0)
    out = {}
    for tid, typ, amount, direction, deleted_at in rows:
        if deleted_at is None:
            sign = -1 if (typ == "expense" or direction == "out") else 1
            run += sign * int(amount)
        out[tid] = run
    return out, run


def total_balance(token):
    st, body = req("GET", "/api/accounts?with_balance=true", token=token)
    assert st == 200, body
    return sum(int(it.get("balance_cents") or 0) for it in body["items"])


def summary(token, start, end):
    st, body = req(
        "GET", f"/api/stats/summary?start_time={start}&end_time={end}", token=token)
    assert st == 200, body
    return body


def create_txn(token, **kw):
    body = {
        "transaction_type": kw.get("type", "expense"),
        "amount_cents": kw["amount"],
        "account_id": kw.get("account", "acc_cmb0001"),
        "category_id": kw.get("category", "cat_e01"),
        "remark": "e2e-" + uuid.uuid4().hex[:8],
        "allow_duplicate": True,
    }
    if kw.get("time"):
        body["transaction_time"] = kw["time"]
    if kw.get("allow_frozen"):
        body["allow_frozen"] = True
    if kw.get("transfer"):
        body["transfer"] = kw["transfer"]
    st, resp = req("POST", "/api/transactions", body, token)
    return st, resp, body


def fmt(ts):
    return time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(ts))


# ---------------------------------------------------------------- matrix a
def matrix_a(token, freeze_days):
    """冻结边界 ±1 秒的写保护。"""
    print("\n== a) 冻结边界 ±1s 写保护 ==")
    now = int(time.time())
    boundary = now - freeze_days * 86400

    # 边界前 1 秒：allow_frozen=false 应被拒（409 FROZEN）
    st, resp, _ = create_txn(token, amount=111, time=fmt(boundary - 1))
    check("a", "边界前 1s 创建被拒 409 FROZEN",
          st == 409 and resp.get("error", {}).get("code") in ("FROZEN", "FROZEN_WRITE_DENIED"),
          f"status={st} body={str(resp)[:200]}")

    # 边界后 1 秒：应成功
    st, resp, _ = create_txn(token, amount=222, time=fmt(boundary + 1))
    ok_after = st == 201
    check("a", "边界后 1s 创建成功 201", ok_after, f"status={st} body={str(resp)[:200]}")
    if not ok_after:
        return
    new_id = resp["transaction"]["id"]

    # 新交易在解冻区 → PUT 允许
    st, put = req("PUT", f"/api/transactions/{new_id}", {"amount_cents": 333}, token)
    check("a", "边界后 1s 交易可 PUT 200", st == 200, f"status={st} body={str(put)[:200]}")

    # 用 allow_frozen 写入冻结区 → 201 + warning.frozen
    st, resp, _ = create_txn(token, amount=444, time=fmt(boundary - 1), allow_frozen=True)
    frozen_ok = st == 201 and resp.get("warning", {}).get("frozen") is True
    check("a", "allow_frozen=true 可写冻结区且返回 warning.frozen",
          frozen_ok, f"status={st} body={str(resp)[:250]}")
    if st == 201:
        fid = resp["transaction"]["id"]
        st2, put2 = req("PUT", f"/api/transactions/{fid}", {"amount_cents": 555}, token)
        check("a", "冻结区交易 PUT 被拒 409 FROZEN",
              st2 == 409 and put2.get("error", {}).get("code") == "FROZEN",
              f"status={st2} body={str(put2)[:200]}")
        st3, put3 = req("DELETE", f"/api/transactions/{fid}", None, token)
        check("a", "冻结区交易 DELETE 被拒 409 FROZEN",
              st3 == 409 and put3.get("error", {}).get("code") == "FROZEN",
              f"status={st3} body={str(put3)[:200]}")


# ---------------------------------------------------------------- matrix b
def matrix_b(token):
    """freeze_days 30→60→30 往返后余额与校验结果不变。"""
    print("\n== b) freeze_days 30→60→30 往返不变性 ==")
    st, acc_before = req("GET", "/api/accounts?with_balance=true", token=token)
    st2, ver_before = req("GET", "/api/maintenance/verify", token=token)
    check("b", "往返前 baseline 可读取", st == 200 and st2 == 200)
    total_before = sum(int(i.get("balance_cents") or 0) for i in acc_before["items"])
    ok_before = ver_before.get("ok")

    for days in (60, 30):
        st, body = req("PUT", "/api/settings", {"freeze_days": days}, token)
        check("b", f"PUT settings freeze_days={days} 200 且生效",
              st == 200 and body.get("freeze_days") == days,
              f"status={st} body={str(body)[:200]}")

    st, acc_after = req("GET", "/api/accounts?with_balance=true", token=token)
    st2, ver_after = req("GET", "/api/maintenance/verify", token=token)
    total_after = sum(int(i.get("balance_cents") or 0) for i in acc_after["items"])

    bal_before = {i["id"]: i.get("balance_cents") for i in acc_before["items"]}
    bal_after = {i["id"]: i.get("balance_cents") for i in acc_after["items"]}
    check("b", "往返后各账户余额逐项不变", bal_before == bal_after,
          f"{bal_before} vs {bal_after}")
    check("b", "往返后总余额不变", total_before == total_after,
          f"{total_before} vs {total_after}")
    check("b", "往返后 verify.ok 不变", ok_before == ver_after.get("ok"),
          f"{ok_before} vs {ver_after.get('ok')}")
    check("b", "往返后 verify 通过", ver_after.get("ok") is True,
          str(ver_after)[:250])


# ---------------------------------------------------------------- matrix c
def matrix_c(token):
    """内部转账 + 调整后 total_balance 与 income/expense 不变。"""
    print("\n== c) 转账 + 调整：总余额与收支口径不变 ==")
    now = int(time.time())
    start, end = fmt(now - 3 * 86400), fmt(now + 3600)
    total0 = total_balance(token)
    sum0 = summary(token, start, end)

    st, resp, _ = create_txn(
        token, type="transfer", amount=12345,
        transfer={"from_account_id": "acc_cmb0001", "to_account_id": "acc_ali0001"})
    check("c", "创建内部转账 201", st == 201, f"status={st} body={str(resp)[:200]}")
    if st != 201:
        return
    tr_id = resp["transaction"]["id"]
    total1 = total_balance(token)
    sum1 = summary(token, start, end)
    check("c", "转账后 total_balance 不变", total0 == total1, f"{total0} vs {total1}")
    check("c", "转账后 income/expense 不变",
          (sum0["income_cents"], sum0["expense_cents"]) ==
          (sum1["income_cents"], sum1["expense_cents"]),
          f"{sum0['income_cents']}/{sum0['expense_cents']} vs "
          f"{sum1['income_cents']}/{sum1['expense_cents']}")

    st, adj = req("POST", f"/api/transactions/{tr_id}/adjust",
                  {"mode": "adjust", "new_amount_cents": 10000, "remark": "e2e 转账金额纠正"},
                  token)
    check("c", "转账双边调整 200 且生成 2 笔",
          st == 200 and len(adj.get("created", [])) == 2,
          f"status={st} body={str(adj)[:250]}")
    if st == 200:
        deltas = [int(c["signed_cents"]) for c in adj["created"]]
        check("c", "调整双边净额为 0", sum(deltas) == 0, str(deltas))
        check("c", "delta_cents = -2345（12345→10000）",
              int(adj.get("delta_cents", 0)) == -2345, str(adj.get("delta_cents")))

    total2 = total_balance(token)
    sum2 = summary(token, start, end)
    check("c", "调整后 total_balance 仍不变", total0 == total2, f"{total0} vs {total2}")
    check("c", "调整后 income/expense 不变",
          (sum0["income_cents"], sum0["expense_cents"]) ==
          (sum2["income_cents"], sum2["expense_cents"]),
          f"{sum0['income_cents']}/{sum0['expense_cents']} vs "
          f"{sum2['income_cents']}/{sum2['expense_cents']}")
    check("c", "调整后 verify 通过",
          req("GET", "/api/maintenance/verify", token=token)[1].get("ok") is True)


# ---------------------------------------------------------------- matrix d
def matrix_d(token):
    """软删支出 → 总余额增加 → 恢复 → 还原。"""
    print("\n== d) 软删/恢复总余额闭环 ==")
    total0 = total_balance(token)
    st, resp, _ = create_txn(token, amount=6789)
    check("d", "创建支出 201", st == 201, f"status={st}")
    if st != 201:
        return
    tid = resp["transaction"]["id"]
    after_create = total_balance(token)
    check("d", "支出后总余额 -6789", after_create == total0 - 6789,
          f"{total0} vs {after_create}")

    st, _ = req("DELETE", f"/api/transactions/{tid}", None, token)
    after_del = total_balance(token)
    check("d", "软删后总余额回到基线", st == 200 and after_del == total0,
          f"status={st} {total0} vs {after_del}")

    st, _ = req("POST", f"/api/transactions/{tid}/restore", None, token)
    after_restore = total_balance(token)
    check("d", "恢复后总余额再次 -6789", st == 200 and after_restore == total0 - 6789,
          f"status={st} {after_create} vs {after_restore}")

    # 清理：软删掉这笔，保持基线
    req("DELETE", f"/api/transactions/{tid}", None, token)
    check("d", "清理后回到基线", total_balance(token) == total0)


# ---------------------------------------------------------------- matrix e
def matrix_e(token):
    """同秒 3 笔交易：created_seq 顺序与逐笔余额累计正确。"""
    print("\n== e) 同秒 3 笔 created_seq 与余额累计 ==")
    same_time = fmt(int(time.time()) - 3600)  # 同一秒，仍在动态区
    ids = []
    for amt in (1000, 2000, 3000):
        st, resp, _ = create_txn(token, amount=amt, time=same_time)
        if st != 201:
            check("e", "同秒交易创建失败", False, f"status={st} body={str(resp)[:200]}")
            return
        ids.append(resp["transaction"]["id"])

    st, listing = req(
        "GET", f"/api/transactions?account_id=acc_cmb0001&start_time={same_time}"
               f"&end_time={same_time}&page_size=50&sort=time_asc", token=token)
    check("e", "列表查询 200", st == 200, f"status={st}")
    if st != 200:
        return
    rows = [r for r in listing["items"] if r["id"] in ids]
    check("e", "3 笔均在列表中", len(rows) == 3, f"got {len(rows)}")

    db = sqlite3.connect(DB)
    try:
        q = ",".join("?" * len(ids))
        seq_rows = db.execute(
            f"SELECT id, created_seq, amount_cents FROM transactions WHERE id IN ({q}) "
            f"ORDER BY created_seq", ids).fetchall()
        seqs = [r[1] for r in seq_rows]
        check("e", "created_seq 严格递增且相邻差 1",
              seqs == list(range(seqs[0], seqs[0] + 3)) if seqs else False, str(seqs))
        check("e", "created_seq 顺序 == 创建顺序",
              [r[0] for r in seq_rows] == ids, f"{[r[0] for r in seq_rows]} vs {ids}")
        # 逐笔余额：余额列（物化或窗口）应连续累计
        bal_rows = [(r[0], r[2]) for r in seq_rows]
    finally:
        db.close()

    by_id = {r["id"]: r for r in rows}
    got_bal = [by_id[i].get("account_balance_after") for i, _ in bal_rows]
    check("e", "3 笔均返回 account_balance_after", all(b is not None for b in got_bal),
          str(got_bal))

    # 用 DB 全量重算逐笔余额（含 initial_balance），与 API 返回的窗口余额比对。
    # 这样既验证 created_seq 排序，也验证累计公式，且不受更晚交易影响。
    expected, final_delta = running_balances("acc_cmb0001")
    if all(b is not None for b in got_bal):
        b1, b2, b3 = got_bal
        check("e", "逐笔余额累计 = 前笔 -2000 / -3000",
              b2 == b1 - 2000 and b3 == b2 - 3000, f"{b1} -> {b2} -> {b3}")
        ok = all(int(b) == expected.get(i) for i, b in zip(ids, got_bal))
        check("e", "3 笔 account_balance_after == DB 重算值",
              ok, f"api={got_bal} db={[expected.get(i) for i in ids]}")

    # 账户余额（API）应等于 DB 重算的期末余额
    st, accs = req("GET", "/api/accounts?with_balance=true", token=token)
    acc_bal = {a["id"]: a.get("balance_cents") for a in accs["items"]}
    check("e", "acc1 API 余额 == DB 重算期末余额",
          acc_bal.get("acc_cmb0001") == final_delta,
          f"{acc_bal.get('acc_cmb0001')} vs {final_delta}")

    # 清理
    for i in ids:
        req("DELETE", f"/api/transactions/{i}", None, token)


# ---------------------------------------------------------------- matrix f
def matrix_f():
    """静态托管与 API 404 的 JSON 契约。"""
    print("\n== f) 静态托管 / SPA fallback / API 404 ==")
    st, body = req("GET", "/", raw=True)
    check("f", "GET / 返回 HTML 200",
          st == 200 and body.lstrip()[:15].lower().startswith(b"<!doctype") or b"<div id=" in body,
          f"status={st} head={body[:60]!r}")
    check("f", "GET / 含前端入口脚本", b"/assets/index-" in body, body[:200].decode("utf-8", "ignore"))

    st, body = req("GET", "/transactions", raw=True)
    check("f", "GET /transactions SPA fallback 返回 HTML",
          st == 200 and b"<div id=" in body and b"/assets/index-" in body,
          f"status={st} head={body[:60]!r}")

    st, body = req("GET", "/api/nope")
    is_json = isinstance(body, dict) and body.get("error", {}).get("code") == "NOT_FOUND"
    check("f", "GET /api/nope 返回 JSON 404（非 HTML）",
          st == 404 and is_json, f"status={st} body={str(body)[:200]}")


# ---------------------------------------------------------------- matrix g
def make_png(w=8, h=8):
    """生成一张合法 PNG（避免 CRC 错误被 OCR 服务判为 broken image）。"""
    import struct
    import zlib
    raw = b"".join(b"\x00" + bytes([120, 80, 40] * w) for _ in range(h))

    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + \
            struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw))
            + chunk(b"IEND", b""))


PNG_1PX = make_png()


def ocr_recognize(token, mode=None, hint=None):
    boundary = "----qa" + uuid.uuid4().hex
    parts = []
    if mode:
        parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"mode\""
                     f"\r\n\r\n{mode}\r\n")
    if hint is not None:
        parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"hint\""
                     f"\r\n\r\n{hint}\r\n")
    parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; "
                 f"filename=\"qa.png\"\r\nContent-Type: image/png\r\n\r\n")
    body = "".join(parts).encode() + PNG_1PX + f"\r\n--{boundary}--\r\n".encode()
    return req("POST", "/api/ocr/recognize", body, token,
               ctype=f"multipart/form-data; boundary={boundary}")


def matrix_g_down(token):
    print("\n== g1) OCR 服务未启动 → 503 OCR_UNAVAILABLE，后端不崩 ==")
    st, body = ocr_recognize(token)
    check("g", "POST /api/ocr/recognize 返回 503 OCR_UNAVAILABLE",
          st == 503 and body.get("error", {}).get("code") == "OCR_UNAVAILABLE",
          f"status={st} body={str(body)[:250]}")
    st2, health = req("GET", "/api/health")
    check("g", "OCR 不可用时后端仍健康（health 200 / db ok）",
          st2 == 200 and health.get("db") == "ok" and health.get("ocr") == "unavailable",
          str(health)[:200])
    st3, verify = req("GET", "/api/maintenance/verify", token=token)
    check("g", "OCR 失败后后端未崩（verify 仍可访问）", st3 == 200, f"status={st3}")


def ocr_recognize_sid(token, screenshot_id, mode="sync"):
    """按已有截图 ID 触发识别（不重传文件），用于验证原图缺失等失败路径。"""
    boundary = "----qa" + uuid.uuid4().hex
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"screenshot_id\""
            f"\r\n\r\n{screenshot_id}\r\n"
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"mode\""
            f"\r\n\r\n{mode}\r\n"
            f"--{boundary}--\r\n").encode()
    return req("POST", "/api/ocr/recognize", body, token,
               ctype=f"multipart/form-data; boundary={boundary}")


def matrix_g_up(token):
    print("\n== g2) OCR 服务启动后 → 200/202 ==")
    st, body = ocr_recognize(token)
    check("g", "POST /api/ocr/recognize 返回 200 或 202",
          st in (200, 202), f"status={st} body={str(body)[:300]}")
    if st in (200, 202):
        check("g", "返回含 screenshot_id / status",
              bool(body.get("screenshot_id")) and bool(body.get("status")),
              str(body)[:250])
        # 严格校验：sync 结果必须是真实识别内容，不能把 OCR 错误体当成功返回
        result = body.get("result") or {}
        check("g", "sync 结果不含嵌套 error/detail（错误不得被当成功吞掉）",
              not ({"error", "detail"} & set(result)), str(result)[:300])
        REAL_KEYS = {"amount", "merchant", "raw_text", "confidence", "data", "timestamp"}
        check("g", "sync 结果含真实识别字段（amount/merchant/raw_text 等）",
              bool(REAL_KEYS & set(result)), str(list(result))[:200])

    st, health = req("GET", "/api/health")
    check("g", "health.ocr = available", health.get("ocr") == "available", str(health)[:200])

    # 非法 hint 应被归一化过滤（不下发 OCR 不接受的字面量），不得 4xx/5xx；
    # 「4xx → 502」的契约由下面的「原图缺失」用例覆盖。
    st4, bad = ocr_recognize(token, mode="sync", hint="not-a-valid-hint")
    check("g", "非法 hint 被归一化：不报 422/500，正常 200 + done",
          st4 == 200 and bad.get("status") == "done",
          f"status={st4} body={str(bad)[:250]}")

    # async：应返回 202 + task_id，且能轮询
    st2, ares = ocr_recognize(token, mode="async")
    check("g", "async 模式返回 202 + task_id",
          st2 == 202 and bool(ares.get("task_id")),
          f"status={st2} body={str(ares)[:300]}")
    if ares.get("task_id"):
        st3, task = req("GET", f"/api/ocr/tasks/{ares['task_id']}", token=token)
        check("g", "async 任务轮询 200", st3 == 200, f"status={st3} body={str(task)[:200]}")

    # 原图缺失：OCR 返回 400 INVALID_IMAGE → 后端对外必须 502（不是 500），且落 ocr_status=failed
    st5, first = ocr_recognize(token, mode="sync")
    sid = first.get("screenshot_id")
    orig = first.get("original_path") or ""
    ok_setup = st5 == 200 and bool(sid) and bool(orig)
    check("g", "准备原图缺失场景（先成功识别 1 次）", ok_setup,
          f"status={st5} sid={sid} path={orig}")
    if ok_setup:
        img = os.path.join(os.path.dirname(DB), orig.replace("/", os.sep))
        removed = False
        try:
            os.remove(img)
            removed = not os.path.exists(img)
        except OSError as exc:
            print(f"  WARN 删除原图失败: {exc}")
        check("g", "原图已从磁盘删除", removed, img)

        st6, miss = ocr_recognize_sid(token, sid)
        check("g", "原图缺失（OCR 400 INVALID_IMAGE）→ 502 OCR_UPSTREAM_ERROR",
              st6 == 502 and miss.get("error", {}).get("code") == "OCR_UPSTREAM_ERROR",
              f"status={st6} body={str(miss)[:300]}")
        det = (miss.get("error") or {}).get("details") or {}
        check("g", "502 details 带 upstream 信息（status/endpoint）",
              det.get("status") == 400 and bool(det.get("endpoint")), str(det)[:200])

        db = sqlite3.connect(DB)
        try:
            row = db.execute("SELECT ocr_status, ocr_result_path FROM screenshots WHERE id=?",
                             (sid,)).fetchone()
        finally:
            db.close()
        check("g", "失败后 screenshots.ocr_status = failed 且无结果文件",
              bool(row) and row[0] == "failed" and not row[1], str(row))


def percentile(samples, p):
    if not samples:
        return 0.0
    s = sorted(samples)
    idx = min(len(s) - 1, int(round((p / 100.0) * (len(s) - 1))))
    return s[idx]


def timed(token, path, n=60):
    samples = []
    for _ in range(n):
        t0 = time.perf_counter()
        st, body = req("GET", path, token=token)
        samples.append((time.perf_counter() - t0) * 1000.0)
        if st != 200:
            print(f"  WARN {path} status={st}")
    return samples


def perf(token):
    """性能采样：列表/图表接口 P95 + 物化耗时。"""
    print("\n== perf) 接口时延与物化耗时 ==")
    now = int(time.time())
    start, end = fmt(now - 89 * 86400), fmt(now)
    cases = [
        ("列表 /api/transactions", "/api/transactions?page_size=20&sort=time_desc"),
        ("趋势 /api/stats/trend", f"/api/stats/trend?start_time={start}&end_time={end}&granularity=day"),
        ("占比 /api/stats/category-breakdown",
         f"/api/stats/category-breakdown?start_time={start}&end_time={end}&type=expense&level=2"),
        ("报表 /api/reports/category",
         f"/api/reports/category?start_time={start}&end_time={end}&type=expense&level=1"),
    ]
    for name, path in cases:
        samples = timed(token, path)
        print(f"  {name}: p50={percentile(samples, 50):.1f}ms "
              f"p95={percentile(samples, 95):.1f}ms max={max(samples):.1f}ms (n={len(samples)})")
    st, body = req("POST", "/api/maintenance/rematerialize", {"full": True}, token)
    verify = body.get("verify") or {}
    print(f"  全量物化 /api/maintenance/rematerialize?full=true: status={st} "
          f"seconds={body.get('seconds')} rows={body.get('rows')} "
          f"verify_ok={verify.get('ok')} checked_rows={verify.get('checked_rows')}")
    st, h = req("GET", "/api/health")
    print(f"  health.uptime_s={h.get('uptime_s')}")


def main():
    args = sys.argv[1:]
    token, _ = get_token()
    assert token and len(token) == 64, "auth_token 应为 64 hex"

    st, health = req("GET", "/api/health")
    if st != 200:
        print(f"后端未就绪: {st} {health}")
        return 2

    if "--ocr-down" in args:
        matrix_g_down(token)
        return report()
    if "--ocr-up" in args:
        matrix_g_up(token)
        return report()
    if "--perf" in args:
        perf(token)
        return report()

    st, settings = req("GET", "/api/settings", token=token)
    freeze_days = int(settings.get("freeze_days") or 30)
    print(f"freeze_days = {freeze_days}")

    matrix_g_down(token)
    matrix_a(token, freeze_days)
    matrix_b(token)
    matrix_c(token)
    matrix_d(token)
    matrix_e(token)
    matrix_f()
    return report()


def report():
    print(f"\n===== e2e-verify: {PASS} PASS / {FAIL} FAIL =====")
    items = {}
    for item, _name, ok, _extra in RESULTS:
        items.setdefault(item, [0, 0])
        items[item][0 if ok else 1] += 1
    for k in sorted(items):
        print(f"  [{k}] PASS={items[k][0]} FAIL={items[k][1]}")
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
