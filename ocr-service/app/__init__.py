"""OCR 服务包。

模块划分（与 docs/ARCHITECTURE.md §2.3 / §8 一致）：
- config.py     全局配置（环境变量覆盖）
- schemas.py    请求/响应 pydantic 模型
- deps.py       引擎注册表 / 任务表 / 队列的单例注入
- main.py       FastAPI 装配 + lifespan 预热
- engines/      引擎抽象（base/registry/mock/ollama/openai_vl）
- pipeline/     preprocess / prompt / postprocess / rules
- routers/      health / recognize / tasks / model
- store/        内存任务表（TTL 1h）
"""

__version__ = "1.0.0"
