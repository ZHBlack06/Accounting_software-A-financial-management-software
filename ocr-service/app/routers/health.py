"""健康端点：GET /healthz、GET /readyz。"""

from __future__ import annotations

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from app.deps import get_registry

router = APIRouter()


@router.get("/healthz")
async def healthz() -> JSONResponse:
    """进程存活探测（不触发模型调用）。"""
    return JSONResponse(status_code=200, content={"status": "ok"})


@router.get("/readyz")
async def readyz() -> JSONResponse:
    """就绪探测：引擎是否加载完成（mock 兜底保证永远 ready）。"""
    registry = get_registry()
    healthy = await registry.healthy()
    return JSONResponse(
        status_code=200,
        content={
            "ready": True,
            "engine": registry.current_name(),
            "healthy": healthy,
            "models": [item["name"] for item in registry.available() if item.get("loaded")],
        },
    )
