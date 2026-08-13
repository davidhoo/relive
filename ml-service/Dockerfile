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

# v2 独立验证器（YuNet）资产：模型必须由 scripts/fetch-yunet-model.sh 在构建前取得并放入
# 构建上下文（不入 Git）。这里 COPY 后立即校验 SHA256SUMS——模型缺失或摘要不匹配必须使
# 镜像构建失败，绝不生成“容器健康但验证器不可用”的发布物。FaceVerifier 运行时再校验一次，
# 防御镜像层损坏。不得用 docker cp / 运行时下载 / 未校验卷文件替代本步骤。
COPY assets ./assets
RUN test -s assets/yunet/face_detection_yunet_2023mar.onnx \
    && cd assets/yunet && sha256sum -c SHA256SUMS

EXPOSE 5050

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "5050"]
