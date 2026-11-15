"""模型管理端点：GET /api/v1/model、POST /api/v1/model/switch。"""

from __future__ import annotations

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from app.deps import get_registry
from app.schemas import EngineInfo, ModelView, SwitchRequest, SwitchResponse

router = APIRouter()


@router.get("/api/v1/model")
async def get_model() -> JSONResponse:
    """当前引擎 + 可用引擎列表 + 累计性能统计（PRD 7.2 性能监控）。"""
    registry = get_registry()
    view = ModelView(
        current=registry.current_name(),
        available=[EngineInfo(**item) for item in registry.available()],
        healthy=await registry.healthy(),
        stats=registry.stats(),
    )
    return JSONResponse(status_code=200, content=view.model_dump())


@router.post("/api/v1/model/switch")
async def switch_model(request: SwitchRequest) -> JSONResponse:
    """热切换引擎。失败时保留当前引擎（服务不崩）。"""
    registry = get_registry()
    result = await registry.switch(request.name, unload_first=request.unload_first)
    body = SwitchResponse(
        ok=result["ok"],
        current=result["current"],
        error=result.get("error"),
    )
    return JSONResponse(status_code=200, content=body.model_dump())
