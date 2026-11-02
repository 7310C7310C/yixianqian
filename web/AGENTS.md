# SPA 局部规则（web/static/legacy）

本文件在 `web/`，覆盖 `web/static/legacy` 的 SPA；项目级流程/门禁见 `../AGENTS.md`，行为决策见 `../docs/DECISIONS.md`。

## 历史栈

- 顶层 SPA 默认虚拟历史栈：子页/浮层/应用内返回统一用 `pushHistory`、`hist.replace`、`handleBack`；不得直接调用 `window.history.pushState/replaceState/back`。
- `native_admin` 与 `?history=legacy` / `localStorage.yxq_history_mode='legacy'` 例外，保持真实历史。
- 门禁（身份证/地区/人脸）直接打开资料编辑时，保存后的 `dropCurrent` 保留父级 tab 作为返回落点；已进入 `profile_edit`/`profile_detail` 后，迟到的 403 不得再弹门禁浮层；缺失地区字段就地红字提示。

## 资料与卡面

- 资料照片上传/删除/换封面后，同步 `selfCard`/`cards` 的 `photo`/`photos`/`photos_thumb`/`photos_meta`，不得依赖整页刷新。
- 门禁/注册表单错误只给一条通用提示；注册二维码未长按时点击选项，选项文字也要标红。

## 消息中心

- 消息页默认动态流；喜欢/留言子页共用同一列表视觉；tabbar 数字只表示收到留言未读，动态未读只在页面内小点。
- 单条已读、全部已读、进入留言后的已读都必须持久，切页返回不得复活；卡上留言不跳卡片，只保留内联回复。

## 生成物与首屏

- 任何 `web/static/**` 变更必须 bump `internal/httpserver/templates.go` 的 `assetVersion`，并同步 admin CSS `@import`。
- 生成物必须走 `make spa-check`；不得手改 `app.min.js`/`app.render.js`，不得把大段 CSS/JS 内联回壳，不得退回 `vant.full.min.*`。
- 不恢复品牌启动页/静态岛，首屏保持旧 hero 图 + 真实卡绘制后交接；不恢复首卡自我引导气泡。

## 验证

`make fast`；改动较大时 `make spa-check`；浏览器回归用 `tools/browser/spa_user_fixes_smoke.py`、`no_history_smoke.py`、`tab_scroll_smoke.py`。
