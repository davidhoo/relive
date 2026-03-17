# 照片过滤与排除系统设计方案

> 状态：待实施
> 创建：2026-03-12
> 背景：系统已扫描约 20 万张照片，需要灵活的排除和过滤机制

## 1. 需求背景

### 1.1 永久排除

- 大量照片需要排除出系统（截图、工作文档、低质量照片等）
- 如果直接删除数据库记录，后续重新扫描会再次导入
- 需要一种"标记排除"机制，让照片记录保留但不再参与展示，且重扫不会恢复

### 1.2 多级展示过滤器

- **全局**：对所有展示生效的默认过滤规则
- **扫描路径级**：针对特定扫描源的照片设定额外过滤
- **设备级**：针对特定设备的展示策略（如客厅相框只展示家庭照）

### 1.3 复杂过滤规则

- 排除某个分类（如排除 screenshot、document）
- 排除/包含某些标签
- 组合逻辑：有 tag a、b、c 但不能有 tag d、e
- 分数阈值过滤（美学分、记忆分）
- 位置、时间等元数据条件

## 2. 现状分析

### 2.1 当前 Photo 模型

- **无状态字段**：没有 hidden/excluded/favorite 等用户可操作的状态
- **分类/标签**：`MainCategory`（varchar 50）、`Tags`（JSON 数组字符串）
- **分数**：`BeautyScore`、`MemoryScore`、`OverallScore`（0-100）
- **位置**：`Location`（城市名）、GPS 坐标
- **时间**：`TakenAt`（拍摄时间）

### 2.2 当前展示选择逻辑

- 仅靠硬编码分数阈值（`MinBeautyScore: 70`、`MinMemoryScore: 60`）
- 通过 `DisplayStrategyConfig` 存储在 `app_config` 表
- 去重逻辑：排除最近已展示的照片
- 无标签/分类过滤能力

### 2.3 当前扫描逻辑

- 按 `file_path` 唯一索引判断是否已存在
- 已存在则跳过，不存在则插入
- 删除扫描路径会硬删除该路径下所有照片记录

## 3. 方案设计

### 3.1 第一层：照片级永久排除

**Photo 表新增 `status` 字段：**

```go
// Photo model 新增字段
Status string `json:"status" gorm:"type:varchar(20);default:'active';index"`
```

| 值 | 含义 |
|---|---|
| `active` | 正常状态，参与展示 |
| `excluded` | 已排除，不参与展示，重扫跳过 |

**核心逻辑变更：**

- **扫描时**：发现 `file_path` 已存在且 `status=excluded` → 直接跳过，不更新不恢复
- **查询时**：默认条件加 `status='active'`，通过 GORM Scope 实现
- **管理接口**：支持批量标记 excluded、批量恢复为 active
- **管理界面**：可通过 `?status=excluded` 筛选查看被排除的照片

### 3.2 第二层：多级展示过滤规则引擎

**新增 `display_filters` 表：**

```go
type DisplayFilter struct {
    ID        uint           `json:"id" gorm:"primarykey"`
    CreatedAt time.Time      `json:"created_at"`
    UpdatedAt time.Time      `json:"updated_at"`
    DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

    Name     string `json:"name" gorm:"type:varchar(100)"`
    Scope    string `json:"scope" gorm:"type:varchar(20);index"`    // global | scan_path | device
    ScopeID  string `json:"scope_id" gorm:"type:varchar(100);index"` // "" for global, path_id or device_id
    Priority int    `json:"priority" gorm:"default:0"`               // 数值越大优先级越高
    Enabled  bool   `json:"enabled" gorm:"default:true"`
    Rules    string `json:"rules" gorm:"type:text"`                  // JSON 规则定义
}
```

**Rules JSON 结构：**

```json
{
  "conditions": [
    {"field": "main_category", "op": "not_in", "values": ["screenshot", "document", "meme"]},
    {"field": "tags", "op": "contains_any", "values": ["family", "travel", "nature"]},
    {"field": "tags", "op": "not_contains_any", "values": ["work", "receipt", "temp"]},
    {"field": "beauty_score", "op": "gte", "value": 60},
    {"field": "memory_score", "op": "gte", "value": 50},
    {"field": "location", "op": "not_empty"}
  ],
  "logic": "and"
}
```

**支持的操作符：**

| op | 含义 | 适用字段类型 |
|---|---|---|
| `in` | 值在列表中 | main_category, location |
| `not_in` | 值不在列表中 | main_category, location |
| `contains_any` | 包含任一标签 | tags |
| `contains_all` | 包含全部标签 | tags |
| `not_contains_any` | 不包含任一标签 | tags |
| `gte` | 大于等于 | beauty_score, memory_score, overall_score |
| `lte` | 小于等于 | beauty_score, memory_score, overall_score |
| `not_empty` | 非空 | location, taken_at |
| `is_empty` | 为空 | location, taken_at |

### 3.3 多级合并策略

```
最终过滤 = global filters ∩ scan_path filters ∩ device filters
```

- **逐级叠加，取 AND**：子级只能收紧父级的条件，不能放宽
- 同一级别内多个 filter 按 `priority` 排序后依次应用
- 一张照片必须通过所有适用 filter 的全部条件才会被选中展示

**示例场景：**

| 级别 | 规则 | 效果 |
|---|---|---|
| global | 排除 category: screenshot, document | 所有设备都不展示截图和文档 |
| scan_path (公司网盘) | 排除 tags: work, meeting | 公司网盘的照片额外排除工作相关 |
| device (客厅相框) | 仅包含 tags: family, travel | 客厅相框只展示家庭和旅行照片 |

## 4. 实现影响面

### 4.1 后端变更

```
backend/internal/model/
  ├── photo.go              # Photo 新增 Status 字段
  └── display_filter.go     # [新文件] DisplayFilter 模型

backend/internal/repository/
  ├── photo_repo.go         # 查询默认加 status='active' scope
  └── display_filter_repo.go # [新文件] DisplayFilter CRUD

backend/internal/service/
  ├── photo_service.go          # 扫描逻辑：跳过 excluded 照片
  ├── display_service.go        # 展示选择：应用 filter 规则
  ├── display_daily_service.go  # 每日批次生成：应用 filter 规则
  └── display_filter_service.go # [新文件] 规则解析、合并、执行引擎

backend/internal/api/v1/handler/
  ├── photo_handler.go           # 新增批量排除/恢复 API
  └── display_filter_handler.go  # [新文件] 过滤规则 CRUD API
```

### 4.2 前端变更

```
frontend/src/
  ├── api/display-filter.ts      # [新文件] 过滤规则 API
  ├── types/display-filter.ts    # [新文件] 类型定义
  ├── views/Photos/              # 照片列表增加批量排除操作、status 筛选
  └── views/Filters/             # [新目录] 过滤规则管理页面
```

### 4.3 新增 API

```
# 照片排除
PATCH  /api/v1/photos/batch-status    # 批量设置 status (excluded/active)

# 过滤规则 CRUD
GET    /api/v1/display-filters                 # 列表（支持 ?scope=global/scan_path/device）
POST   /api/v1/display-filters                 # 创建
GET    /api/v1/display-filters/:id             # 详情
PUT    /api/v1/display-filters/:id             # 更新
DELETE /api/v1/display-filters/:id             # 删除
POST   /api/v1/display-filters/:id/preview     # 预览：返回匹配的照片数量和样本
```

## 5. 设计决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 排除机制 | Photo 加 status 字段 | 简单高效，GORM Scope 一行实现，查询有索引 |
| 排除 vs 单独表 | 字段而非关系表 | 一对一关系，无需额外 JOIN |
| 过滤规则存储 | JSON 字段 | 规则结构灵活可扩展，规则总量不大（几十条） |
| Tags 过滤方式 | 应用层过滤 | Tags 是 JSON 字符串存储，SQL LIKE 不够精确，候选集内存过滤即可 |
| 多级合并逻辑 | AND 叠加 | 子级只收紧不放宽，语义清晰，避免冲突 |
| 规则生效时机 | 展示选择时实时计算 | 20 万照片中候选集通常几百到几千张，实时过滤无性能瓶颈 |

## 6. 性能考量

- `status` 字段有索引，排除过滤 O(1)
- 展示选择本身经过多级筛选（已分析、分数阈值、时间范围），候选集通常几百张
- 过滤规则在内存候选集上执行，不构成瓶颈
- 未来如需更复杂标签查询，可考虑拆出 `photo_tags` 多对多关系表，当前 JSON + 应用层过滤足够

## 7. 后续扩展

- **智能排除建议**：基于 AI 分析结果，自动建议排除低质量/重复照片
- **规则模板**：预置常用过滤规则模板（如"只看人物"、"风景模式"等）
- **时间条件**：支持按拍摄年份范围、季节等时间维度过滤
- **规则测试**：preview API 可在保存前预览规则效果
