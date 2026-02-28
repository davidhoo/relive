# Relive 前端设计系统

本项目采用现代化的设计系统，提供统一的视觉风格和交互体验。

## 设计文件

### 1. `assets/styles/variables.css`
定义了完整的设计 Token 系统，包括：
- **颜色系统**：主色、辅色、状态色、渐变色
- **间距系统**：xs, sm, md, lg, xl, 2xl, 3xl
- **圆角**：sm, md, lg, xl, 2xl, full
- **阴影**：xs, sm, md, lg, xl, 2xl, inner
- **动画**：fast, base, slow, slower, bounce
- **字体**：大小、粗细、行高
- **深色模式**：自动适配系统主题

### 2. `assets/styles/common.css`
提供常用的组件样式和工具类：
- **卡片组件**：modern-card, glass-card, stat-card
- **图片卡片**：image-card（带悬停效果）
- **动画效果**：fade-in, slide-in, scale-in
- **进度条**：modern-progress（带渐变和动画）
- **标签**：modern-tag（多种颜色主题）

## 设计原则

### 1. 渐变色使用
```css
/* 主要渐变 - 用于按钮、强调元素 */
background: var(--gradient-primary);

/* 成功渐变 - 用于成功状态 */
background: var(--gradient-success);

/* 警告渐变 - 用于警告提示 */
background: var(--gradient-warning);

/* 错误渐变 - 用于错误状态 */
background: var(--gradient-error);
```

### 2. 间距系统
```css
/* 使用统一的间距变量 */
padding: var(--spacing-xl);
margin-bottom: var(--spacing-lg);
gap: var(--spacing-md);
```

### 3. 圆角和阴影
```css
/* 卡片圆角 */
border-radius: var(--radius-xl);

/* 按钮圆角 */
border-radius: var(--radius-lg);

/* 卡片阴影 */
box-shadow: var(--shadow-lg);
```

### 4. 动画效果
```css
/* 过渡动画 */
transition: all var(--transition-base);

/* 悬停效果 */
.card:hover {
  transform: translateY(-4px);
  box-shadow: var(--shadow-xl);
}
```

## 常用组件

### 统计卡片
```vue
<div class="stat-card">
  <div class="stat-card-header">
    <div class="stat-card-icon stat-icon-primary">
      <el-icon><Picture /></el-icon>
    </div>
    <div class="stat-card-title">总照片数</div>
  </div>
  <div class="stat-card-value">1,234</div>
  <div class="stat-card-subtitle">所有照片</div>
</div>
```

### 图片卡片
```vue
<div class="image-card">
  <el-image :src="url" class="image-card-image" />
  <div class="image-card-badge">9.5</div>
  <div class="image-card-overlay">
    <div class="overlay-content">
      <div class="photo-name">照片名称</div>
    </div>
  </div>
</div>
```

### 现代卡片
```vue
<div class="modern-card">
  <!-- 内容 -->
</div>
```

### 玻璃态卡片
```vue
<div class="glass-card">
  <!-- 内容 -->
</div>
```

## 动画类

### 淡入动画
```vue
<div class="animate-fade-in">内容</div>
```

### 延迟动画
```vue
<div class="animate-fade-in animate-delay-1">内容 1</div>
<div class="animate-fade-in animate-delay-2">内容 2</div>
<div class="animate-fade-in animate-delay-3">内容 3</div>
```

### 缩放动画
```vue
<div class="animate-scale-in">内容</div>
```

### 滑入动画
```vue
<div class="animate-slide-in-right">从右滑入</div>
<div class="animate-slide-in-left">从左滑入</div>
```

## 响应式设计

所有页面都支持响应式布局，断点如下：
- **xs**: 480px（手机竖屏）
- **sm**: 640px（手机横屏）
- **md**: 768px（平板竖屏）
- **lg**: 1024px（平板横屏）
- **xl**: 1280px（笔记本）
- **2xl**: 1536px（桌面显示器）

## 深色模式

设计系统自动适配系统深色模式：
```css
@media (prefers-color-scheme: dark) {
  /* 深色模式样式 */
}

/* 或使用 .dark 类强制深色模式 */
.dark {
  /* 深色模式样式 */
}
```

## 页面结构

### 标准页面布局
```vue
<template>
  <div class="page-name">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">页面标题</span>
      </h1>
      <p class="page-subtitle">页面描述</p>
    </div>

    <!-- 页面内容 -->
    <div class="modern-card animate-fade-in">
      <!-- 内容 -->
    </div>
  </div>
</template>

<style scoped>
.page-name {
  padding: var(--spacing-xl);
  background: var(--color-bg-secondary);
  min-height: 100vh;
}
</style>
```

## 颜色使用指南

### 主色调
- **Primary (蓝紫色)**：主要操作、链接、选中状态
- **Secondary (紫色)**：次要操作、装饰元素

### 状态色
- **Success (绿色)**：成功消息、已完成状态
- **Warning (橙色)**：警告消息、待处理状态
- **Error (红色)**：错误消息、失败状态
- **Info (蓝色)**：信息提示、说明文字

### 中性色
- **Text Primary**：主要文字
- **Text Secondary**：次要文字
- **Text Tertiary**：辅助文字
- **Text Disabled**：禁用文字

## 最佳实践

1. **保持一致性**：使用设计 Token 而不是硬编码值
2. **合理使用动画**：不要过度使用动画，保持流畅自然
3. **注意可访问性**：确保足够的对比度和点击区域
4. **响应式优先**：使用 Element Plus 的栅格系统
5. **性能优化**：使用 CSS 变量和 transform 实现动画

## 图标使用

使用 Element Plus Icons：
```vue
<script setup>
import {
  Picture,
  MagicStick,
  Monitor,
  DataLine
} from '@element-plus/icons-vue'
</script>

<template>
  <el-icon><Picture /></el-icon>
</template>
```

## 开发建议

1. **组件化**：将重复使用的 UI 抽取为组件
2. **样式隔离**：使用 `scoped` 避免样式污染
3. **命名规范**：使用 BEM 命名法或语义化命名
4. **代码注释**：为复杂样式添加注释说明
5. **测试适配**：在不同设备和浏览器测试

## 更新日志

### v1.0.0 (2026-02-28)
- 创建完整的设计系统
- 实现 CSS 变量系统
- 添加常用组件样式
- 改进 Dashboard、Photos、System 页面
- 支持深色模式
- 添加响应式设计
