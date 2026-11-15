"""引擎注册表 / 任务表 / 识别队列的进程级单例与 FastAPI 依赖注入。"""

from __future__ import annotations

import asyncio

from app.engines.registry import EngineRegistry
from app.store.tasks import TaskStore

# 进程级单例（--workers 1，模型常驻内存，见 ARCHITECTURE §8.1）
registry = EngineRegistry()
task_store = TaskStore()
# 识别队列：并发度 1（避免多任务并发抢显存）。
# 注意：asyncio.Queue 必须在事件循环内创建（Python 3.13 严格校验），
# 因此在 lifespan startup 时惰性创建。
recognize_queue: asyncio.Queue | None = None


def get_registry() -> EngineRegistry:
    return registry


def get_task_store() -> TaskStore:
    return task_store


def get_recognize_queue() -> asyncio.Queue:
    """返回识别队列（lifespan 启动后可用）。"""
    global recognize_queue
    if recognize_queue is None:
        recognize_queue = asyncio.Queue()
    return recognize_queue
