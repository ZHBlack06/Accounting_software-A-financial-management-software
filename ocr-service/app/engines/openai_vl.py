"""OpenAI 兼容引擎：POST {base}/v1/chat/completions（vLLM / LM Studio 等）。

image_url 使用 data:image/jpeg;base64,... 内联传递；
端点不可达时由 registry 回落 mock。
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


class OpenAIVLEngine(BaseEngine):
    """调用 OpenAI 兼容 /v1/chat/completions 的多模态视觉模型。"""

    def __init__(
        self,
        model: str | None = None,
        base_url: str | None = None,
        api_key: str | None = None,
    ) -> None:
        settings = get_settings()
        self.base_url = (base_url or settings.openai_base_url).rstrip("/")
        self.model = model or settings.openai_default_model
        self.api_key = api_key if api_key is not None else settings.openai_api_key
        # 兼容 "openai:<base>/<model>" 形态：斜杠后最后一段为模型名
        self.name = f"openai:{self.base_url}/{self.model}"
        self._client: httpx.AsyncClient | None = None

    def _headers(self) -> dict[str, str]:
        headers = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        return headers

    async def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None or self._client.is_closed:
            settings = get_settings()
            self._client = httpx.AsyncClient(timeout=settings.timeout_s)
        return self._client

    async def load(self) -> None:
        """探测 /v1/models；不可达抛 EngineUnavailableError。"""
        settings = get_settings()
        try:
            async with httpx.AsyncClient(timeout=settings.load_timeout_s) as client:
                response = await client.get(f"{self.base_url}/models", headers=self._headers())
                if response.status_code in (401, 403):
                    logger.warning("OpenAI 兼容端点鉴权失败（可能需要 OPENAI_API_KEY）")
                response.raise_for_status()
        except httpx.HTTPError as exc:
            raise EngineUnavailableError(f"OpenAI 兼容端点不可达 {self.base_url}: {exc}") from exc
        await self._ensure_client()

    async def unload(self) -> None:
        if self._client is not None and not self._client.is_closed:
            await self._client.aclose()
        self._client = None

    async def health(self) -> bool:
        settings = get_settings()
        try:
            async with httpx.AsyncClient(timeout=settings.load_timeout_s) as client:
                response = await client.get(f"{self.base_url}/models", headers=self._headers())
                return response.status_code == 200
        except httpx.HTTPError:
            return False

    async def recognize(self, image: Image.Image, hint: str) -> dict:
        """调用 /v1/chat/completions 识别；失败重试 1 次。"""
        from app.pipeline.preprocess import image_to_jpeg_bytes

        image_bytes = image_to_jpeg_bytes(image)
        b64 = base64.b64encode(image_bytes).decode("ascii")
        data_uri = f"data:image/jpeg;base64,{b64}"

        messages: list[dict] = []
        for message in build_messages(hint):
            if message["role"] == "user":
                messages.append({
                    "role": "user",
                    "content": [
                        {"type": "text", "text": message["content"]},
                        {"type": "image_url", "image_url": {"url": data_uri}},
                    ],
                })
            else:
                messages.append({"role": message["role"], "content": message["content"]})
        payload = {
            "model": self.model,
            "messages": messages,
            "temperature": 0.1,
            "stream": False,
        }

        last_error: Exception | None = None
        for attempt in (1, 2):
            try:
                client = await self._ensure_client()
                response = await client.post(
                    f"{self.base_url}/chat/completions",
                    json=payload,
                    headers=self._headers(),
                )
                response.raise_for_status()
                data = response.json()
                choices = data.get("choices") or []
                content = (choices[0].get("message") or {}).get("content", "") if choices else ""
                if not content:
                    raise EngineError(f"OpenAI 兼容端点返回空 content（model={self.model}）")
                return {
                    "raw_text": content,
                    "model_used": self.name,
                    "payload": content,  # 原始输出交由 postprocess 解析
                }
            except (httpx.HTTPError, EngineError) as exc:
                last_error = exc
                logger.warning("OpenAI 兼容调用失败（第 %s 次）：%s", attempt, exc)
                await self.unload()
        raise EngineError(f"OpenAI 兼容调用失败（已重试 1 次）: {last_error}")
