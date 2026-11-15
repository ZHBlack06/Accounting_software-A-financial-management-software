"""Ollama 引擎：POST {base}/api/chat 多模态调用（ARCHITECTURE §8.2）。

请求体：{model, messages:[{role, content, images:[b64]}], stream:false, format:"json"}
超时 OCR_TIMEOUT_S（默认 120s），失败重试 1 次；仍失败抛 EngineError。
"""

from __future__ import annotations

import base64
import logging

import httpx
from PIL import Image

from app.config import get_settings
from app.engines.base import BaseEngine, EngineError, EngineUnavailableError
from app.pipeline.prompt import build_messages

logger = logging.getLogger(__name__)


class OllamaEngine(BaseEngine):
    """通过 Ollama /api/chat 调用多模态模型（如 qwen2.5vl:7b）。"""

    def __init__(self, model: str | None = None, base_url: str | None = None) -> None:
        settings = get_settings()
        self.model = model or settings.ollama_default_model
        self.base_url = (base_url or settings.ollama_base_url).rstrip("/")
        self.name = f"ollama:{self.model}"
        self._client: httpx.AsyncClient | None = None

    async def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None or self._client.is_closed:
            settings = get_settings()
            self._client = httpx.AsyncClient(timeout=settings.timeout_s)
        return self._client

    async def load(self) -> None:
        """探测 Ollama 端点；不可达或模型不存在抛 EngineUnavailableError。"""
        settings = get_settings()
        try:
            async with httpx.AsyncClient(timeout=settings.load_timeout_s) as client:
                response = await client.get(f"{self.base_url}/api/tags")
                response.raise_for_status()
                data = response.json()
                models = {str(item.get("name", "")) for item in data.get("models", [])}
                # 模型不在列表 → 视为加载失败（switch 返回 ok:false，保留当前引擎）
                if models and self.model not in models and not any(
                    name.startswith(self.model + ":") for name in models
                ):
                    raise EngineUnavailableError(
                        f"Ollama 端点 {self.base_url} 未找到模型 {self.model}（可用：{sorted(models)}）"
                    )
        except httpx.HTTPError as exc:
            raise EngineUnavailableError(f"Ollama 端点不可达 {self.base_url}: {exc}") from exc
        await self._ensure_client()

    async def unload(self) -> None:
        if self._client is not None and not self._client.is_closed:
            await self._client.aclose()
        self._client = None

    async def health(self) -> bool:
        settings = get_settings()
        try:
            async with httpx.AsyncClient(timeout=settings.load_timeout_s) as client:
                response = await client.get(f"{self.base_url}/api/version")
                return response.status_code == 200
        except httpx.HTTPError:
            return False

    async def _chat_once(self, image_bytes: bytes, hint: str) -> str:
        client = await self._ensure_client()
        b64 = base64.b64encode(image_bytes).decode("ascii")
        messages = build_messages(hint)
        for message in messages:
            if message["role"] == "user":
                message["images"] = [b64]
        payload = {
            "model": self.model,
            "messages": messages,
            "stream": False,
            "format": "json",
        }
        response = await client.post(f"{self.base_url}/api/chat", json=payload)
        response.raise_for_status()
        data = response.json()
        content = (data.get("message") or {}).get("content", "")
        if not content:
            raise EngineError(f"Ollama 返回空 content（model={self.model}）")
        return content

    async def recognize(self, image: Image.Image, hint: str) -> dict:
        """调用 Ollama 识别；失败重试 1 次，仍失败抛 EngineError。"""
        from app.pipeline.preprocess import image_to_jpeg_bytes

        image_bytes = image_to_jpeg_bytes(image)
        last_error: Exception | None = None
        for attempt in (1, 2):
            try:
                content = await self._chat_once(image_bytes, hint)
                return {
                    "raw_text": content,
                    "model_used": self.name,
                    "payload": content,  # 原始输出交由 postprocess 解析
                }
            except (httpx.HTTPError, EngineError) as exc:
                last_error = exc
                logger.warning("Ollama 调用失败（第 %s 次）：%s", attempt, exc)
                # 重建客户端以防连接失效
                await self.unload()
        raise EngineError(f"Ollama 调用失败（已重试 1 次）: {last_error}")
