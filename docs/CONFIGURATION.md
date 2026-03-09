# Relive 配置指南

> 本文档说明 Relive 当前版本的配置分层、单一真值和推荐修改入口。

## 先看结论

如果你是使用者，优先只关心这 4 个入口：
- `.env`
- `docker-compose.yml`
- `analyzer.yaml`
- Web 后台配置页

如果脚本提示缺少 `backend/config.dev.yaml` 或 `backend/config.prod.yaml`，应从对应的 `.example` 文件复制生成。

一句话原则：
- **部署改文件**
- **业务改后台**

---

## 配置分层

Relive 的配置分为三层：

### 1. 用户部署配置

这层由部署者直接修改。

**文件：**
- `.env`
- `docker-compose.yml`
- `analyzer.yaml`
- `frontend/.env.development`
- `frontend/.env.production`

**负责：**
- 端口
- 容器挂载路径
- `external_url`
- secrets / API key 注入
- analyzer 如何连接 Relive 服务
- analyzer 自己的并发、下载、batch、provider 配置

### 2. 服务启动默认配置

这层是后端启动时读取的默认值和路径约定，主要面向维护者和开发者。

**当前文件：**
- `backend/config.dev.yaml.example`
- `backend/config.dev.yaml`（本地生成）
- `backend/config.prod.yaml.example`
- `backend/config.prod.yaml`（本地生成）
- `backend/config.base.yaml`（启动默认值基线）
- `backend/config.default.yaml`（历史文件，已退役）

**负责：**
- server 默认监听行为
- database 路径
- photos 根目录约定
- thumbnail / logging / performance 默认值
- security 非敏感默认值

> 注意：这一层是“启动默认值”，不是“运行时业务真值”。

### 3. 数据库运行时配置

这层由 Web 后台配置页维护，系统运行时应以数据库为准。

**负责：**
- AI provider 选择与相关参数
- geocode provider 选择与相关参数
- 展示策略
- prompt 配置
- scan paths
- 其他业务运行参数

---

## 单一真值规则

### 部署类配置

真值来源：部署层文件

| 配置主题 | 真值来源 |
|----------|----------|
| 端口 | `.env` / `docker-compose.yml` |
| 挂载路径 | `docker-compose.yml` |
| `external_url` | 部署层（建议 `.env` 注入） |
| JWT secret | `.env` |
| analyzer 服务端连接 | `analyzer.yaml` |

### 业务运行类配置

真值来源：数据库

| 配置主题 | 真值来源 |
|----------|----------|
| AI provider 与参数 | 数据库 |
| geocode provider 与参数 | 数据库 |
| 展示策略 | 数据库 |
| prompt 配置 | 数据库 |
| scan paths | 数据库 |

> 启动 YAML 中这些配置最多只作为首次启动默认值或兜底值。

---

## 两个最容易混淆的配置

## `photos.root_path`

`photos.root_path` 属于**部署 / 启动层**，不是用户业务真值。

它表示：
- 后端进程能看到的照片根目录边界
- 通常与 Docker 挂载后的容器内路径对应，例如 `/app/photos`

它不表示：
- 用户最终要扫描哪些目录

用户真正选择扫描的目录列表，应通过后台配置页写入数据库中的 `scan_paths`。

可以这样理解：
- `photos.root_path` = 系统能看见哪里
- `scan_paths` = 用户要扫哪里

## `external_url`

`external_url` 属于**部署层**，不建议进数据库。

原因：
- 它依赖域名、反向代理、端口映射和网络拓扑
- 它是环境事实，不是业务偏好
- analyzer 下载链接依赖它

推荐做法：
- 由部署者在 `.env` 中统一配置
- 再通过部署层或启动层传给后端

---

## 使用者应该怎么改配置

### 你在部署时通常改这些

#### `.env`
适合放：
- `JWT_SECRET`
- 端口类配置
- `RELIVE_EXTERNAL_URL`（建议新增并统一使用）
- 可选的 API key 注入

#### `docker-compose.yml`
适合放：
- volumes 挂载
- ports 映射
- 容器资源限制
- 运行时环境变量注入

#### `analyzer.yaml`
适合放：
- `server.endpoint`
- `server.api_key`
- analyzer 并发和下载参数
- analyzer 所用 AI provider 配置

#### Web 后台配置页
适合放：
- AI provider
- geocode provider
- display strategy
- prompts
- scan paths

---

## 开发者应该怎么理解配置

如果你在改代码，请优先按这个思路判断配置该放哪里：

### 放部署层
如果它：
- 和容器、域名、挂载、端口、secret 绑定
- 离开当前部署环境就会变化

### 放数据库
如果它：
- 是业务运行策略
- 希望通过后台动态调整
- 改完后不想重启服务

### 放启动默认值层
如果它：
- 是后端默认行为
- 是路径或日志等结构性约定
- 不是用户日常调参项

---

## 当前已知重复与改进方向

### analyzer 配置入口重复
当前用户入口应统一为：
- `analyzer.yaml.example`
- `analyzer.yaml`

`backend/configs/analyzer.yaml` 属于重复入口，已删除；根目录 `analyzer.yaml.example` 是唯一 analyzer 模板。

### 后端默认配置语义重叠
当前存在：
- `backend/config.prod.yaml`
- `backend/config.prod.yaml.example`
- `backend/config.base.yaml`
- `backend/config.default.yaml`（历史文件，已退役）

这两个文件语义容易重叠，后续建议收敛为：
- `backend/config.base.yaml`
- `backend/config.dev.yaml`
- `backend/config.prod.yaml`
- `backend/config.prod.yaml.example`

---

## 推荐阅读顺序

### 使用者
1. `README.md`
2. `QUICKSTART.md`
3. `docs/QUICKSTART.md`
4. 本文档
5. `docs/ANALYZER_API_MODE.md`

### 开发者
1. 本文档
2. `docs/DEVELOPMENT.md`
3. `backend/pkg/config/config.go`
4. `docs/PROJECT_STATUS.md`
