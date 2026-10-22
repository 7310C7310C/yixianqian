# ADR 0004：媒体经服务器代理 R2

## 状态
已采用（P0）

## 背景
照片属于私人物件，需求明确要求浏览器不得直接取得 R2 URL；同时要控制格式、
大小与显示缩图。

## 决策
- 上传：`POST /api/media`（JSON）或 `POST /media/upload`（HTML form），
  multipart 字段 `file`。服务器读入上限 `MEDIA_MAX_BYTES`（默认 10 MiB），
  以 magic bytes + `image.DecodeConfig` 验证 JPEG/PNG；HEIC/HEIF 明确拒绝。
- 缩图：以 `golang.org/x/image/draw` CatmullRom 缩至 `MEDIA_THUMB_SIZE`
  （默认 512），白底 JPEG q=82；原始档保持原样。
- 存储：MinIO Go SDK 连 S3 兼容 R2。key 由 server 产生
  `media/{userID}/{uuid}.{jpg|png}`，缩图为 `_thumb.jpg`；bucket 不公开。
- DB `media` 记录 key、content type、大小、尺寸；浏览器只看到
  `/media/{id}`。
- 读取：`GET /media/{id}` 要求登录，且只能读自己的 media；`?size=thumb`
  回缩图。回应 `Content-Length`、`Content-Type`、`private, max-age=300`，
  不使用 presigned URL、不暴露 bucket。
- 失败补偿：R2 上传成功但 DB 写入失败时删除物件；缩图失败时删原始档。

## 后果
- 所有图片流量经过 Go 服务，P0 可接受；量大时可加 CDN 或短效签名 URL，
  但需重新检视隐私边界。
- 缩图在写入时产生，读取便宜；代价是存储加倍。
- EXIF 方向尚未校正，HEIC 不支持。
