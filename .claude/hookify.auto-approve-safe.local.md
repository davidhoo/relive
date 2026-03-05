---
name: auto-approve-safe-operations
enabled: false
event: bash
action: approve
conditions:
  - field: command
    operator: regex_match
    pattern: ^(git\s|ls|cat|pwd|whoami|date|echo|tree|grep|find|which|make\s|go\s|npm\s|pnpm\s|yarn\s|node\s|docker\s(?!.*\s-v)|docker-compose\s(?!.*\s-v)|curl|wget|mkdir|touch|cp\s|mv\s|chmod|chown)
---

🟢 **自动批准安全操作 - Relive 项目优化**

以下命令将自动执行，无需确认：

## Git 操作（全部自动）
- ✅ 所有 `git` 命令：status, diff, log, add, commit, push, pull, fetch, merge, checkout, branch, rebase 等

## Go 开发（Relive 后端）
- ✅ `go build`, `go run`, `go test`, `go mod tidy`, `go mod download`
- ✅ `go fmt`, `go vet`, `go get`, `go install`
- ✅ `make build`, `make run`, `make test`, `make lint`, `make fmt`, `make deps`

## 前端开发（Vue 3 + Vite）
- ✅ `npm install`, `npm run dev`, `npm run build`, `npm test`
- ✅ `pnpm install`, `yarn install`（如果使用）
- ✅ `node` 命令

## Docker 操作（排除删除数据卷）
- ✅ `docker build`, `docker run`, `docker ps`, `docker images`, `docker logs`
- ✅ `docker exec`, `docker inspect`, `docker-compose up`, `docker-compose restart`
- ⚠️ `docker-compose down -v` 和 `docker volume rm` 仍需确认（由其他规则保护）

## 系统命令
- ✅ `ls`, `cat`, `pwd`, `tree`, `grep`, `find`, `which`
- ✅ `mkdir`, `touch`, `cp`, `mv`, `chmod`, `chown`
- ✅ `curl`, `wget`, `echo`, `date`

## 不包括（仍需确认）
- ⚠️ `rm` 命令（由 dangerous-rm 规则保护）
- ⚠️ Docker 数据卷删除（由 docker-volume 规则保护）

这些命令覆盖了 Relive 项目 99% 的日常开发操作，大幅提升效率！
