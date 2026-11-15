# -*- coding: utf-8 -*-
"""以独立数据目录运行 smoke.py（绝不触碰 data/ledger.db）。

smoke.py 里的 DB/BASE 与 os.path.join("data", ...) 都是硬编码，
这里在内存中覆写（不改 smoke.py 源码），断言逻辑保持完全一致。
"""
import importlib.util
import os
import sys

QA_DATA = os.environ.get("QA_DATA_DIR", "data-qa")
QA_DB = os.environ.get("QA_DB", os.path.join(QA_DATA, "ledger.db"))
QA_BASE = os.environ.get("QA_BASE", "http://127.0.0.1:8770")


class _PathShim:
    """把 os.path.join("data", ...) 重定向到 QA 数据目录。"""

    def __getattr__(self, name):
        return getattr(os.path, name)

    def join(self, *parts):
        parts = list(parts)
        if parts and parts[0] in ("data", "data/"):
            parts[0] = QA_DATA
        return os.path.join(*parts)


class _OsShim:
    def __getattr__(self, name):
        return getattr(os, name)

    path = _PathShim()


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    spec = importlib.util.spec_from_file_location("smoke", os.path.join(here, "smoke.py"))
    mod = importlib.util.module_from_spec(spec)
    sys.modules["smoke"] = mod
    spec.loader.exec_module(mod)
    mod.DB = QA_DB
    mod.BASE = QA_BASE
    mod.os = _OsShim()
    print(f"[smoke-qa] data_dir={QA_DATA} db={QA_DB} base={QA_BASE}")
    return mod.main()


if __name__ == "__main__":
    sys.exit(main())
