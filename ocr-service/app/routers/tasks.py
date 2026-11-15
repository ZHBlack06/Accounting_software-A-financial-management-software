"""任务查询端点：GET /api/v1/tasks/{task_id}。"""

from __future__ import annotations

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from app.deps import get_task_store

router = APIRouter()


@router.get("/api/v1/tasks/{task_id}")
async def get_task(task_id: str) -> JSONResponse:
    """查询任务状态：pending→processing→done；未知/过期（TTL 1h）→ 404。"""
    task = await get_task_store().get(task_id)
    if task is None:
        return JSONResponse(
            status_code=404,
            content={"error": {"code": "TASK_NOT_FOUND", "message": f"任务不存在或已过期：{task_id}"}},
        )
    return JSONResponse(status_code=200, content=get_task_store().to_view(task))
