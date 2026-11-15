# Ledger OCR Service（个人记账软件 v1.0 — OCR 推理服务）

FastAPI + 引擎抽象 + 热切换 + 无模型降级的本地 OCR 服务。监听 `127.0.0.1:8771`，
**不写数据库、不写文件**（只按绝对路径只读图片）；除配置的模型端点外不发任何网络请求。

## 目录结构

```
ocr-service/
├── requirements.txt          # 固定版本依赖（无 torch/transformers）
├── Dockerfile                # python:3.13-slim，非 root，带健康检查
├── README.md                 # 本文件
├── app/
│   ├── main.py               # FastAPI 装配 + lifespan 预热 + 串行 worker
│   ├── config.py             # 环境变量配置
│   ├── schemas.py            # pydantic 请求/响应模型
│   ├── deps.py               # registry / task_store / queue 单例
│   ├── routers/              # health / recognize / tasks / model
│   ├── engines/              # base / registry / mock / ollama / openai_vl
│   ├── pipeline/             # preprocess / prompt / postprocess / rules
│   └── store/tasks.py        # 内存任务表（TTL 1h）
├── models/README.md          # 模型放置与热切换说明（权重不入库）
└── tests/                    # pytest 单测 + fixtures/
```

## 快速启动

```powershell
cd ocr-service
python -m pip install -r requirements.txt
uvicorn app.main:app --host 127.0.0.1 --port 8771 --workers 1
```

> 必须 `--workers 1`：模型常驻内存，多 worker 会重复加载（ARCHITECTURE §8.1）。

启动后自检：

```powershell
curl http://127.0.0.1:8771/healthz     # {"status":"ok"}
curl http://127.0.0.1:8771/readyz      # {"ready":true,"engine":"mock",...}
```

未配置任何模型时 `readyz` 依然 `ready:true`（mock 兜底引擎在启动时即加载）。

## 环境变量清单

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OCR_HOST` / `OCR_PORT` | `127.0.0.1` / `8771` | 监听地址（仅信息展示，实际由 uvicorn 参数决定） |
| `OCR_DEFAULT_ENGINE` | `mock` | 启动默认引擎，如 `ollama:qwen2.5vl:7b` |
| `OLLAMA_BASE_URL` | `http://127.0.0.1:11434` | Ollama 端点 |
| `OLLAMA_DEFAULT_MODEL` | `qwen2.5vl:7b` | ollama 引擎默认模型 |
| `OPENAI_BASE_URL` | `http://127.0.0.1:1234/v1` | OpenAI 兼容端点（vLLM / LM Studio） |
| `OPENAI_DEFAULT_MODEL` | `qwen2-vl-7b-instruct` | openai_vl 引擎默认模型 |
| `OPENAI_API_KEY` | 空 | 兼容端点的 Bearer Key（可选） |
| `OCR_TIMEOUT_S` | `120` | 单次模型调用超时（秒） |
| `OCR_LOAD_TIMEOUT_S` | `5` | 引擎 load/健康检查超时（秒） |
| `OCR_TASK_TTL_S` | `3600` | 异步任务保留时长（秒，1h 后 404） |
| `OCR_MAX_SIDE` | `2048` | 预处理长边上限（像素） |
| `OCR_JPEG_QUALITY` | `88` | 预处理 JPEG 质量 |
| `OCR_MAX_IMAGE_BYTES` | `20971520` | 图片大小上限（20MB） |
| `OCR_STATS_WINDOW` | `1000` | P95 统计滑动窗口 |
| `LOG_LEVEL` | `INFO` | 日志级别 |

## 引擎与热切换

引擎名称语法：

| 名称 | 引擎 | 说明 |
|---|---|---|
| `mock` | MockEngine | 无模型兜底：确定性正则/关键词启发式，永远 `need_review=true`、confidence 全 0.3 |
| `ollama` / `ollama:<model>` | OllamaEngine | 调 Ollama `/api/chat` 多模态（`format:"json"`，失败重试 1 次） |
| `ollama:<model>@<base_url>` | OllamaEngine | 指定 Ollama 地址 |
| `openai` / `openai:<model>` | OpenAIVLEngine | 调 OpenAI 兼容 `/v1/chat/completions` |
| `openai:<base_url>/<model>` | OpenAIVLEngine | 如 `openai:http://127.0.0.1:8000/v1/qwen2-vl-7b` |

热切换（运行时）：

```powershell
# 查看当前引擎 + 可用引擎 + 累计统计（count/avg_ms/p95_ms/fail_rate）
curl http://127.0.0.1:8771/api/v1/model

# 切换到 Ollama 模型
curl -X POST http://127.0.0.1:8771/api/v1/model/switch `
     -H "Content-Type: application/json" `
     -d '{"name":"ollama:qwen2.5vl:7b"}'
# 成功：{"ok":true,"current":"ollama:qwen2.5vl:7b"}
# 失败（端点不可达/模型不存在）：{"ok":false,"current":"mock","error":"..."} ← mock 保持可用，服务不崩
```

切换语义：先构建并 load 新引擎，成功后才 unload 旧引擎；失败保留当前引擎。

### 配置 Ollama 模型（推荐本机路径）

```powershell
ollama pull qwen2.5vl:7b
$env:OCR_DEFAULT_ENGINE="ollama:qwen2.5vl:7b"
uvicorn app.main:app --host 127.0.0.1 --port 8771
# 或运行中热切换（见上）
```

### 自动降级

真实引擎（ollama / openai_vl）识别失败（超时/网络/模型错误）时**自动回落 mock**，
并在结果的 `warnings` 中写入 `"模型不可用，已降级为 mock（...）"`；服务永不因模型故障崩溃。

## API 一览

| Method | Path | 说明 |
|---|---|---|
| GET | `/healthz` | 进程存活 |
| GET | `/readyz` | `{ready, engine, healthy, models}` |
| POST | `/api/v1/recognize` | `{image_path 或 image_base64, model:"auto\|mock\|...", mode:"sync\|async", hint:"wechat\|alipay\|receipt"}` |
| POST | `/api/v1/recognize/batch`（兼容 `/api/v1/recognize-batch`） | `{images:[...], model}` → `{task_ids:[...]}` |
| GET | `/api/v1/tasks/{task_id}` | `{status, progress, result, error, elapsed_ms}`；未知/过期 404 |
| GET | `/api/v1/model` | 当前引擎 + 可用列表 + 累计统计 |
| POST | `/api/v1/model/switch` | `{name, unload_first}` → `{ok, current, error}` |

### 识别结果字段（PRD 7.3 + ARCHITECTURE §5.8 双契约）

`id, source, timestamp, account, transaction_type, amount, category, sub_category,
merchant, region, tags, remark, confidence{amount,timestamp,merchant,category},
raw_text, model_used, need_review`（PRD 7.3 原字段）+
`transaction_time, amount_cents(整数分), account_id, category_id, merchant_id,
warnings`（后端契约；`account_id/category_id` 恒为 null，由 Go 端对照主数据回填）。
`transaction_type` 取值为枚举 `expense / income`（ARCHITECTURE §10.4）；
`amount` 为元浮点、`amount_cents` 为整数分，两者同时返回。

`id` 生成规则与后端 ids 包一致：`yyyyMMddHHmmss(14) + "0000" + 校验码`
（前 18 位数字逐位求和 mod 11，10 → `'X'`）。

### 规则校验边界（重要）

Python 侧（`app/pipeline/rules.py`）只做**纯文本**规则（PRD 7.6 的 ①金额格式
②时间格式 ③收支方向 ④汇总行过滤 ⑤重复行去重），非法即 `need_review=true` + `warnings`。
另外在 `postprocess.normalize_result` 中，真实模型返回的 `confidence < 0.6` 字段会追加
"低置信度字段"提示并强制 `need_review=true`（mock 恒 0.3，已由降级 warning 覆盖）。
**需要数据库的三条（账户存在性 / 分类合法性 / 入库重复检测 / 商户平台 / 冻结边界）
由 Go 端在入账前校验**——本服务不连数据库（ARCHITECTURE §8.3 边界约定）。

### mock 引擎的文本来源（测试通道）

mock 引擎不做真实像素识别（无 OCR 依赖）。其确定性文本来源为：
1. PNG `tEXt/iTXt` 元数据键 `ocr_text`（测试夹具由 `tests/fixtures/__init__.py` 写入）；
2. 无元数据 → 全字段置空 + 降级警告。
真实文本识别必须配置 ollama / openai_vl 引擎。测试夹具图片上同时**真实绘制**了
账单文字（Pillow 渲染），供真实模型联调使用。

## 测试

```powershell
cd ocr-service
python -m pytest -q          # 首次运行自动生成 tests/fixtures/wechat_bill_fixture.png
```

覆盖：postprocess 三类脏 JSON 修复、rules 纯文本规则、mock 启发式、
API 集成（sync/async/任务轮询/热切换失败保护）。

## Docker

```powershell
docker build -t ledger-ocr ./ocr-service
docker run -d -p 8771:8771 --read-only \
  -v D:/Project/记账/data/screenshots:/data/screenshots:ro \
  -e OCR_DEFAULT_ENGINE=ollama:qwen2.5vl:7b \
  -e OLLAMA_BASE_URL=http://host.docker.internal:11434 \
  ledger-ocr
```

镜像 `python:3.13-slim`、非 root 用户运行、内置 HEALTHCHECK（`/healthz`）。
截图目录必须以 `:ro` 只读挂载 —— 本服务只读图片，不写文件。

## 预留项（非 TODO，设计如此）

- `hint` 的 `receipt` 为通用小票提示词；新增票据类型只需在 `app/pipeline/prompt.py`
  的 `HINT_EXTRA` 中追加条目。
- 模型交叉验证（PRD 7.2 升级项）基础版不做；`registry.recognize_with_fallback`
  已预留降级管道，未来可在其后追加第二引擎交叉校验。
- NAS 轻量 OCR（PRD 3.4）归入升级版，不在本服务范围。
