"""识别端点：POST /api/v1/recognize、/api/v1/recognize/batch（兼容 -batch）。"""

from __future__ import annotations

import logging
import time
from typing import Any

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from app.deps import get_recognize_queue, get_registry, get_task_store
from app.pipeline.postprocess import normalize_result
from app.pipeline.preprocess import ImageLoadError, load_image, preprocess
from app.pipeline.rules import validate
from app.schemas import (
    AsyncResponse,
    BatchRequest,
    BatchResponse,
    RecognizeRequest,
    SyncResponse,
)
from app.store.tasks import TaskStore

logger = logging.getLogger(__name__)
router = APIRouter()


def _error(status: int, code: str, message: str, details: dict[str, Any] | None = None) -> JSONResponse:
    """统一错误体：{"error": {"code", "message", "details"}}。"""
    body: dict[str, Any] = {"error": {"code": code, "message": message}}
    if details:
        body["error"]["details"] = details
    return JSONResponse(status_code=status, content=body)


async def run_recognition(
    image_path: str | None,
    image_base64: str | None,
    model: str,
    hint: str,
) -> dict[str, Any]:
    """完整识别流水线：preprocess → engine → postprocess → rules。

    Returns:
        {"result": 最终结构化 dict, "elapsed_ms": int, "model_used": str}

    Raises:
        ImageLoadError: 图片加载失败（路由层转 400）。
    """
    started = time.perf_counter()
    image = preprocess(load_image(path=image_path, b64=image_base64))
    force_mock = model == "mock"
    engine_output, extra_warnings = await get_registry().recognize_with_fallback(
        image, hint, force_mock=force_mock
    )
    result = normalize_result(engine_output, extra_warnings)
    rule_warnings, rule_need_review = validate(result)
    if rule_warnings:
        result["warnings"] = list(result.get("warnings") or []) + rule_warnings
    if rule_need_review:
        result["need_review"] = True
    elapsed_ms = int((time.perf_counter() - started) * 1000)
    return {"result": result, "elapsed_ms": elapsed_ms, "model_used": result["model_used"]}


@router.post("/api/v1/recognize")
async def recognize(request: RecognizeRequest) -> JSONResponse:
    """单张识别：mode=sync 直接返回结果；mode=async 返回 task_id（202）。"""
    if request.mode == "sync":
        try:
            outcome = await run_recognition(
                request.image_path, request.image_base64, request.model, request.hint
            )
        except ImageLoadError as exc:
            return _error(400, "INVALID_IMAGE", str(exc))
        body = SyncResponse(
            data=outcome["result"],
            model_used=outcome["model_used"],
            elapsed_ms=outcome["elapsed_ms"],
        )
        return JSONResponse(status_code=200, content=body.model_dump())

    # async：建任务并入队（并发度 1，由 worker 串行消费）
    task = await get_task_store().create()
    await get_recognize_queue().put({
        "task_id": task.task_id,
        "image_path": request.image_path,
        "image_base64": request.image_base64,
        "model": request.model,
        "hint": request.hint,
    })
    body = AsyncResponse(task_id=task.task_id, status="pending")
    return JSONResponse(status_code=202, content=body.model_dump())


async def _batch_impl(request: BatchRequest) -> JSONResponse:
    """批量识别：全部走异步任务（串行队列），立即返回 task_ids。"""
    task_ids: list[str] = []
    for index, item in enumerate(request.images):
        if isinstance(item, str):
            image_path, image_base64, hint = item, None, "receipt"
        else:
            image_path, image_base64, hint = item.image_path, item.image_base64, item.hint
        task = await get_task_store().create()
        task_ids.append(task.task_id)
        await get_recognize_queue().put({
            "task_id": task.task_id,
            "image_path": image_path,
            "image_base64": image_base64,
            "model": request.model,
            "hint": hint,
            "batch_index": index,
        })
    body = BatchResponse(task_ids=task_ids, count=len(task_ids))
    return JSONResponse(status_code=202, content=body.model_dump())


@router.post("/api/v1/recognize/batch")
async def recognize_batch(request: BatchRequest) -> JSONResponse:
    """批量识别（ARCHITECTURE §8.1 路径）。"""
    return await _batch_impl(request)


@router.post("/api/v1/recognize-batch")
async def recognize_batch_alias(request: BatchRequest) -> JSONResponse:
    """批量识别（兼容短横线路径，供 Go 端 §5.8 端点 57 代理）。"""
    return await _batch_impl(request)
