# SPA 根模板预编译（tools/spa_precompile）

把 `web/static/legacy/index.html` 里 `#app` 的 in-DOM 模板预编译成
`web/static/legacy/app.render.js`，浏览器不再为 176KB 根模板跑 Vue 运行时编译器。
生成物已提交；**生产部署不需要 Node**。

## 版本

- `@vue/compiler-dom` 必须与 `web/static/legacy/lib/vue.global.prod.js` 完全一致：
  当前 **3.4.27**（`package-lock.json` 锁定精确版本）。
- 升级 Vue 运行时必须同步升级本工具并重新生成；否则 render 辅助函数可能不匹配。

## 命令

```bash
cd tools/spa_precompile
npm ci            # 首次/CI 校验；只装 devDependency
npm run build     # 生成 ../../web/static/legacy/app.render.js
npm run check     # 校验生成物是否与当前 index.html 模板一致（退出码非 0 = 过期）
```

改 `index.html` 的 `#app` 模板后必须重新 `npm run build` 并提交生成物；生成物头部含
`template-sha256=<hash>` 与 compiler 版本，`npm run check` 会重算并比对全文。

## 运行时

`app.render.js` 是普通 classic script（IIFE，无 `eval`/`new Function`，CSP 不变）：

```js
window.__YXQ_RENDER__ = render // 由 @vue/compiler-dom 生成
```

`index.html` 在 `app.js` 之前以 `defer` 加载它；`app.js` 的根 options 使用
`render: window.__YXQ_RENDER__`。若文件缺失/加载失败，`window.__YXQ_RENDER__`
为 undefined，Vue 回退到 in-DOM 模板的运行时编译（功能不回归，只慢）。

## 大小

当前：模板 176,371B → render 768,618B（gzip ≈69.6KB）。大是因为根模板包含全部页面
分支；后续可把页面拆成组件进一步缩小（需构建流程决策）。
