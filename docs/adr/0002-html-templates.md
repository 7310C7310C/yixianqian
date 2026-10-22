# ADR 0002：管理端与登录流程使用 html/template

## 状态
已采用

## 背景
项目需要一套无需前端构建链、可直接随 Go binary 发布的服务端页面，用于管理端、
登录、注册、启用、密码与错误页。用户端页面由既有的 v1 Vue 3 + Vant SPA 提供，
不在此决策范围内（见 ADR 0007）。

## 决策
使用标准库 `html/template`，以 `webassets` 套件 `embed` 模板与静态文件：

- `layout.tmpl` 提供统一外框；管理端页面共享 `admin_nav` 等 partial。
- 启动时解析并 clone 每个页面模板，失败即拒绝启动。
- 输出先写入 buffer，避免模板错误吐出半页。
- 模板只服务服务端页面；不引入前端框架或前端构建步骤。
- 管理端与登录/注册/启用/密码页在没有 JavaScript 时仍可完成核心流程。

## 后果
- 无 codegen 依赖，`go build` 即可产出单一 binary。
- 模板数据是执行期 `PageData`，字段错误由 render 测试发现。
- 安全边界集中在 Go handler：session、CSRF、RBAC 与审计不依赖模板。
- 若日后服务端页面规模变大，可平移至 templ，路由与 handler 不需改。
