# -*- coding: utf-8 -*-
"""验证「OCR 上游返回 success:false」时后端不得把结果当成功（ocr_status 不能是 done）。

自包含：脚本自己起一个 stub OCR（8781）+ 一个独立后端（8780，数据目录 data-qa-fail），
跑完自动收尾。不触碰 data/ledger.db。

用法：python scripts/ocr-failmode-check.py
"""
import http.server
import json
import os
import shutil
import sqlite3
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
import zlib

STUB_PORT = 8781
BACKEND_PORT = 8780
DATA_DIR = "data-qa-fail"
DB = os.path.join(DATA_DIR, "ledger.db")
BASE = f"http://127.0.0.1:{BACKEND_PORT}"

PASS = 0
FAIL = 0


def check(name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  PASS {name}")
    else:
        FAIL += 1
        print(f"  FAIL {name} {extra}")


def make_png(w=8, h=8):
    raw = b"".join(b"\x00" + bytes([120, 80, 40] * w) for _ in range(h))

    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + \
            struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw))
            + chunk(b"IEND", b""))


STUB_MODE = {"mode": "success_false"}  # success_false | http500


class StubOCR(http.server.BaseHTTPRequestHandler):
    """可切换模式的 OCR stub：success:false 或 HTTP 500。"""

    def log_message(self, *args):
        pass

    def _json(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/healthz"):
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": {"code": "NOT_FOUND"}})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        if STUB_MODE["mode"] == "http500":
            self._json(500, {"error": {"code": "OCR_INTERNAL",
                                       "message": "模型加载失败（模拟上游 5xx）"}})
            return
        self._json(200, {"success": False,
                         "error": {"code": "STUB_FAIL", "message": "模拟识别失败"}})


def req(method, path, body=None, token=None, ctype="application/json"):
    url = BASE + path
    r = urllib.request.Request(url, data=body, method=method)
    r.add_header("Content-Type", ctype)
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}


def recognize(token, mode):
    png = make_png()
    b = "----qa" + uuid.uuid4().hex
    body = (f"--{b}\r\nContent-Disposition: form-data; name=\"mode\"\r\n\r\n{mode}\r\n"
            f"--{b}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"q.png\"\r\n"
            f"Content-Type: image/png\r\n\r\n").encode() + png + f"\r\n--{b}--\r\n".encode()
    return req("POST", "/api/ocr/recognize", body, token,
               ctype=f"multipart/form-data; boundary={b}")


def wait_up(url, timeout=20):
    for _ in range(timeout * 5):
        try:
            with urllib.request.urlopen(url, timeout=1):
                return True
        except Exception:
            time.sleep(0.2)
    return False


def screenshot_status(sid):
    db = sqlite3.connect(DB)
    try:
        return db.execute("SELECT ocr_status, ocr_result_path FROM screenshots WHERE id=?",
                          (sid,)).fetchone()
    finally:
        db.close()


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    stub = http.server.ThreadingHTTPServer(("127.0.0.1", STUB_PORT), StubOCR)
    threading.Thread(target=stub.serve_forever, daemon=True).start()
    print(f"stub OCR @127.0.0.1:{STUB_PORT} 已启动")

    env = dict(os.environ)
    env.update({
        "LEDGER_DATA_DIR": DATA_DIR,
        "LEDGER_PORT": str(BACKEND_PORT),
        "LEDGER_OCR_ENDPOINT": f"http://127.0.0.1:{STUB_PORT}",
    })
    proc = subprocess.Popen([os.path.join(os.getcwd(), "ledger.exe")], env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        if not wait_up(BASE + "/api/health"):
            print("后端未能启动")
            return 2
        db = sqlite3.connect(DB)
        token = db.execute("SELECT value FROM meta WHERE key='auth_token'").fetchone()[0]
        db.close()

        st, health = req("GET", "/api/health")
        check("stub OCR 在线（health.ocr=available）",
              st == 200 and health.get("ocr") == "available", str(health)[:150])

        # sync：上游 success:false → 必须 502，且落 failed（不能 done）
        st, body = recognize(token, "sync")
        check("success:false（sync）→ 502 OCR_UPSTREAM_ERROR",
              st == 502 and body.get("error", {}).get("code") == "OCR_UPSTREAM_ERROR",
              f"status={st} body={str(body)[:250]}")
        sid = ""
        db = sqlite3.connect(DB)
        try:
            row = db.execute("SELECT id FROM screenshots ORDER BY created_at DESC LIMIT 1").fetchone()
            sid = row[0] if row else ""
        finally:
            db.close()
        status = screenshot_status(sid) if sid else None
        check("success:false（sync）→ screenshots.ocr_status = failed",
              bool(status) and status[0] == "failed", str(status))
        check("success:false（sync）→ 不写 ocr_result_path",
              bool(status) and not status[1], str(status))

        # async：上游 success:false 无 task_id → 502，且状态绝不能是 done
        st2, body2 = recognize(token, "async")
        check("success:false（async）→ 502 OCR_UPSTREAM_ERROR",
              st2 == 502 and body2.get("error", {}).get("code") == "OCR_UPSTREAM_ERROR",
              f"status={st2} body={str(body2)[:250]}")
        db = sqlite3.connect(DB)
        try:
            row = db.execute("SELECT id FROM screenshots ORDER BY created_at DESC LIMIT 1").fetchone()
            sid2 = row[0] if row else ""
        finally:
            db.close()
        status2 = screenshot_status(sid2) if sid2 else None
        check("success:false（async）→ ocr_status 不是 done",
              bool(status2) and status2[0] != "done", str(status2))
        print(f"  INFO async 失败后 ocr_status={status2[0] if status2 else None}"
              f"（期望 failed，若是 processing 属 P2 轻微问题）")

        # 上游 5xx → 502 OCR_UPSTREAM_ERROR（只有连接不上才是 503）
        STUB_MODE["mode"] = "http500"
        st5, body5 = recognize(token, "sync")
        check("上游 5xx → 502 OCR_UPSTREAM_ERROR（不是 503/500）",
              st5 == 502 and body5.get("error", {}).get("code") == "OCR_UPSTREAM_ERROR",
              f"status={st5} body={str(body5)[:250]}")
        det5 = (body5.get("error") or {}).get("details") or {}
        check("5xx 的 502 details 带 status=500",
              det5.get("status") == 500, str(det5)[:200])
        st5b, health5 = req("GET", "/api/health")
        check("上游 5xx 后 health.ocr 仍 available（服务在线，只是内部报错）",
              st5b == 200 and health5.get("ocr") == "available", str(health5)[:150])
        STUB_MODE["mode"] = "success_false"

        # 上游挂掉 → 503，且后端仍健康
        stub.shutdown()
        time.sleep(0.3)
        st3, body3 = recognize(token, "sync")
        check("stub 停掉后 → 503 OCR_UNAVAILABLE",
              st3 == 503 and body3.get("error", {}).get("code") == "OCR_UNAVAILABLE",
              f"status={st3} body={str(body3)[:250]}")
        st4, health2 = req("GET", "/api/health")
        check("上游挂掉后后端仍健康", st4 == 200 and health2.get("db") == "ok", str(health2)[:150])
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
        try:
            stub.shutdown()
        except Exception:
            pass
        shutil.rmtree(DATA_DIR, ignore_errors=True)

    print(f"\n===== ocr-failmode: {PASS} PASS / {FAIL} FAIL =====")
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
