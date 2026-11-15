"""请求/响应 pydantic 模型。

识别结果字段与《项目计划书》PRD 7.3 完全一致：
    id, source, timestamp, account, transaction_type, amount, category,
    sub_category, merchant, region, tags, remark, confidence, raw_text,
    model_used, need_review
同时提供后端契约（ARCHITECTURE §5.8）需要的扩展字段：
    transaction_time, amount_cents, account_id, category_id, merchant_id, warnings
（amount 为元浮点，amount_cents 为整数分；transaction_type 取值为
 ARCHITECTURE §10.4 枚举 expense/income/transfer。）
"""

from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, Field, model_validator

Hint = Literal["wechat", "alipay", "receipt"]
Mode = Literal["sync", "async"]


class RecognizeRequest(BaseModel):
    """POST /api/v1/recognize 请求体。

    image_path 与 image_base64 二选一：
    - image_path：绝对路径（Go 后端传入其落盘的截图路径，本服务只读）
    - image_base64：不带 data: 前缀的裸 base64
    model: "auto|heavy|light|mock"，或显式引擎名（如 "ollama:qwen2.5vl:7b"）
    """

    image_path: str | None = None
    image_base64: str | None = None
    model: str = "auto"
    mode: Mode = "sync"
    hint: Hint = "receipt"

    @model_validator(mode="after")
    def _check_image(self) -> "RecognizeRequest":
        if not self.image_path and not self.image_base64:
            raise ValueError("image_path 与 image_base64 必须提供其一")
        return self


class BatchImageItem(BaseModel):
    """批量识别的单张图片描述。"""

    image_path: str | None = None
    image_base64: str | None = None
    hint: Hint = "receipt"


class BatchRequest(BaseModel):
    """POST /api/v1/recognize/batch（兼容 /api/v1/recognize-batch）请求体。

    images 支持两种形态：字符串列表（纯路径）或对象列表（含 hint）。
    """

    images: list[str | BatchImageItem] = Field(min_length=1)
    model: str = "auto"


class SyncResponse(BaseModel):
    """同步识别响应（PRD 7.4 sync 形态）。"""

    success: bool = True
    data: dict[str, Any]
    model_used: str
    elapsed_ms: int
    task_id: str | None = None
    status: Literal["done"] = "done"


class AsyncResponse(BaseModel):
    """异步识别响应（PRD 7.4 async 形态）。"""

    success: bool = True
    task_id: str
    status: Literal["pending", "processing"] = "pending"


class BatchResponse(BaseModel):
    """批量识别响应：立即返回 task_ids（并发度 1，逐个排队）。"""

    success: bool = True
    task_ids: list[str]
    count: int


class TaskView(BaseModel):
    """GET /api/v1/tasks/{task_id} 响应。"""

    task_id: str
    status: Literal["pending", "processing", "done", "failed"]
    progress: int = 0
    result: dict[str, Any] | None = None
    error: str | None = None
    elapsed_ms: int | None = None
    engine: str | None = None
    model_used: str | None = None


class EngineInfo(BaseModel):
    """GET /api/v1/model 中 available 数组元素。"""

    name: str
    engine: str
    loaded: bool
    params: str = ""


class ModelView(BaseModel):
    """GET /api/v1/model 响应。"""

    current: str
    available: list[EngineInfo]
    healthy: bool
    stats: dict[str, Any]


class SwitchRequest(BaseModel):
    """POST /api/v1/model/switch 请求体。"""

    name: str
    unload_first: bool = True


class SwitchResponse(BaseModel):
    """POST /api/v1/model/switch 响应。"""

    ok: bool
    current: str
    error: str | None = None


class ErrorBody(BaseModel):
    """统一错误体（与 ARCHITECTURE §5.0 形态对齐）。"""

    error: dict[str, Any]
