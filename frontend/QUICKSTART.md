# 前端设计系统 - 快速开始

这是一个快速参考指南，帮助你在开发新页面或组件时使用 Relive 的设计系统。

## 📦 文件结构

```
frontend/src/assets/styles/
├── variables.css    # 设计 Token（颜色、间距、阴影等）
└── common.css       # 可复用组件样式

frontend/src/
├── style.css        # 全局样式和 Element Plus 覆盖
└── main.ts          # 样式引入入口
```

## 🎨 常用设计 Token

### 颜色
```css
/* 主色 */
var(--color-primary)           /* #5b7fff */
var(--color-primary-dark)      /* #4468ff */

/* 状态色 */
var(--color-success)           /* 绿色 */
var(--color-warning)           /* 橙色 */
var(--color-error)             /* 红色 */
var(--color-info)              /* 蓝色 */

/* 渐变 */
var(--gradient-primary)        /* 蓝紫渐变 */
var(--gradient-success)        /* 绿色渐变 */
```

### 间距
```css
var(--spacing-xs)    /* 4px */
var(--spacing-sm)    /* 8px */
var(--spacing-md)    /* 16px */
var(--spacing-lg)    /* 24px */
var(--spacing-xl)    /* 32px */
var(--spacing-2xl)   /* 48px */
```

### 圆角
```css
var(--radius-sm)     /* 4px */
var(--radius-md)     /* 8px */
var(--radius-lg)     /* 12px */
var(--radius-xl)     /* 16px */
var(--radius-full)   /* 9999px */
```

### 阴影
```css
var(--shadow-sm)     /* 小阴影 */
var(--shadow-md)     /* 中阴影 */
var(--shadow-lg)     /* 大阴影 */
var(--shadow-xl)     /* 超大阴影 */
```

### 动画
```css
var(--transition-fast)    /* 150ms */
var(--transition-base)    /* 250ms */
var(--transition-slow)    /* 350ms */
```

## 📐 页面模板

### 标准页面
```vue
<template>
  <div class="my-page">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">我的页面</span>
      </h1>
      <p class="page-subtitle">页面描述文字</p>
    </div>

    <!-- 内容区域 -->
    <div class="modern-card animate-fade-in animate-delay-1">
      <h2>内容标题</h2>
      <p>内容...</p>
    </div>
  </div>
</template>

<style scoped>
.my-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-secondary);
  min-height: 100vh;
}
</style>
```

## 🎯 常用组件

### 1. 统计卡片
```vue
<div class="stat-card">
  <div class="stat-card-header">
    <div class="stat-card-icon stat-icon-primary">
      <el-icon><Picture /></el-icon>
    </div>
    <div class="stat-card-title">标题</div>
  </div>
  <div class="stat-card-value">1,234</div>
  <div class="stat-card-subtitle">副标题</div>
</div>
```

### 2. 现代卡片
```vue
<div class="modern-card">
  <h3>卡片标题</h3>
  <p>卡片内容</p>
</div>
```

### 3. 图片卡片
```vue
<div class="image-card">
  <el-image :src="url" class="image-card-image" />
  <div class="image-card-badge">标签</div>
  <div class="image-card-overlay">
    <div class="overlay-content">
      <div>悬停显示的内容</div>
    </div>
  </div>
</div>
```

### 4. 渐变按钮
```vue
<el-button class="gradient-button">
  <el-icon><Plus /></el-icon>
  按钮文字
</el-button>

<style scoped>
.gradient-button {
  background: var(--gradient-primary);
  border: none;
  color: white;
  transition: all var(--transition-base);
}

.gradient-button:hover {
  background: var(--gradient-primary-hover);
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}
</style>
```

## ✨ 动画类

### 基础动画
```html
<!-- 淡入 -->
<div class="animate-fade-in">内容</div>

<!-- 缩放进入 -->
<div class="animate-scale-in">内容</div>

<!-- 滑入 -->
<div class="animate-slide-in-right">从右滑入</div>
<div class="animate-slide-in-left">从左滑入</div>
```

### 延迟动画（用于列表）
```html
<div class="animate-fade-in">立即显示</div>
<div class="animate-fade-in animate-delay-1">延迟 100ms</div>
<div class="animate-fade-in animate-delay-2">延迟 200ms</div>
<div class="animate-fade-in animate-delay-3">延迟 300ms</div>
```

## 🎪 悬停效果

### 上浮效果
```css
.my-card {
  transition: all var(--transition-base);
}

.my-card:hover {
  transform: translateY(-4px);
  box-shadow: var(--shadow-xl);
}
```

### 缩放效果
```css
.my-button {
  transition: transform var(--transition-base);
}

.my-button:hover {
  transform: scale(1.05);
}
```

### 图标旋转
```css
.my-icon {
  transition: transform var(--transition-base);
}

.my-card:hover .my-icon {
  transform: rotate(5deg) scale(1.1);
}
```

## 📱 响应式设计

### Element Plus 栅格
```vue
<el-row :gutter="20">
  <!-- 手机 12 列，平板 8 列，桌面 6 列 -->
  <el-col :xs="12" :sm="8" :md="6">
    <div>内容</div>
  </el-col>
</el-row>
```

### 媒体查询
```css
/* 平板 */
@media (max-width: 768px) {
  .my-page {
    padding: var(--spacing-md);
  }
}

/* 手机 */
@media (max-width: 480px) {
  .page-title {
    font-size: var(--font-size-xl);
  }
}
```

## 🌈 颜色使用指南

### 图标背景色
```css
/* 主色系 */
.stat-icon-primary {
  background: linear-gradient(135deg, rgba(91, 127, 255, 0.1), rgba(168, 85, 247, 0.1));
  color: var(--color-primary);
}

/* 成功色系 */
.stat-icon-success {
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.1), rgba(52, 211, 153, 0.1));
  color: var(--color-success);
}

/* 警告色系 */
.stat-icon-warning {
  background: linear-gradient(135deg, rgba(245, 158, 11, 0.1), rgba(251, 191, 36, 0.1));
  color: var(--color-warning);
}

/* 信息色系 */
.stat-icon-info {
  background: linear-gradient(135deg, rgba(59, 130, 246, 0.1), rgba(96, 165, 250, 0.1));
  color: var(--color-info);
}
```

### 文字渐变
```css
.text-gradient {
  background: var(--gradient-primary);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}
```

## 🔥 常用代码片段

### 页面容器
```css
.my-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-secondary);
  min-height: 100vh;
}
```

### 卡片容器
```css
.my-card {
  background: var(--color-bg-primary);
  border-radius: var(--radius-xl);
  padding: var(--spacing-xl);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
}

.my-card:hover {
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
}
```

### 标题区域
```css
.section-title {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--spacing-xl);
}
```

### 网格布局
```css
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: var(--spacing-lg);
}
```

## 💡 最佳实践

### ✅ 推荐做法
```css
/* 使用设计 Token */
color: var(--color-primary);
padding: var(--spacing-lg);
border-radius: var(--radius-xl);

/* 使用 transform 实现动画 */
transform: translateY(-4px);

/* 使用语义化类名 */
.photo-card { }
.stat-value { }
```

### ❌ 避免做法
```css
/* 避免硬编码 */
color: #5b7fff;
padding: 24px;
border-radius: 16px;

/* 避免使用 width/height 动画 */
width: 200px; /* 动画性能差 */

/* 避免无意义的类名 */
.div1 { }
.box { }
```

## 🎓 学习资源

- **设计系统文档**: `/frontend/DESIGN_SYSTEM.md`
- **改进说明**: `/frontend/DESIGN_IMPROVEMENTS.md`
- **示例页面**:
  - Dashboard: `/frontend/src/views/Dashboard/index.vue`
  - Photos: `/frontend/src/views/Photos/index.vue`
  - System: `/frontend/src/views/System/index.vue`

## 🆘 常见问题

### Q: 如何添加新的颜色？
A: 在 `variables.css` 中定义新的 CSS 变量：
```css
:root {
  --color-my-new-color: #ff6b6b;
}
```

### Q: 如何自定义 Element Plus 组件样式？
A: 在 `style.css` 中使用深度选择器：
```css
.el-button {
  border-radius: var(--radius-lg) !important;
}
```

### Q: 如何禁用动画？
A: 在元素上不使用 `animate-*` 类即可。

### Q: 如何测试深色模式？
A:
1. 系统级别：在操作系统中切换深色模式
2. 代码级别：在 HTML 元素添加 `.dark` 类

## 📞 需要帮助？

查看完整文档：
- `/frontend/DESIGN_SYSTEM.md` - 完整的设计系统文档
- `/frontend/DESIGN_IMPROVEMENTS.md` - 改进说明和技术细节

祝你开发愉快！🚀
