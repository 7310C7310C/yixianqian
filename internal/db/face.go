package db

import (
	"context"
	"fmt"
)

// ProfilePhotoFace 是一张资料照片的本地人脸检测结果。
// CenterX/CenterY/BoxW/BoxH 为相对原图的比例（0~1），未检出人脸时为 nil。
type ProfilePhotoFace struct {
	MediaID string
	Checked bool
	HasFace bool
	CenterX *float64
	CenterY *float64
	BoxW    *float64
	BoxH    *float64
}

// FaceBackfillCandidate 是批量人脸补齐需要的最小照片信息。
// 每个 MediaID 只出现一次：同一媒体被多个账号共用时只 probe 一次。
type FaceBackfillCandidate struct {
	MediaID      string
	ObjectKey    string
	ContentType  string
	UserID       int64
	LegacyUserID string
}

func faceFloat64(v *float32) *float64 {
	if v == nil {
		return nil
	}
	out := float64(*v)
	return &out
}

func faceFloat32(v *float64) *float32 {
	if v == nil {
		return nil
	}
	out := float32(*v)
	return &out
}

// SetProfilePhotoFace 写入一张照片的检测结果。只有检测成功才应调用；
// 检测失败不要写入，让 face_checked 保持 false 供后续补齐重试。
//
// 检测结果只取决于 media 本身，因此这里按 media_id 更新所有引用该媒体的
// profile_photos 行；staging 的压测账号可能共用同一 media，不能只更新一行。
// 调用方（backfill 候选查询）已按 media_id 去重，保证同一媒体只 probe 一次。
func (d *DB) SetProfilePhotoFace(ctx context.Context, mediaID string, result ProfilePhotoFace) error {
	centerX, centerY := result.CenterX, result.CenterY
	boxW, boxH := result.BoxW, result.BoxH
	if centerX == nil || centerY == nil || boxW == nil || boxH == nil {
		centerX, centerY, boxW, boxH = nil, nil, nil, nil
	}
	hasFace := result.HasFace
	if !result.Checked {
		hasFace = false
	}
	tag, err := d.pool.Exec(ctx,
		`UPDATE profile_photos
		    SET face_checked = $2, has_face = $3,
		        face_center_x = $4, face_center_y = $5,
		        face_box_w = $6, face_box_h = $7
		  WHERE media_id = $1`,
		mediaID, result.Checked, hasFace,
		faceFloat32(centerX), faceFloat32(centerY),
		faceFloat32(boxW), faceFloat32(boxH))
	if err != nil {
		return fmt.Errorf("db: set profile photo face: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ProfileHasFace 报告用户是否至少有一张已检测且检出可用人脸的照片。
// 没有任何 checked 照片时返回 false，不做“缺省为 true”的假设。
func (d *DB) ProfileHasFace(ctx context.Context, userID int64) (bool, error) {
	var has bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM profile_photos
			 WHERE user_id = $1 AND face_checked AND has_face
		 )`, userID).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("db: profile has face: %w", err)
	}
	return has, nil
}

func scanProfilePhotoFaces(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]ProfilePhotoFace, error) {
	out := make([]ProfilePhotoFace, 0, 9)
	for rows.Next() {
		var (
			item             ProfilePhotoFace
			centerX, centerY *float32
			boxW, boxH       *float32
		)
		if err := rows.Scan(&item.MediaID, &item.Checked, &item.HasFace,
			&centerX, &centerY, &boxW, &boxH); err != nil {
			return nil, fmt.Errorf("db: scan profile photo face: %w", err)
		}
		item.CenterX, item.CenterY = faceFloat64(centerX), faceFloat64(centerY)
		item.BoxW, item.BoxH = faceFloat64(boxW), faceFloat64(boxH)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate profile photo faces: %w", err)
	}
	return out, nil
}

// FaceCentersForProfile 按展示顺序返回用户每张照片的人脸数据。
// 未检测或未检出人脸的照片同样返回一行，中心/框为 nil。
func (d *DB) FaceCentersForProfile(ctx context.Context, userID int64) ([]ProfilePhotoFace, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT media_id, face_checked, has_face,
		        face_center_x, face_center_y, face_box_w, face_box_h
		 FROM profile_photos
		 WHERE user_id = $1
		 ORDER BY is_cover DESC, sort_order, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list profile photo faces: %w", err)
	}
	defer rows.Close()
	return scanProfilePhotoFaces(rows)
}

// FaceCentersForProfiles 批量返回多个用户的人脸数据，供卡片列表一次取齐，
// 避免每张卡片一次查询。
func (d *DB) FaceCentersForProfiles(ctx context.Context, userIDs []int64) (map[int64][]ProfilePhotoFace, error) {
	out := make(map[int64][]ProfilePhotoFace, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT user_id, media_id, face_checked, has_face,
		        face_center_x, face_center_y, face_box_w, face_box_h
		 FROM profile_photos
		 WHERE user_id = ANY($1)
		 ORDER BY user_id, is_cover DESC, sort_order, id`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("db: list profile photo faces by users: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			userID           int64
			item             ProfilePhotoFace
			centerX, centerY *float32
			boxW, boxH       *float32
		)
		if err := rows.Scan(&userID, &item.MediaID, &item.Checked, &item.HasFace,
			&centerX, &centerY, &boxW, &boxH); err != nil {
			return nil, fmt.Errorf("db: scan profile photo face by user: %w", err)
		}
		item.CenterX, item.CenterY = faceFloat64(centerX), faceFloat64(centerY)
		item.BoxW, item.BoxH = faceFloat64(boxW), faceFloat64(boxH)
		out[userID] = append(out[userID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate profile photo faces by users: %w", err)
	}
	return out, nil
}

// ListFaceBackfillCandidates 列出待补齐的照片。includeChecked=false 时只取
// face_checked=false 的照片；legacyUserID 非空时只取该用户。
//
// 候选按 media_id 去重：同一媒体只返回最早的一行，backfill 因此只下载/检测
// 一次；SetProfilePhotoFace 会把结果写到所有引用该媒体的 profile_photos 行。
// 这避免 staging 中共用同一 media 的批量账号被重复 probe 并放大回填耗时。
func (d *DB) ListFaceBackfillCandidates(ctx context.Context, limit int, includeChecked bool, legacyUserID string) ([]FaceBackfillCandidate, error) {
	var limitValue any
	if limit > 0 {
		limitValue = limit
	}
	rows, err := d.pool.Query(ctx,
		`SELECT media_id, object_key, content_type, user_id, legacy_user_id
		 FROM (
		     SELECT DISTINCT ON (pp.media_id)
		            pp.media_id, m.object_key, m.content_type, pp.user_id,
		            COALESCE(u.legacy_user_id, '') AS legacy_user_id, pp.id
		       FROM profile_photos pp
		       JOIN media m ON m.id = pp.media_id
		       JOIN users u ON u.id = pp.user_id
		      WHERE ($1::boolean OR NOT pp.face_checked)
		        AND ($2 = '' OR u.legacy_user_id = $2)
		      ORDER BY pp.media_id, pp.id
		 ) AS distinct_media
		 ORDER BY id
		 LIMIT $3::bigint`, includeChecked, legacyUserID, limitValue)
	if err != nil {
		return nil, fmt.Errorf("db: list face backfill candidates: %w", err)
	}
	defer rows.Close()

	out := make([]FaceBackfillCandidate, 0, 64)
	for rows.Next() {
		var item FaceBackfillCandidate
		if err := rows.Scan(&item.MediaID, &item.ObjectKey, &item.ContentType,
			&item.UserID, &item.LegacyUserID); err != nil {
			return nil, fmt.Errorf("db: scan face backfill candidate: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate face backfill candidates: %w", err)
	}
	return out, nil
}
