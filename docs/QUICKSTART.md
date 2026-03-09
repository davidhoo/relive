# 快速部署指南

> 面向 NAS / 服务器部署的当前版本快速入口。

## 环境要求

- Docker 20.10+
- Docker Compose 1.29+
- 建议 4GB+ 内存
- 需要一个宿主机照片目录供容器挂载

## 部署步骤

### 1. 克隆仓库

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

### 2. 准备环境变量

```bash
cp .env.example .env
```

建议至少修改：

```env
JWT_SECRET=replace-with-a-random-secret
```

### 3. 配置照片目录挂载

编辑 `docker-compose.yml`：

```yaml
services:
  relive:
    volumes:
      - /volume1/photos:/app/photos:ro
```

后续在 Web 中添加扫描路径时，请使用容器内路径：`/app/photos`。

### 4. 启动服务

```bash
chmod +x deploy.sh
./deploy.sh
```

或者：

```bash
make deploy
```

### 5. 访问系统

- Web：`http://your-host:8080`
- 默认账号：`admin / admin`
- 首次登录会强制修改密码

## 推荐初始化流程

1. “配置管理” → 添加扫描路径
2. “照片管理” → 启动扫描或重建
3. “配置管理” → 配置 AI Provider
4. “AI 分析” → 在线分析，或单独部署 `relive-analyzer`

## analyzer API 模式

当前 analyzer 工作流如下：

1. 在“设备管理”中创建 `offline` 或 `service` 类型设备
2. 复制生成的 `api_key`
3. `cp analyzer.yaml.example analyzer.yaml`
4. 在 `analyzer.yaml` 中填写 `server.endpoint` 与 `server.api_key`
5. 运行：

```bash
make build-analyzer
./backend/bin/relive-analyzer check -config analyzer.yaml
./backend/bin/relive-analyzer analyze -config analyzer.yaml
```

> 旧版 `export.db` 导出/导入流程已不再是当前默认工作流。

## 常用命令

```bash
docker-compose ps
docker-compose logs -f
docker-compose restart
docker-compose down
git pull && make deploy
```
