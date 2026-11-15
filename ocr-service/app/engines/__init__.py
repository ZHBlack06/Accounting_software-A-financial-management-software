"""app.engines —— OCR 引擎抽象与实现。"""

from app.engines.base import BaseEngine, EngineError, EngineUnavailableError
from app.engines.mock import MockEngine

__all__ = ["BaseEngine", "EngineError", "EngineUnavailableError", "MockEngine"]
