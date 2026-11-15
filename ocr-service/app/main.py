"""FastAPI 装配 + lifespan 预热（ARCHITECTURE §8.2 启动策略）。

启动顺序：
1. 加载 MockEngine（ready=true，Go 端 /api/ocr/model 永远可用）；
2. 按 OCR_DEFAULT_ENGINE 尝试加载真实引擎，失败只打 warn，不阻止启动；
3. 启动串行识别 worker（asyncio.Queue，并发度 1）。

关闭顺序：停 worker → unload 当前引擎。

本服务不写数据库、不写文件（仅按绝对路径只读图片）；
除配置的模型端点（Ollama / OpenAI 兼容）外不发任何网络请求。
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import AsyncIterator

from fastapi import FastAPI

from app import __version__
from app.config import get_settings
from app.deps import get_recognize_queue, get_registry, get_task_store
from app.pipeline.preprocess import ImageLoadError
from app.routers import health, model, recognize, tasks

logging.basicConfig(
    level=getattr(logging, get_settings().log_level.upper(), logging.INFO),
    format="%(asctime)s %(levelname)s [ocr-service] %(name)s: %(message)s",
)
logger = logging.getLogger("app.main")


async def _worker() -> None:
    """串行识别 worker：并发度 1（避免多任务并发抢显存）。

    单个任务失败只标记 failed，worker 本身永不退出。
    """
    queue = get_recognize_queue()
    store = get_task_store()
    while True:
        payload = await queue.get()
        task_id = payload.get("task_id", "")
        try:
            await store.update(task_id, status="processing", progress=10)
            outcome = await recognize.run_recognition(
                payload.get("image_path"),
                payload.get("image_base64"),
                payload.get("model", "auto"),
                payload.get("hint", "receipt"),
            )
            await store.update(
                task_id,
                status="done",
                progress=100,
                result=outcome["result"],
                elapsed_ms=outcome["elapsed_ms"],
                model_used=outcome["model_used"],
                engine=outcome["model_used"],
            )
        except ImageLoadError as exc:
            await store.update(task_id, status="failed", error=f"INVALID_IMAGE: {exc}", progress=100)
        except Exception as exc:  # noqa: BLE001 —— worker 必须吞掉一切异常
            logger.exception("任务 %s 处理失败", task_id)
            await store.update(task_id, status="failed", error=str(exc), progress=100)
        finally:
            queue.task_done()


@contextlib.asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """预热引擎并启动 worker。"""
    settings = get_settings()
    logger.info(
        "OCR 服务启动：%s（default_engine=%s, task_ttl=%ss）",
        settings.endpoint,
        settings.default_engine,
        settings.task_ttl_s,
    )
    await get_registry().startup()
    worker_task = asyncio.create_task(_worker())
    logger.info("就绪：engine=%s", get_registry().current_name())
    try:
        yield
    finally:
        worker_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await worker_task
        await get_registry().shutdown()
        logger.info("OCR 服务已关闭")


app = FastAPI(
    title="Ledger OCR Service",
    version=__version__,
    description="个人记账软件 v1.0 —— 本地 OCR 推理服务（引擎抽象 + 热切换 + mock 降级）",
    lifespan=lifespan,
)

app.include_router(health.router)
app.include_router(recognize.router)
app.include_router(tasks.router)
app.include_router(model.router)
