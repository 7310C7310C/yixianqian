-- 站点公开资产（当前：小天使二维码）。PM 2026-10-29：注册页展示二维码，
-- 后台可上传/更换。资产很小（<=2MB），直接存 PostgreSQL，避免公开读取依赖 R2
-- 或把 bucket URL 暴露给前端；只由 /angel-qr 公开串流。
CREATE TABLE IF NOT EXISTS site_assets (
    key          text PRIMARY KEY,
    content_type text NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png')),
    byte_size    bigint NOT NULL CHECK (byte_size > 0 AND byte_size <= 2097152),
    width        integer NOT NULL CHECK (width > 0),
    height       integer NOT NULL CHECK (height > 0),
    data         bytea NOT NULL,
    etag         text NOT NULL,
    updated_by   bigint REFERENCES users (id) ON DELETE SET NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
