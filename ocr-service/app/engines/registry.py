"""引擎注册表：名称→引擎工厂 + 当前引擎 + 切换锁 + 累计统计。

工厂语法（ARCHITECTURE §8.2）：
- "mock"                            → MockEngine
- "ollama"                          → OllamaEngine（用 OLLAMA_DEFAULT_MODEL）
- "ollama:<model>"                  → OllamaEngine(model)
- "ollama:<model>@<base_url>"       → OllamaEngine(model, base_url)
- "openai"                          → OpenAIVLEngine（用 OPENAI_* 配置）
- "openai:<model>"                  → OpenAIVLEngine(model)
- "openai:<base_url>/<model>"       → OpenAIVLEngine(model, base_url)

切换语义：先构建并 load 新引擎，成功后才 unload 旧引擎并替换；
load 失败保留当前引擎（返回 ok:false），服务永不崩溃。
"""

from __future__ import annotations

import asyncio
import logging
import time

from PIL import Image

from app.config import get_settings
from app.engines.base import BaseEngine, EngineError, EngineUnavailableError
from app.engines.mock import MockEngine

logger = logging.getLogger(__name__)


class EngineRegistry:
    """进程内唯一的引擎注册表（切换期间 recognize 阻塞等待）。"""

    def __init__(self) -> None:
        self._current: BaseEngine | None = None
        self._fallback: MockEngine | None = None
        self._lock = asyncio.Lock()
        # 累计统计（滑动窗口 OCR_STATS_WINDOW 条）
        self._count = 0
        self._fail_count = 0
        self._total_ms = 0.0
        self._latencies: list[float] = []

    # ---- 生命周期 -------------------------------------------------------

    async def startup(self) -> None:
        """启动策略：先加载 mock（ready=true），再按 OCR_DEFAULT_ENGINE 尝试真实引擎。"""
        self._fallback = MockEngine()
        await self._fallback.load()
        self._current = self._fallback
        default_engine = get_settings().default_engine
        if default_engine and default_engine != "mock":
            result = await self.switch(default_engine)
            if not result["ok"]:
                logger.warning("默认引擎 %s 加载失败，保留 mock：%s", default_engine, result.get("error"))
            else:
                logger.info("默认引擎已加载：%s", result["current"])

    async def shutdown(self) -> None:
        if self._current is not None:
            await self._current.unload()
        if self._fallback is not None:
            await self._fallback.unload()

    # ---- 工厂 -----------------------------------------------------------

    def build(self, name: str) -> BaseEngine:
        """按名称构建引擎实例（不 load）。未知名称抛 EngineError。"""
        spec = (name or "").strip()
        if not spec:
            raise EngineError("引擎名称为空")
        if spec == "mock":
            return MockEngine()
        if spec.startswith("ollama"):
            from app.engines.ollama import OllamaEngine

            rest = spec[len("ollama"):].lstrip(":")
            model: str | None = None
            base_url: str | None = None
            if rest:
                if "@" in rest:
                    model, base_url = rest.split("@", 1)
                else:
                    model = rest
            return OllamaEngine(model=model or None, base_url=base_url)
        if spec.startswith("openai"):
            from app.engines.openai_vl import OpenAIVLEngine

            rest = spec[len("openai"):].lstrip(":")
            model: str | None = None
            base_url: str | None = None
            if rest:
                if rest.startswith("http://") or rest.startswith("https://"):
                    # openai:<base_url>/<model>
                    stripped = rest.rstrip("/")
                    model = stripped.rsplit("/", 1)[-1] or None
                    base_url = stripped.rsplit("/", 1)[0] or None
                else:
                    model = rest
            return OpenAIVLEngine(model=model, base_url=base_url)
        raise EngineError(f"未知引擎名称：{spec}（支持 mock / ollama:<model> / openai:<base>/<model>）")

    # ---- 查询 -----------------------------------------------------------

    async def get(self) -> BaseEngine:
        """取当前引擎；未初始化时回落 mock。"""
        async with self._lock:
            if self._current is not None:
                return self._current
            if self._fallback is None:
                self._fallback = MockEngine()
                await self._fallback.load()
            return self._fallback

    def current_name(self) -> str:
        return self._current.name if self._current is not None else "mock"

    def available(self) -> list[dict]:
        """可用引擎列表（供 GET /api/v1/model）。"""
        settings = get_settings()
        items = [{"name": "mock", "engine": "mock", "loaded": True, "params": "regex-heuristic"}]
        ollama_name = f"ollama:{settings.ollama_default_model}"
        items.append({
            "name": ollama_name,
            "engine": "ollama",
            "loaded": self.current_name() == ollama_name,
            "params": settings.ollama_default_model,
        })
        openai_name = f"openai:{settings.openai_base_url}/{settings.openai_default_model}"
        items.append({
            "name": openai_name,
            "engine": "openai_vl",
            "loaded": self.current_name() == openai_name,
            "params": settings.openai_default_model,
        })
        return items

    def stats(self) -> dict:
        """累计性能统计：count / avg_ms / p95_ms / fail_rate。"""
        if self._count == 0:
            return {"count": 0, "avg_ms": 0.0, "p95_ms": 0.0, "fail_rate": 0.0}
        window = sorted(self._latencies)
        if window:
            idx = min(len(window) - 1, max(0, int(round(0.95 * (len(window) - 1)))))
            p95 = window[idx]
        else:
            p95 = 0.0
        total = self._total_ms + sum(x for x in window)
        avg = total / self._count
        return {
            "count": self._count,
            "avg_ms": round(avg, 2),
            "p95_ms": round(p95, 2),
            "fail_rate": round(self._fail_count / self._count, 4),
        }

    # ---- 统计记录 -------------------------------------------------------

    def record(self, elapsed_ms: float, ok: bool) -> None:
        self._count += 1
        if not ok:
            self._fail_count += 1
        else:
            self._latencies.append(elapsed_ms)
            self._total_ms += elapsed_ms
            limit = get_settings().stats_window
            if len(self._latencies) > limit:
                self._latencies = self._latencies[-limit:]

    # ---- 切换与识别 -----------------------------------------------------

    async def switch(self, name: str, unload_first: bool = True) -> dict:
        """热切换引擎。成功返回 {ok:true, current}；失败返回 {ok:false, current, error}。"""
        async with self._lock:
            try:
                engine = self.build(name)
                await engine.load()
            except (EngineError, EngineUnavailableError) as exc:
                logger.warning("切换到 %s 失败：%s", name, exc)
                return {"ok": False, "current": self.current_name(), "error": str(exc)}
            except Exception as exc:  # noqa: BLE001 —— 任何构建失败都不能拖垮服务
                logger.warning("切换到 %s 异常：%s", name, exc)
                return {"ok": False, "current": self.current_name(), "error": str(exc)}
            old = self._current
            self._current = engine
            if unload_first and old is not None and old is not engine:
                try:
                    await old.unload()
                except Exception as exc:  # noqa: BLE001
                    logger.warning("旧引擎 unload 失败（忽略）：%s", exc)
            logger.info("引擎已切换：%s → %s", old.name if old else "-", engine.name)
            return {"ok": True, "current": engine.name}

    async def recognize_with_fallback(
        self, image: Image.Image, hint: str, force_mock: bool = False
    ) -> tuple[dict, list[str]]:
        """识别 + 自动降级：当前引擎失败时回落 mock 并写 warning。

        Returns:
            (引擎原始输出 dict, 本次产生的额外 warnings)
        """
        engine = await self.get()
        if force_mock and engine.name != "mock":
            if self._fallback is None:
                self._fallback = MockEngine()
                await self._fallback.load()
            engine = self._fallback
        started = time.perf_counter()
        try:
            result = await engine.recognize(image, hint)
            self.record((time.perf_counter() - started) * 1000, True)
            return result, []
        except EngineError as exc:
            self.record((time.perf_counter() - started) * 1000, False)
            logger.warning("引擎 %s 识别失败，自动回落 mock：%s", engine.name, exc)
            if self._fallback is None:
                self._fallback = MockEngine()
                await self._fallback.load()
            started = time.perf_counter()
            result = await self._fallback.recognize(image, hint)
            self.record((time.perf_counter() - started) * 1000, True)
            return result, [f"模型不可用，已降级为 mock（{engine.name}: {exc}）"]

    async def healthy(self) -> bool:
        engine = await self.get()
        try:
            return await engine.health()
        except Exception:  # noqa: BLE001
            return False
