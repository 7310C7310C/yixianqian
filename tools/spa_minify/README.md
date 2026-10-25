# app.js 压缩（tools/spa_minify）

把 `web/static/legacy/app.js`（classic script，未压缩源档）在**开发期**用 esbuild 压成
`web/static/legacy/app.min.js`；壳 `index.html` 只加载 `app.min.js`，`app.js` 保留在库
作为源档与回退。**生成物已提交，部署与 CI 不需要 Node。**

批次7 A2 实测：`app.js` 338,155B / gzip 92,873B → `app.min.js` 146,739B / gzip 46,671B
（raw −57%、gzip −50%）。

## 文件

| 文件 | 说明 |
|---|---|
| `build.mjs` | 生成 `app.min.js` + `app.min.manifest.json`；`--check` 重建到临时档并逐字节比对 |
| `check.py` | CI 门禁（纯标准库）：manifest 的 source/min sha256+bytes 必须与实档一致，壳必须以 `app.min.js` 作主脚本、`app.js` 必须仍在 |
| `web/static/legacy/app.min.js` | 生成物（提交） |
| `web/static/legacy/app.min.js.gz` | 由 `make static-gzip` 生成的预压缩变体（提交） |
| `web/static/legacy/app.min.manifest.json` | 生成物元数据（源 sha、产物 sha/bytes、esbuild 版本与 target） |

## 命令

```bash
cd tools/spa_minify
npm ci                 # 首次/重建；只装 devDependency
npm run build          # 生成 app.min.js + manifest（改完 app.js 必跑）
npm run check          # 重建到临时档与已提交产物比对（exit != 0 = 过期）
```

改完 `app.js` 的固定流程：

```bash
cd tools/spa_minify && npm run build && cd ../..
make static-gzip       # 重生成 app.min.js.gz（以及其它受影响的 .gz）
make spa-check         # Go 门禁 + python3 tools/spa_minify/check.py
```

`make spa-check-node`（开发机）额外跑本目录的 `npm run check`；`make ci` 用
`make spa-check`，**不依赖 Node**。

## 约束

- 必须是 classic script 语义：用 esbuild `transform` 而不是 `bundle`，不得包成 IIFE/ESM，
  否则 `app.js` 顶层的全局（`yxqBootFailure`/`YXQ_PAGE_GROUPS`/`yxqLoadPageChunk` 等）会变作用域。
  现版本 esbuild 不会重命名顶层识别字，门禁不覆盖这一点——改 esbuild 版本后要用浏览器 smoke 复核。
- `target` 对齐项目浏览器下限（`safari14.1`/`chrome84`/`firefox74`）。注意：`safari14`（无 `.1`）
  会让 esbuild 试图降级解构而直接报错。
- 不生成 sourcemap 到 `web/`（会被 `go:embed` 一起发布）；需要时自己写到 `/tmp`。
- 只做语法等价压缩：不改字面量/分支/事件；不允许为了尺寸改语义。
