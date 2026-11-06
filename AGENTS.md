# AGENTS.md — yixianqian

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-11-06
sources:
  - Makefile
  - .github/workflows/**
  - tools/perf/**
  - deploy/nginx/**
-->

> **本文件是项目规则的唯一入口。** 开工先读本文件；当前状态看 `docs/STATUS.md`；
> 产品范围与验收看 `docs/PRODUCT.md`；现行决策看 `docs/DECISIONS.md`；
> 部署/回滚看 `docs/runbooks/staging.md`。机械事实（路由、env、migration、Makefile target）
> 以 code/config/Makefile 为准，需要索引时看 `docs/reference/REFERENCE.md`。

## 文档原则

- **docs 只写 AI 推不出来的东西**：意图、非目标、硬约束、验收标准、当前阻塞、现行决策、操作顺序与陷阱。
- **code / config / migrations / Makefile 是机械事实的唯一真相**；git log 与 commit message 是历史的唯一真相。
- 不在 docs 复制路由表、env 表、目录结构、逐批测试证据、部署日志或旧版本快照。
- 同一事实只允许一个 owner 文档；其他文档只链接，不复制。
- **本文件只收跨功能红线**；单功能行为/UI 细节写 `docs/DECISIONS.md` 或测试，不写这里。
- `STATUS` 与 `runbook` 只写当前状态；历史批次、日期标题/行、旧快照一律不进 docs。
- 不在 `STATUS` 写死运行版本/SHA/迁移/人数；需要时跑 `make status`，避免每次部署后补 docs-only commit。
- 行数预算与「禁止历史标题」由 `tools/check_docs.py` 检查；预算在 `tools/docs_budget.json`，超限即 CI 红。

## 不可违反的硬约束

- 不写密钥；密码、token、cookie、`.env`、R2 key 不得进入 repo、日志、报告或文档。
- 用户端复用 v1 Vue 3 + Vant SPA；管理台只经同源 iframe，顶层 `/admin` 一律 303 到 `/?admin=1`。
- 消息中心/动态/留言/全员广播的现行行为与不变式见 `docs/DECISIONS.md` §2.5；本文件不复制。
- 不恢复已废弃方案：`/welcome` 页面、独立 `/login` 页（并入首页弹框）、首页二维码、顶层 `/admin` SSR/404、`/legacy/api` 边缘缓存、`vant.full.min.*`、飞书 runtime、第二套用户 UI、首卡自我引导气泡（`self-guide-tip`；首卡已自带「这是你 · 他人视角」标签）；不恢复品牌启动页/静态岛，首屏保持旧 hero 图；hero 在 App 骨架挂载后交接（不再等数据/首卡绘制），首卡随后渲染。
- PostgreSQL 是唯一权威；R2 只存私有媒体，浏览器只经 `/media/{id}`；不新增 Redis/消息队列，正确性不依赖缓存。
- 城市字段只接受 vendored 数据集值（注册/资料均必填）；所有上传统一转 JPEG（HEIC 先 ispe 预检再 heif-convert）。细节见 `docs/DECISIONS.md` §1.2、§2.4。
- migration 只增不改；回滚 binary 不等于回滚 schema；生产大表索引另行走手工窗口。
- staging 验证先于生产；生产切换、删数据、凭证/权限变更、验收勾选必须 PM 批准。
- 不 force-push、不重写已推送历史；commit message 用简体中文，并带 `Docs:` 或 `Docs-Impact: none（理由）`。
- 改动 `web/static/**`（含生成物）必须在同一提交 bump `internal/httpserver/templates.go` 的 `assetVersion`，并同步 admin CSS `@import`。
- 公开匿名页的扫描防护契约（除 `<title>` 外源码 0 汉字、引用 CSS/JS 0 汉字）不得回退；新增公开页先看 `docs/DECISIONS.md`。

## 标准交付流水线

顺序固定：**改 → 本地验证 → commit → push → CI 绿 → 自动部署 → 部署后 smoke → 批次报告**。

1. **改**：小改动在 `main`；较大改动用独立 worktree/分支，维护者本地合并后 push。
2. **本地验证**：每个 edit 后 `make fast`；每个 commit 前 `make ci` 一次；push 前必跑 `make prepush`，且必须在**最后一次改动之后**跑。注意 `make ci` 不包含 `docs-check-stale`，只有 `make prepush` 包含；static/JS 改动后漏跑 prepush 会让 Docs workflow 红。
3. **commit**：简体中文；带 `Docs:` 或 `Docs-Impact: none（理由）`；改 `docs/PRODUCT.md` 必须带 `PM-Approved: <摘要>`。
4. **push**：常规/高风险须在 push 前拿到独立 Reviewer 对本地 commit 的 PASS；低风险快速通道（纯文案/样式/排序/静态资源，不涉 auth/权限/数据/API/schema/支付）可自测 + smoke 后直接 push，并在 commit 说明范围与风险。
5. **CI 绿**：最近一次**代码**提交的 `lint-build`/`unit`/`integration`（docs-only 不触发 CI）；`integration` 三库并行（`internal/db` + httpserver 按测试名奇偶分片）。`Docs` 每次 push 跑 stale：命中 `sources` 的文件必须在**同一提交**更新对应文档，trailer 不豁免；旧 commit 的 Docs 红不会因后续 docs-only 提交消失。
6. **自动部署**：`yxq-autodeploy.timer` 每 60 秒检查 `origin/main`；CI + Docs 绿后构建/重启/health，失败自动回滚。代码提交必须带 `Reviewed-By:` trailer；docs-only 豁免。
7. **部署后 smoke**：先跑 `make status`，以 `/healthz` 的 `version=staging-<sha>` 对齐最近一次**代码**部署提交；docs-only 只推进 `deployed.sha`，不重启。
8. **批次报告**：改动、证据、部署状态、残余风险；历史留在 commit message。

硬约束：**push ≠ 已部署**；**CI 红不部署**；不得 `[skip ci]`、绕过 CI 或在红/取消时部署。人工部署仅用于回滚/热修；人工回滚后必须同步 `/opt/yixianqian-next/deployed.sha`。

## 验证与证据

- 先定义可证伪判准（命令 + 期望输出），再改；报告含改动、证据、未完成/阻塞、残余风险、回滚方式。
- 失败先修根因；同一红灯 3 轮未解，停下写根因与选项给 PM，不无限重试。
- 生产/数据/权限类改动必须另加 Reviewer + security-gate/Auditor；不得自审。

## 前端与性能红线

- 顶层 SPA 默认虚拟历史栈：子页/浮层/应用内返回统一用 `pushHistory`、`hist.replace`、`handleBack`；不得直接调用 `window.history.pushState/replaceState/back`。`native_admin` 与 `?history=legacy` 例外。
- 前端交互不变量（照片同步、门禁返回栈、签到/匹配入口）见 `docs/DECISIONS.md` §4.3；本文件只留性能与历史栈红线。局部 SPA 规则见 `web/AGENTS.md`。
- 热路径（`web/static/legacy/**`、`deploy/nginx/**`、`internal/httpserver/{middleware,handlers_media,media_serve,legacy_ui,readcache,app}.go`、DB home/cards/profile/notifications/照片授权、热索引 migration）必须给 `make perf-bench`/`make perf-lcp` 前后数字；前后必须对同一基线提交（通常是本次改动前的 HEAD），不是更早的历史基线；p95 不得比基线差 >10%。
- 不得移除：边缘 keepalive、静态 gzip/identity 分键缓存、TLS session ticket、缩略图磁盘缓存、媒体 ETag/304、10s 读缓存 + epoch、`0008`–`0010` 热索引；不得给 `/legacy/api` 加边缘 `proxy_cache`，cache key 不含 `$cookie_`。
- 不得把 `app.css`/`app.min.js`/`app.render.js` 大段内联回壳；不得退回 `vant.full.min.*`；生成物必须走 `make spa-check`。
- 预算只改 `tools/perf/budgets.json` 并走 Review；`notes` 与 commit 都要记录旧值→新值、实测值与理由，基线抬高不得只为凑上限（新增真实功能、UI 迭代、视觉/交互修复（含查看器/过渡/通知/红点/未读计数）按实测重设 baseline，连续迭代每次记录实测值，不得复用旧 baseline 掩盖回归）；没有自动更新命令。当前值与变更记录只查该文件，不在本文件或 `tools/README.md` 复制数值。

## 文档地图

| 文件 | owner | 用途 |
|---|---|---|
| `AGENTS.md` | maintainer | 本文件；唯一项目级规则入口 |
| `README.md` | maintainer | 稳定概览、架构、本地开发 |
| `docs/PRODUCT.md` | pm | 使命、非目标、硬约束、验收清单 |
| `docs/STATUS.md` | maintainer | 阶段、已实现 vs 已验收、P0/缺口；运行状态查 `make status` |
| `docs/DECISIONS.md` | maintainer | 现行决策、理由、不要做什麼；历史看 git log |
| `docs/runbooks/staging.md` | maintainer | staging 部署/回滚/smoke/人脸/腾讯/迁移附录 |
| `docs/reference/REFERENCE.md` | maintainer | 自动生成机械索引；勿手改 |
| `tools/README.md` | maintainer | 生成物重建顺序与性能护栏命令 |
| `CLAUDE.md` | maintainer | Claude Code 入口指针（生成） |

局部规则：`web/AGENTS.md`（SPA）、`internal/httpserver/AGENTS.md`（HTTP/API），只在该目录工作时读。
新增/删除治理文档必须同步本表；`CLAUDE.md` 与 `docs/reference/REFERENCE.md` 由 `make docs-gen` 刷新。

## 变更 → 文档

| 变更 | 需更新 |
|---|---|
| 路由/env/migration/Makefile target | 跑 `make docs-gen` 刷新 REFERENCE；语义变化才改对应文档 |
| 产品范围/验收 | `docs/PRODUCT.md`（PM 批准 + commit 带 `PM-Approved:`）→ `docs/STATUS.md` |
| 单功能行为/UI 细节 | `docs/DECISIONS.md` 或测试；不要写进本文件 |
| 当前阶段/P0/缺口 | `docs/STATUS.md` |
| 部署/证书/CI/运维 | `docs/runbooks/staging.md`、`AGENTS.md` |
| 架构/安全/媒体/匹配决策 | `docs/DECISIONS.md`；重大变化需 PM 批准 |
| 生成物/构建顺序 | `tools/README.md` |

## 任务契约

1. **开工检查**：`git status`、`git log --oneline -10`、读 `docs/STATUS.md` 与相关代码；不要假设旧文档正确。
2. **可证伪判准**：写清命令、期望输出、正常与边界路径。
3. **证据**：命令 + 关键输出；没有输出视为未验证。
4. **文档影响**：列出要更新的文档；无影响写 `Docs-Impact: none（理由）`。
5. **报告与回滚**：报告改动、证据、阻塞、残余风险；说明如何撤销（schema 只增不改，回滚 binary 不等于回滚数据）。

## 工具兼容

`AGENTS.md` 是通用入口；Codex、opencode、Cursor、Copilot 等可直接读取。Claude Code 用 `CLAUDE.md` 的 `@` 导入指向本文件；Gemini CLI/Aider 等未自动读取的工具，由使用者先指向本文件。不要为每个工具复制一份规则。
