---
name: warn-env-file-edit
enabled: false
event: file
action: warn
conditions:
  - field: file_path
    operator: regex_match
    pattern: \.env$|\.env\..*$
---

🔐 **环境配置文件编辑警告**

你正在编辑环境配置文件。请注意：

- ✅ 确保敏感信息（API Key、密码等）不要硬编码
- ✅ 确认该文件已在 .gitignore 中
- ✅ 不要提交真实的密钥到代码仓库

**Relive 项目敏感配置**：
- QWEN_API_KEY - 阿里通义千问 API 密钥
- DATABASE_URL - 数据库连接字符串
- NAS_PHOTO_PATH - NAS 照片路径
