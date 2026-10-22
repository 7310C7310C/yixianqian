# 文档索引与权威顺序

- 最后审阅：2026-10-22
- 维护者：Lead（Project Lead / 产品工程负责人）
- 目的：让 AI 与团队成员先看对文件，避免把历史/草案/未勾选清单当成当前状态。

## 1. 读取顺序（冲突时由上到下）

1. `docs/PROJECT_CHARTER.md`：使命、范围、架构硬约束（Source of Truth）。
2. `docs/adr/*.md`：已接受的架构决策；新决策新增 ADR，不重写旧 ADR。
3. `docs/STATUS.md`：当前 staging/生产状态与已知缺口（只在里程碑/验收更新）。
4. `docs/product-spec.md`：验收目标清单；`[ ]` 是待验收目标，不是实时进度。
5. `docs/milestones.md`：里程碑定义与当前阶段。
6. `docs/runbooks/staging.md`：实际部署、回滚、smoke 步骤。
7. `docs/data-migration.md`：testapp 数据迁移设计与对账口径。
8. `docs/agent-task-template.md`：派工/交付契约模板。
9. `README.md`：稳定项目概览；不写即时进度。
10. `CHANGELOG.md`：已交付变更摘要。

## 2. 文件状态

| 文档 | 状态 | 谁决定内容 | 何时更新 |
|---|---|---|---|
| `PROJECT_CHARTER.md` | 现行，Source of Truth | PM + Lead | 使命/范围/架构硬约束改变；需版本与日期 |
| `adr/*.md` | 现行或已被取代（看状态） | Lead/决策作者 | 新架构决策；旧 ADR 只加 superseded 标记 |
| `STATUS.md` | 现行状态快照 | Lead | 里程碑、发布、staging 验收；必须写日期 |
| `product-spec.md` | 验收目标 | PM 定范围；Lead 维护勾选 | 需求变更或验收通过；勾选必须有证据 |
| `milestones.md` | 里程碑定义 | Lead | 进入/完成里程碑；不写每日进度 |
| `runbooks/staging.md` | 现行操作手册 | Dev Lead/Lead | 部署拓扑、命令、回滚改变 |
| `data-migration.md` | 迁移设计/基线 | Analyst/Dev Lead | schema 或迁移/对账口径改变 |
| `agent-task-template.md` | 派工模板 | Lead | 团队角色/交付契约改变 |
| `README.md` | 稳定概览 | Lead | 架构/目录/支持矩阵等稳定事实改变 |
| `CHANGELOG.md` | 变更历史 | Lead | 每次发布/里程碑交付 |
| `AGENTS.md` | 项目规则 | Lead | 规则/结构/文档治理改变 |

## 3. 更新规则

- **没有自动更新。** 不存在后台 prompt/定时任务会改这些文件；只有被明确派工的 agent 才能写入。
- Agent 不得自行修改 `PROJECT_CHARTER`、`product-spec` 勾选、`STATUS` 或 ADR 状态；只能在任务书写明时提案，由 Lead/PM 决定。
- 功能完成不等于文档完成：任务必须报告“需要更新哪些 docs”；Lead 在合并/验收时检查。
- 历史文件若不再适用，必须加 `> 历史文件（日期/原因）` 或删除；不要让 AI 猜。
- 发现代码与文档冲突：先停下来报告，按权威顺序处理；代码不能自动覆盖 Charter。
- `STATUS.md` 是唯一“当前进度”来源；README 与 product-spec 不再承担即时状态。
