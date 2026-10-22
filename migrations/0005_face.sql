-- 本地人脸检测结果（YuNet/Haar），只增列，不改既有数据。
-- face_checked=false 表示尚未检测（含迁移前的老照片），由上传流程或
-- cmd/facebackfill 补齐；没有 checked 照片时 has_face 一律按 false 处理。
ALTER TABLE profile_photos
    ADD COLUMN IF NOT EXISTS face_checked boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS has_face boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS face_center_x real,
    ADD COLUMN IF NOT EXISTS face_center_y real,
    ADD COLUMN IF NOT EXISTS face_box_w real,
    ADD COLUMN IF NOT EXISTS face_box_h real;

-- 批量补齐只扫描未检测照片。
CREATE INDEX IF NOT EXISTS profile_photos_face_unchecked_idx
    ON profile_photos (id) WHERE NOT face_checked;
