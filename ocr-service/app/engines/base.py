"""引擎抽象基类（ARCHITECTURE §8.2）。

所有引擎必须实现 load/unload/health/recognize 四个协程；
recognize 返回与 PRD 7.3 一致的结构化 dict（字段规整交给 pipeline/postprocess）。
"""

from __future__ import annotations

from abc import ABC, abstractmethod

from PIL import Image


class EngineError(RuntimeError):
    """引擎调用失败（网络/超时/模型不存在等），可触发回落 mock。"""


class EngineUnavailableError(EngineError):
    """引擎端点不可达，load() 或 health() 失败时抛出。"""


class BaseEngine(ABC):
    """OCR 引擎抽象。

    Attributes:
        name: 引擎全名（如 "mock" / "ollama:qwen2.5vl:7b" /
             "openai:http://127.0.0.1:1234/v1/qwen2-vl-7b"）。
    """

    name: str = "base"

    @abstractmethod
    async def load(self) -> None:
        """加载引擎资源；不可达时抛 EngineUnavailableError。"""

    @abstractmethod
    async def unload(self) -> None:
        """释放引擎资源（幂等）。"""

    @abstractmethod
    async def health(self) -> bool:
        """端点/资源是否可用（短超时探测）。"""

    @abstractmethod
    async def recognize(self, image: Image.Image, hint: str) -> dict:
        """识别单张图片。

        Args:
            image: 预处理后的 RGB PIL 图像（附带 ocr_text 属性时为
                   PNG 文本元数据，供 mock 引擎确定性测试）。
            hint: wechat / alipay / receipt。

        Returns:
            与 PRD 7.3 字段一致的结构化 dict（允许字段缺失，由
            postprocess 规整补 null）。

        Raises:
            EngineError: 调用失败，由 registry 决定是否回落 mock。
        """
