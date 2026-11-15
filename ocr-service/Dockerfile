# OCR 服务镜像：python:3.13-slim、非 root、内置健康检查
# 构建上下文为 ocr-service/ 目录：docker build -t ledger-ocr .
FROM python:3.13-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    OCR_HOST=0.0.0.0 \
    OCR_PORT=8771

WORKDIR /srv/ocr

# 先装依赖（利用层缓存；--no-deps 不适用，这里按 requirements 固定版本安装）
COPY requirements.txt ./
RUN python -m pip install --no-cache-dir -r requirements.txt

# 应用代码
COPY app ./app
COPY tests ./tests

# 非 root 用户运行
RUN useradd --create-home --uid 10001 ocr && chown -R ocr:ocr /srv/ocr
USER ocr

EXPOSE 8771

# 健康检查：slim 镜像无 curl，用 Python 标准库探测 /healthz
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8771/healthz', timeout=4).status == 200 else 1)"]

# workers=1：模型常驻内存，多 worker 会重复加载（ARCHITECTURE §8.1）
CMD ["python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8771", "--workers", "1"]
