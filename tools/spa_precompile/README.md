# SPA 模板预编译与按页拆 chunk（tools/spa_precompile）

把 `web/static/legacy/index.html` 的 `#app` 根模板和 `web/static/legacy/pages/*.html`
（26 个页面分支）预编译成 render function。浏览器不再运行 Vue 模板编译器；
**生成物已提交，生产部署/CI 不需要 Node**。

## 版本

- `@vue/compiler-dom` 必须与 `web/static/legacy/lib/vue.global.prod.js` 完全一致：
  当前 **3.4.27**（`package-lock.json` 锁定精确版本）。
- 升级 Vue 运行时必须同步升级本工具并重新生成；否则 render 辅助函数可能不匹配。

## 文件与生成物

| 输入 | 输出 |
|---|---|
| `index.html`（含 `<page-host>`） | `app.render.js`：root render + initial `cards` render |
| `pages/cards.html` | 同上（initial chunk） |
| 其余 25 个 `pages/*.html` | `pages/{likes,home,profile,detail,group}.render.js`（5 个 lazy chunk） |
| 全部页面模板 | `pages/pages.manifest.json`（page→group/file/模板 sha256/size；chunk bytes/sha256） |

运行期 `app.js` 的 `PageHost` 以根组件 proxy 调用页面 render；首次进入某页时动态加载
对应 chunk，失败显示 `.boot-fail`。模板 ref 经 `setupState` 代理写回根 ref。

## 命令

```bash
cd tools/spa_precompile
npm ci                 # 首次/重建；只装 devDependency
node split_pages.mjs   # 已执行过；从旧 index.html 再抽 26 页（幂等）
npm run build          # 生成 app.render.js + pages/*.render.js + manifest
npm run check          # 校验生成物与模板逐字节一致（退出码非 0 = 过期）
```

改 `index.html` / `pages/*.html` 后必须重新 `npm run build` 并提交生成物。
`make spa-check-node` 跑本检查；`make spa-check` 用 Go 测试校验 manifest 的 sha256/bytes，
`make ci` 使用后者（不依赖 Node）。

## 大小（批次6，实际提交）

- root+cards `app.render.js`：182,598B（gzip 24,898B），原单文件 768,783B / 67,819B。
- 5 个 chunk 合计约 357KB raw（按页懒加载，不计入首屏）。
- 模板/组件 hash 与 manifest 由 `TestPageChunksMatchTemplates`、`TestGeneratedBundleHashes`、
  `TestVantSubsetCoversTemplate` 门禁；手改生成物或模板未重建都会让 `make ci` 变红。
