# 子代理任务书模板（强制）

每个执行角色（Dev Lead/Developer/Reviewer/Auditor）任务书至少包含以下字段，否则 Lead 不派工：

## 1. 基线与工作区
- 工作区：`/root/wt/yxq-<任务>`，分支 `feat/<任务>`。
- **必须从当前 `origin/main` 最新提交开工作区**；开工前 `git fetch && git log --oneline -1`。
- 只改本 worktree；不要改 server1 部署文件；不要 push（由 Lead 合并）。

## 2. 范围与共享文件
- 目标/非目标。
- **允许新增**：本模块的 `internal/db/<module>.go`、`internal/httpserver/<module>*.go`、模板、CSS、测试、`init(){registerModule(...)}`。
- **禁止修改**（除非任务书明确授权）：`routes.go`、`app.go`、`config.go`、`templates.go`、`layout.tmpl`、`migrations/*`、其他模块文件。
- 需要 schema/config 变更时先报告，由 Lead 处理。

## 3. 验收判准（可证伪）
- 列出用户可见流程、API、权限、错误码、数据不变量。
- 必须包含：正常路径 + 至少一个对抗/边界路径（重复、越权、并发、过期、空数据）。
- 引用 `docs/product-spec.md` 的对应条目。

## 4. 集成门禁
- 完成后、commit 前：
  1. `git merge origin/main`（或 rebase）到本分支。
  2. 在**合并后的树**上跑 `make ci`（gofmt/vet/`go test -p 1 -count=1`/build/迁移测试）。
  3. 若改到共享文件或跨模块，另跑受影响模块的 integration。
- 只测自己的 feature branch 不算通过；必须测“合并 main 后”的结果。
- 报告必须附命令 + 关键输出；无输出视为未验证。

## 5. 提交与文字
- commit message 用**简体中文**；PR 标题/说明、Release 摘要也用简体中文。
- 分支名可用英文。
- 不写密钥；不 force-push。

## 6. 交付回报
commit hash、文件清单、测试证据、未完成/阻塞、schema/依赖缺口、如何回滚。
