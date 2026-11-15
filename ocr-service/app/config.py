"""全局配置：模型名/地址/超时/并发/日志级别，全部支持环境变量覆盖。

环境变量清单（详见 ocr-service/README.md）：
- OCR_HOST / OCR_PORT           监听地址（默认 127.0.0.1 / 8771，仅信息展示）
- OCR_DEFAULT_ENGINE            启动默认引擎（默认 "mock"）
- OLLAMA_BASE_URL               Ollama 端点（默认 http://127.0.0.1:11434）
- OLLAMA_DEFAULT_MODEL          ollama 引擎默认模型（默认 qwen2.5vl:7b）
- OPENAI_BASE_URL               OpenAI 兼容端点（默认 http://127.0.0.1:1234/v1）
- OPENAI_DEFAULT_MODEL          openai_vl 引擎默认模型（默认 qwen2-vl-7b-instruct）
- OPENAI_API_KEY                OpenAI 兼容端点的 API Key（可选）
- OCR_TIMEOUT_S                 单次模型调用超时（默认 120s）
- OCR_LOAD_TIMEOUT_S            引擎 load/健康检查超时（默认 5s）
- OCR_TASK_TTL_S                任务表 TTL（默认 3600s = 1h）
- OCR_MAX_SIDE                  预处理长边上限（默认 2048）
- OCR_JPEG_QUALITY              预处理 JPEG 质量（默认 88）
- OCR_MAX_IMAGE_BYTES           图片大小上限（默认 20MB）
- OCR_STATS_WINDOW              性能统计滑动窗口（默认 1000）
- LOG_LEVEL                     日志级别（默认 INFO）
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field


def _env_str(key: str, default: str) -> str:
    value = os.environ.get(key, "").strip()
    return value if value else default


def _env_int(key: str, default: int) -> int:
    raw = os.environ.get(key, "").strip()
    try:
        return int(raw) if raw else default
    except ValueError:
        return default


@dataclass(frozen=True)
class Settings:
    """不可变配置对象，进程内单例（见 deps.py）。"""

    host: str = field(default_factory=lambda: _env_str("OCR_HOST", "127.0.0.1"))
    port: int = field(default_factory=lambda: _env_int("OCR_PORT", 8771))
    default_engine: str = field(default_factory=lambda: _env_str("OCR_DEFAULT_ENGINE", "mock"))
    ollama_base_url: str = field(default_factory=lambda: _env_str("OLLAMA_BASE_URL", "http://127.0.0.1:11434"))
    ollama_default_model: str = field(default_factory=lambda: _env_str("OLLAMA_DEFAULT_MODEL", "qwen2.5vl:7b"))
    openai_base_url: str = field(default_factory=lambda: _env_str("OPENAI_BASE_URL", "http://127.0.0.1:1234/v1"))
    openai_default_model: str = field(default_factory=lambda: _env_str("OPENAI_DEFAULT_MODEL", "qwen2-vl-7b-instruct"))
    openai_api_key: str = field(default_factory=lambda: os.environ.get("OPENAI_API_KEY", ""))
    timeout_s: float = field(default_factory=lambda: float(_env_int("OCR_TIMEOUT_S", 120)))
    load_timeout_s: float = field(default_factory=lambda: float(_env_int("OCR_LOAD_TIMEOUT_S", 5)))
    task_ttl_s: int = field(default_factory=lambda: _env_int("OCR_TASK_TTL_S", 3600))
    max_side: int = field(default_factory=lambda: _env_int("OCR_MAX_SIDE", 2048))
    jpeg_quality: int = field(default_factory=lambda: _env_int("OCR_JPEG_QUALITY", 88))
    max_image_bytes: int = field(default_factory=lambda: _env_int("OCR_MAX_IMAGE_BYTES", 20 * 1024 * 1024))
    stats_window: int = field(default_factory=lambda: _env_int("OCR_STATS_WINDOW", 1000))
    log_level: str = field(default_factory=lambda: _env_str("LOG_LEVEL", "INFO"))

    @property
    def endpoint(self) -> str:
        """对外展示的服务端点。"""
        return f"http://{self.host}:{self.port}"


_settings: Settings | None = None


def get_settings() -> Settings:
    """返回配置单例（惰性初始化，测试可用环境变量后重置）。"""
    global _settings
    if _settings is None:
        _settings = Settings()
    return _settings


def reset_settings() -> None:
    """重置配置单例（仅测试使用）。"""
    global _settings
    _settings = None
