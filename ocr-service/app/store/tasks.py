"""内存任务表（ARCHITECTURE §8.4）：dict[task_id, Task] + TTL 1h。

状态机：pending → processing → done / failed；未知或过期任务返回 None（路由层 404）。
本服务不写任何文件，任务仅存活在内存中。
"""

from __future__ import annotations

import asyncio
import time
import uuid
from dataclasses import dataclass, field
from typing import Any

from app.config import get_settings


@dataclass
class Task:
    """一次异步识别任务。"""

    task_id: str
    status: str = "pending"  # pending | processing | done | failed
    progress: int = 0
    result: dict[str, Any] | None = None
    error: str | None = None
    elapsed_ms: int | None = None
    engine: str | None = None
    model_used: str | None = None
    created_at: float = field(default_factory=time.time)
    updated_at: float = field(default_factory=time.time)


class TaskStore:
    """线程/协程安全的内存任务表（TTL 清理惰性执行）。"""

    def __init__(self, ttl_s: int | None = None) -> None:
        self._tasks: dict[str, Task] = {}
        self._lock = asyncio.Lock()
        self._ttl = ttl_s

    def _ttl_seconds(self) -> float:
        return float(self._ttl if self._ttl is not None else get_settings().task_ttl_s)

    async def create(self) -> Task:
        """创建 pending 任务。"""
        task_id = f"ocr_{uuid.uuid4().hex[:8]}"
        task = Task(task_id=task_id)
        async with self._lock:
            self._purge_locked()
            self._tasks[task_id] = task
        return task

    async def get(self, task_id: str) -> Task | None:
        """查询任务；不存在或已过期（超过 TTL）返回 None。"""
        async with self._lock:
            self._purge_locked()
            return self._tasks.get(task_id)

    async def update(self, task_id: str, **kwargs: Any) -> Task | None:
        """更新任务字段。"""
        async with self._lock:
            task = self._tasks.get(task_id)
            if task is None:
                return None
            for key, value in kwargs.items():
                if hasattr(task, key):
                    setattr(task, key, value)
            task.updated_at = time.time()
            return task

    async def size(self) -> int:
        async with self._lock:
            self._purge_locked()
            return len(self._tasks)

    def _purge_locked(self) -> None:
        """删除超过 TTL 的任务（调用方需持有锁）。"""
        now = time.time()
        ttl = self._ttl_seconds()
        expired = [tid for tid, task in self._tasks.items() if now - task.created_at > ttl]
        for tid in expired:
            del self._tasks[tid]

    def to_view(self, task: Task) -> dict[str, Any]:
        """Task → API 视图 dict。"""
        return {
            "task_id": task.task_id,
            "status": task.status,
            "progress": task.progress,
            "result": task.result,
            "error": task.error,
            "elapsed_ms": task.elapsed_ms,
            "engine": task.engine,
            "model_used": task.model_used,
        }
