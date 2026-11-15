# models/ —— 模型放置与热切换说明

**本目录不存放任何模型权重（不入 Git 仓库）**，仅作为放置说明。

## 为什么这里没有权重

本服务的真实识别引擎（`ollama` / `openai_vl`）都是**远程端点客户端**：
模型权重由 Ollama / vLLM / LM Studio 等推理框架自行管理，Python 服务只通过
httpx 调用其 HTTP API，因此 `requirements.txt` 中没有任何 torch/transformers
依赖，安装体积 < 60MB、启动 < 5s。

## 放置模型的方式

### Ollama（推荐，本机单卡/核显）

```powershell
ollama pull qwen2.5vl:7b        # 约 6GB，自动落入 Ollama 自己的模型目录
ollama list                      # 确认模型名
```

候选轻量/强能力模型（PRD 3.4）：
- 轻量级（0.8B–1B）：GLM-OCR（0.9B）、OvisOCR2（0.8B，GGUF 量化约 529MB）
- 强能力（2B–4B）：NuExtract3（4B）、Qianfan-OCR（4B）、FireRed-OCR（2B）、Qwen2.5-VL-7B

### vLLM / LM Studio（OpenAI 兼容端点）

```powershell
# vLLM 示例：以 OpenAI 兼容模式起服务（模型路径任意）
vllm serve Qwen/Qwen2-VL-7B-Instruct --port 8000

# LM Studio：在 GUI 中加载模型并开启本地服务器（默认 1234 端口）
```

## 热切换

模型加载后无需重启服务，调用 `POST /api/v1/model/switch` 即可：

```powershell
curl -X POST http://127.0.0.1:8771/api/v1/model/switch \
     -H "Content-Type: application/json" \
     -d '{"name":"ollama:qwen2.5vl:7b"}'
```

名称语法见 `ocr-service/README.md` 的"引擎与热切换"一节。切换失败
（端点不可达 / 模型不存在）时当前引擎保持不变，mock 兜底始终可用。

## 显存与并发

- 识别队列并发度固定为 1（`asyncio.Queue` 串行消费），避免多任务并发抢占显存。
- 单次调用超时 `OCR_TIMEOUT_S`（默认 120s），失败自动重试 1 次后回落 mock。
