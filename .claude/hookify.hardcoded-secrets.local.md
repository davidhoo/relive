---
name: warn-hardcoded-secrets
enabled: true
event: file
action: warn
conditions:
  - field: file_path
    operator: regex_match
    pattern: \.(go|js|ts|tsx|jsx|yml|yaml)$
  - field: new_text
    operator: regex_match
    pattern: (API_KEY|SECRET|TOKEN|PASSWORD|QWEN_API)\s*[:=]\s*["'][^"']{20,}["']
---

🔐 **检测到可能的硬编码密钥！**

在代码中发现可能的硬编码敏感信息。

**Relive 项目安全建议**：

✅ **正确做法**：
```go
// 从环境变量读取
apiKey := os.Getenv("QWEN_API_KEY")

// 或从配置文件读取
config.Load(".env")
```

❌ **错误做法**：
```go
// 不要这样做！
apiKey := "sk-abc123..."
```

**Relive 敏感配置**：
- `QWEN_API_KEY` - 通义千问 API 密钥
- `DATABASE_URL` - 数据库连接
- `NAS_PHOTO_PATH` - 照片路径（可能包含敏感路径）

**提醒**：
- 所有密钥应该从环境变量读取
- .env 文件必须在 .gitignore 中
- 提供 .env.example 作为模板
