# Network Error 问题 - 最终修复

## 问题原因

使用了错误的 API 辅助函数导致响应结构不匹配。

### 错误代码
```typescript
import request from '@/utils/request'  // ❌ axios 实例

const response = await request.get('/api/v1/config/photos.scan_paths')
// response.data = { success: true, data: {...} }
// 这是 AxiosResponse 结构
```

### 正确代码
```typescript
import { http } from '@/utils/request'  // ✅ 封装的辅助函数

const response = await http.get<BackendConfigResponse>('/config/photos.scan_paths')
// response = { success: true, data: {...} }
// http 已经提取了 response.data，返回 ApiResponse
```

## 修复内容

**文件:** `frontend/src/api/config.ts`

### 主要更改

1. **导入语句**
   ```typescript
   // Before
   import request from '@/utils/request'

   // After
   import { http } from '@/utils/request'
   ```

2. **API 调用**
   ```typescript
   // Before
   const response = await request.get('/api/v1/config/photos.scan_paths')
   const apiResponse = response.data
   if (apiResponse.success && apiResponse.data && apiResponse.data.value) {
     return JSON.parse(apiResponse.data.value)
   }

   // After
   const response = await http.get<BackendConfigResponse>('/config/photos.scan_paths')
   if (response.data && response.data.value) {
     return JSON.parse(response.data.value)
   }
   ```

3. **类型安全**
   - 添加 `BackendConfigResponse` 接口
   - 使用泛型类型参数

## 为什么会有 Network Error

浏览器报告 "Network Error" 是因为：
1. `request.get()` 直接返回 axios AxiosResponse
2. 代码试图访问不存在的嵌套属性
3. JSON.parse 接收到 undefined
4. 抛出异常被 axios 拦截器捕获
5. 显示为 "Network Error"

## 测试步骤

### 1. 强制刷新浏览器
```
访问: http://localhost:5173/config
按键: Cmd+Shift+R (Mac) 或 Ctrl+Shift+R (Windows)
```

### 2. 测试添加路径
1. 点击"添加路径"按钮
2. 填写表单：
   - 名称：Test Path
   - 路径：/tmp
3. 点击"验证"按钮
   - 应该显示：✅ 路径有效
4. 勾选"设为默认路径"
5. 点击"保存"
   - 应该显示：保存成功

### 3. 验证功能
- 列表中应该显示新添加的路径
- "上次扫描"显示"从未扫描"
- 可以点击"编辑"修改
- 可以点击"删除"删除
- 可以点击"设为默认"改变默认路径

### 4. 测试照片页面集成
1. 访问：http://localhost:5173/photos
2. 应该看到路径选择下拉框
3. 默认路径应该被预选
4. 可以切换路径
5. 点击"扫描照片"应该能正常工作

## 如果还有问题

### 检查浏览器控制台
```
1. 按 F12 打开开发者工具
2. 切换到 Console 标签
3. 查看是否有错误信息
4. 切换到 Network 标签
5. 检查 API 请求是否成功
```

### 手动重启前端
```bash
# 如果 Vite HMR 没有自动更新
cd frontend

# 停止服务器 (Ctrl+C)

# 重新启动
npm run dev
```

### 清除浏览器缓存
```
Chrome: Cmd+Shift+Delete (Mac) 或 Ctrl+Shift+Delete (Windows)
选择"缓存的图片和文件"
时间范围：过去 1 小时
点击"清除数据"
```

## 验证修复

使用浏览器控制台测试 API：

```javascript
// 打开 http://localhost:5173 的控制台
const testApi = async () => {
  const response = await fetch('http://localhost:8080/api/v1/config/photos.scan_paths')
  const data = await response.json()
  console.log('Response:', data)

  if (data.data && data.data.value) {
    const paths = JSON.parse(data.data.value)
    console.log('Paths:', paths)
  }
}
testApi()
```

## 与其他 API 文件的一致性

现在 `config.ts` 与其他 API 文件保持一致：

```typescript
// photo.ts
import { http } from '@/utils/request'
export const photoApi = {
  getList(params) {
    return http.get('/photos', { params })
  }
}

// config.ts (已修复)
import { http } from '@/utils/request'
export const configApi = {
  getScanPaths() {
    return http.get('/config/photos.scan_paths')
  }
}
```

## 响应结构对比

### http.get() 返回值
```typescript
{
  success: true,
  data: {
    id: 1,
    key: "photos.scan_paths",
    value: "{\"paths\":[...]}"  // JSON string here
  },
  message: "Config retrieved successfully"
}
```

访问路径：`response.data.value` ✅

### request.get() 返回值（错误的方式）
```typescript
{
  data: {
    success: true,
    data: {
      id: 1,
      key: "photos.scan_paths",
      value: "{\"paths\":[...]}"
    }
  },
  status: 200,
  headers: {...}
}
```

访问路径：`response.data.data.value` ❌

---

**状态:** ✅ 已修复
**修复时间:** 2026-03-02
**影响范围:** Config 页面的所有功能
