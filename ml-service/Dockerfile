FROM python:3.12-slim

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV INSIGHTFACE_HOME=/app/models

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    libgl1 \
    libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app

# v2 独立验证器（YuNet）资产：复制并校验 SHA256SUMS。
# 模型缺失或校验失败不阻断构建（资产由 NAS 侧注入），但运行时 FaceVerifier.available=False，
# 历史复核 worker 写 technical_error，绝不静默退回 v1 同源评分。
# 注：任务书要求「Docker 构建时校验 SHA256SUMS」，但模型文件不入 Git、不在构建上下文，
# 故构建时无法校验。降级为运行时 FaceVerifier.__init__ 校验（FaceVerifier.available 标志门控）。
COPY assets ./assets

EXPOSE 5050

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "5050"]
