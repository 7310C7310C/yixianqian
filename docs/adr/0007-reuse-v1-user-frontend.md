# ADR 0007：用户端沿用 v1 Vue 3 + Vant SPA

- 状态：已接受
- 日期：2026-10-22
- 决策者：Lead
- 相关：`docs/PROJECT_CHARTER.md`、`docs/product-spec.md`、ADR 0002

## 背景

一线牵 v1 的用户端是一个已在真实活动使用的 Vue 3 + Vant SPA，包含完整的四 tab、
卡片滑动、喜欢/点赞/留言、活动报名、分组志愿与一对一结果流程。v2 需要保留这些
既有行为与视觉，同时把登录、session、数据与媒体迁到 Go/PostgreSQL/R2。

## 决策

1. **用户端沿用 v1 Vue 3 + Vant SPA**，保留其 DOM、CSS、页面状态机、AJAX 行为与
   tab 交互；不重写用户界面。
2. Go 后端提供：
   - 手机号 + 密码登录、启用链接、session 与 CSRF；
   - v1 SPA 所需的 `/legacy/api/*` 兼容 API（旧 JSON 形状）；
   - 媒体代理 `/media/{id}`、`/cards/photo/{id}`、`/profile/photo/{id}`、
     `/activities/{id}/poster`；
   - 管理端继续使用 Go SSR（ADR 0002）。
3. 前端只做必要适配：
   - 去掉 Feishu SDK 登录路径，未登录跳转 `/login`；
   - API 前缀与 JSON 形状适配；
   - 图片 URL 改走应用媒体代理；
   - 替换 Feishu 专属文案、注册入口与名片描述。
4. **不维护第二套完整用户 UI**。若未来真实设备数据表明需要兼容层，另立 ADR 评估
   转译与 CSS fallback。
5. 用户端 SPA 需要 JavaScript；登录、注册、启用、密码与管理端仍保持服务端可用。

## 兼容性

按 v1 前端原样复用（不转译、不补 flex-gap fallback）：

- 完整支持：iOS 14.5+ / Safari 14.1+、Chrome/Android WebView 84+、Firefox 74+。
- 低于此级别可能白屏或版面错乱；不冒充支持。
- 如未来真实设备数据显示旧设备占比显著，另立 ADR 做兼容层。

## 后果

正面：
- 用户端视觉、AJAX、tab/滚动/子页行为直接继承 v1，UI 一致性高。
- 不需要维护两套用户页面与两套模板。

代价：
- 需要维护 `/legacy/api/*` 兼容层（数据形状、openid/ID 映射、媒体代理、CSRF）。
- 引入 Vue/Vant 资产；首屏与低端机性能需实测。
- 用户端依赖 JavaScript；最低浏览器版本高于纯 SSR 页面。

## 边界

- 用户页仅由 v1 SPA 提供，不另建 Go 用户页模板、样式、脚本或路由；`/cards`、`/likes`、`/activities`、`/profile` 等旧路径直接返回 404。
- 管理端、登录、注册、启用、密码与错误页使用 Go SSR，不依赖用户端 SPA。
