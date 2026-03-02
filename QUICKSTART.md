# Relive 快速启动指南

> 5 分钟让 Relive 运行起来！

## 📋 前置要求

- ✅ Docker 20.10+
- ✅ Docker Compose 1.29+
- ✅ 照片目录（可以是任意路径）
- ✅ （可选）AI 服务（Ollama/Qwen/OpenAI）

## 🚀 快速启动（3 步）

### 1. 克隆并进入项目

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

### 2. 配置环境变量

```bash
# 复制配置文件
cp .env.example .env

# 编辑 .env 文件
nano .env  # 或使用你喜欢的编辑器
```

**必须配置**：
```env
# 修改为你的照片目录
PHOTOS_PATH=/volume1/photos
```

**可选配置**（如果使用在线 AI）：
```env
# Qwen API（推荐，便宜）
QWEN_API_KEY=your-qwen-api-key

# 或 OpenAI API
OPENAI_API_KEY=your-openai-api-key
```

### 3. 运行启动脚本

```bash
./start.sh
```

启动脚本会自动：
- ✅ 检查环境
- ✅ 构建前端
- ✅ 构建 Docker 镜像
- ✅ 启动所有服务

等待 30 秒后访问：
- **前端界面**：http://localhost:8888
- **后端 API**：http://localhost:8080

---

## 🎯 首次使用流程

### 1. 访问前端界面

打开浏览器访问 http://localhost:8888

### 2. 扫描照片

在首页点击 **"开始扫描"** 按钮，系统会：
- 扫描你配置的照片目录
- 读取 EXIF 信息（拍摄时间、GPS 等）
- 生成缩略图
- 保存到数据库

**扫描时间**：约 1000 张/分钟（取决于硬盘速度）

### 3. 配置 AI Provider

在 **"配置管理"** 页面设置 AI 提供者：

**选项 A：使用本地 Ollama（免费）**
```yaml
provider: ollama
endpoint: http://host.docker.internal:11434
model: llava:13b
```

**选项 B：使用 Qwen API（便宜）**
```yaml
provider: qwen
api_key: your-api-key
model: qwen-vl-max
```

**选项 C：使用 OpenAI（高质量）**
```yaml
provider: openai
api_key: your-api-key
model: gpt-4-vision-preview
```

### 4. 方式一：在线分析（适合小量照片）

在 **"AI 分析"** 页面：
- 点击 **"开始分析"**
- 选择要分析的照片数量
- 实时查看进度

**适用场景**：
- ✅ 照片数量 < 1000
- ✅ AI 服务与 NAS 在同一网络
- ✅ 想要实时看到结果

### 5. 方式二：离线分析（推荐，适合大量照片）

#### Step 1：导出数据

在 **"导出/导入"** 页面：
- 点击 **"导出数据"**
- 下载 `export.db` 文件（包含照片信息和缩略图）

#### Step 2：离线分析

将 `export.db` 复制到任何有 AI 服务的电脑上：

```bash
# 编译 relive-analyzer（如果还没有）
cd backend
go build -o relive-analyzer ./cmd/relive-analyzer

# 检查数据库
./relive-analyzer check -db export.db

# 估算成本和时间
./relive-analyzer estimate -config configs/analyzer.yaml -db export.db

# 开始分析
./relive-analyzer analyze -config configs/analyzer.yaml -db export.db
```

**优势**：
- ✅ 可以在任何电脑上运行
- ✅ 不需要访问 NAS
- ✅ 支持断点续传
- ✅ 性能更好（批量处理）

#### Step 3：导入结果

分析完成后，在 **"导出/导入"** 页面：
- 上传分析后的 `export.db`
- 系统自动合并结果
- 完成！

### 6. 查看结果

在 **"照片列表"** 页面：
- 浏览所有照片
- 查看 AI 分析结果
- 按分类、标签筛选

---

## 📊 常用命令

### 查看服务状态
```bash
docker-compose ps
```

### 查看日志
```bash
# 所有服务
docker-compose logs -f

# 只看后端
docker-compose logs -f relive-backend

# 只看前端
docker-compose logs -f relive-frontend
```

### 停止服务
```bash
docker-compose down
```

### 重启服务
```bash
docker-compose restart
```

### 更新代码后重新构建
```bash
# 拉取最新代码
git pull

# 重新构建并启动
./start.sh
```

---

## 🔧 故障排除

### 前端无法连接后端

检查后端是否正常运行：
```bash
curl http://localhost:8080/system/health
```

应该返回：
```json
{
  "status": "healthy",
  "version": "dev",
  "uptime": "5m30s"
}
```

### 照片扫描失败

1. 检查照片目录权限
```bash
docker-compose exec relive-backend ls -la /app/photos
```

2. 检查日志
```bash
docker-compose logs relive-backend | grep ERROR
```

### AI 分析失败

1. 测试 AI 服务连接
```bash
# 如果使用 Ollama
curl http://localhost:11434/api/version

# 如果使用 Qwen
curl -H "Authorization: Bearer $QWEN_API_KEY" \
  https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation
```

2. 查看详细日志
```bash
docker-compose logs relive-backend | grep "AI Provider"
```

### 容器无法启动

```bash
# 查看详细错误
docker-compose logs

# 检查端口占用
netstat -an | grep 8080
netstat -an | grep 8888
```

---

## 🎨 下一步

- 📱 配置 ESP32 设备展示照片
- 🔐 配置 Web 认证（生产环境）
- 🌐 配置反向代理（Nginx/Traefik）
- 📊 查看分析统计和成本

---

## 💡 提示

1. **扫描照片**：第一次扫描可能需要一些时间，取决于照片数量
2. **AI 分析**：推荐使用离线分析工具（relive-analyzer）处理大量照片
3. **成本控制**：使用 Ollama（免费）或 Qwen（便宜）可以大幅降低成本
4. **备份数据**：定期备份 `data/backend/relive.db`

---

## 📖 更多文档

- [部署指南](docs/DEPLOYMENT.md) - 详细的部署文档
- [API 文档](docs/API_DESIGN.md) - 完整的 API 说明
- [离线工具](docs/ANALYZER.md) - relive-analyzer 使用指南
- [架构设计](docs/ARCHITECTURE.md) - 系统架构说明

---

**遇到问题？**
- 查看 [故障排查文档](docs/DEPLOYMENT.md#故障排查)
- 提交 [GitHub Issue](https://github.com/davidhoo/relive/issues)
