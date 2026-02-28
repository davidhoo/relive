---
name: block-dangerous-rm
enabled: true
event: bash
pattern: rm\s+-rf\s+(/|~|\*|\.\.|\$)
action: block
---

🛑 **危险的删除命令已阻止！**

检测到可能造成严重后果的 `rm -rf` 命令。

**为什么被阻止**：
- 可能删除重要系统文件
- 可能删除 Relive 项目数据
- 不可逆操作

**建议**：
1. 仔细检查要删除的路径
2. 使用更具体的路径（避免通配符）
3. 考虑先移动到临时目录而不是直接删除
4. 对于 Docker 清理，使用 `docker system prune` 等安全命令
