package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/jackc/pgx/v5"
)

// MaxProfilePhotos mirrors the legacy Bitable attachment cell limit.
const MaxProfilePhotos = 9

// Select option sets are stored in PostgreSQL as plain strings, so the same
// lists drive both editing and validation.
var (
	ProfileEducationOptions = []string{"大专以下", "大专", "本科", "硕士", "博士"}
	ProfileIncomeOptions    = []string{"10W以下", "10W - 20W", "20W - 30W", "30W - 50W", "50W以上"}
	ProfileYesNoOptions     = []string{"有", "无"}
	ProfileLiveWithOptions  = []string{"独立生活", "与父母住在一起", "根据具体情况而定"}
	ProfileTraitOptions     = []string{
		"喜欢安静", "比较主动", "非常自律", "做事严谨", "性格沉稳", "积极乐观",
		"有始有终", "喜爱冒险", "猎奇", "渴望成功", "理性待事", "正直", "话痨",
		"善于交际", "容易相处", "有点小幽默", "传统的", "思想前卫", "总是充满热情", "讲究效率",
		"其他",
	}
	ProfileHobbyOptions = []string{
		"有氧运动", "听音乐", "看电影", "看书", "手绘", "唱歌", "弹吉他", "弹钢琴",
		"剧本杀", "狼人杀", "王者荣耀", "吃鸡", "密室逃脱", "爱车一族", "旅游爱好者",
		"看小说", "拍照片", "养宠物", "做饭", "寻觅美食", "逛博物馆", "游乐场", "蹦迪",
		"其他",
	}
	ProfileSportOptions = []string{
		"篮球", "足球", "乒乓球", "羽毛球", "网球", "台球", "游泳", "跑步", "爬山",
		"射击", "跳绳", "漂流", "瑜伽", "慢走", "跳舞", "骑行", "攀岩", "蹦床",
		"滑雪", "冲浪", "滑冰", "跆拳道", "蹦极", "极限运动", "潜水",
		"其他",
	}
	ProfileMBTIOptions = []string{"E", "I", "S", "N", "T", "F", "J", "P"}
)

// ValidationError marks a client-supplied profile value as invalid. Messages
// are safe to show to the user and never contain other users' data.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// ProfilePhoto is one ordered photo attached to a profile.
type ProfilePhoto struct {
	ID        int64
	MediaID   string
	SortOrder int
	IsCover   bool
	Width     int
	Height    int
}

// Profile is the full owner-visible profile. Sensitive values are limited to
// what the owner may see; the ID card ciphertext is never loaded here.
type Profile struct {
	ID              int64
	LegacyUserID    string
	Phone           string
	Name            string
	Nickname        string
	Gender          string
	Birthday        *time.Time
	HeightCM        *int
	Education       string
	BaptismalName   string
	Church          string
	ChurchLocation  string
	ChurchGroup     string
	NativePlace     string
	City            string
	Industry        string
	Position        string
	Income          string
	House           string
	Driving         string
	HouseNoteHave   string
	HouseNoteNone   string
	DrivingNoteHave string
	DrivingNoteNone string
	Marriage        string
	Family          string
	LiveWithParents string
	Personality     string
	SelfTraits      []string
	SelfHobbies     []string
	SelfSports      []string
	MBTI            []string
	PartnerCriteria string
	PartnerTraits   []string
	PartnerHobbies  []string
	PartnerSports   []string
	Wechat          string
	IDCardLast4     string
	IDCardValid     bool
	AccountKind     string
	AccountStatus   string
	ContactConsent  bool
	ReferralSource  string
	IsForChild      string
	WechatPayment   string
	RegisteredAt    *time.Time
	LastActiveAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Photos          []ProfilePhoto
}

// ProfileUpdate carries optional whitelisted changes. A nil pointer leaves the
// column untouched; a pointer to an empty string clears a text field.
type ProfileUpdate struct {
	Nickname        *string
	HeightCM        *int
	Education       *string
	City            *string
	Church          *string
	ChurchGroup     *string
	ChurchLocation  *string
	NativePlace     *string
	Industry        *string
	Position        *string
	Income          *string
	House           *string
	Driving         *string
	Personality     *string
	Family          *string
	LiveWithParents *string
	PartnerCriteria *string
	SelfTraits      *[]string
	SelfHobbies     *[]string
	SelfSports      *[]string
	MBTI            *[]string
	PartnerTraits   *[]string
	PartnerHobbies  *[]string
	PartnerSports   *[]string
	Wechat          *string
	Phone           *string

	// ID card backfill is only accepted while the account is still invalid.
	// The handler validates and encrypts the number; the database stores the
	// ciphertext, last four digits and keyed uniqueness hash.
	IDCardCipher []byte
	IDCardLast4  string
	IDCardHash   string
}

// Empty reports whether the update contains no whitelisted changes.
func (u ProfileUpdate) Empty() bool {
	return u.Nickname == nil && u.HeightCM == nil && u.Education == nil &&
		u.City == nil && u.Church == nil && u.ChurchGroup == nil &&
		u.ChurchLocation == nil && u.NativePlace == nil && u.Industry == nil &&
		u.Position == nil && u.Income == nil && u.House == nil && u.Driving == nil &&
		u.Personality == nil && u.Family == nil && u.LiveWithParents == nil &&
		u.PartnerCriteria == nil && u.SelfTraits == nil && u.SelfHobbies == nil &&
		u.SelfSports == nil && u.MBTI == nil && u.PartnerTraits == nil &&
		u.PartnerHobbies == nil && u.PartnerSports == nil && u.Wechat == nil &&
		u.Phone == nil && u.IDCardCipher == nil
}

// Account status switches are restricted to the product's original rule.
var (
	ErrStatusNotSwitchable = errors.New("db: account status cannot be switched")
	ErrActiveSignupBlocked = errors.New("db: active signup blocks matched status")
	ErrObserverNoSwitch    = errors.New("db: observer account cannot switch status")
	ErrPhotoLimit          = errors.New("db: profile photo limit reached")
)

const profileColumns = `id, COALESCE(legacy_user_id, ''), phone, name, nickname, gender,
	birthday, height_cm, education, baptismal_name, church, church_location, church_group,
	native_place, city, industry, position, income, house, driving, house_note_have,
	house_note_none, driving_note_have, driving_note_none, marriage, family,
	live_with_parents, personality, self_traits, self_hobbies, self_sports, mbti,
	partner_criteria, partner_traits, partner_hobbies, partner_sports, wechat,
	id_card_last4, id_card_valid, account_kind, account_status, contact_consent,
	referral_source, is_for_child, wechat_payment, registered_at, last_active_at,
	created_at, updated_at`

func decodeStringList(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func scanProfile(row rowScanner) (Profile, error) {
	var (
		p                                       Profile
		traits, hobbies, sports, mbti           []byte
		partnerTraits, partnerHobbies, partnerS []byte
	)
	err := row.Scan(
		&p.ID, &p.LegacyUserID, &p.Phone, &p.Name, &p.Nickname, &p.Gender,
		&p.Birthday, &p.HeightCM, &p.Education, &p.BaptismalName, &p.Church,
		&p.ChurchLocation, &p.ChurchGroup, &p.NativePlace, &p.City, &p.Industry,
		&p.Position, &p.Income, &p.House, &p.Driving, &p.HouseNoteHave,
		&p.HouseNoteNone, &p.DrivingNoteHave, &p.DrivingNoteNone, &p.Marriage,
		&p.Family, &p.LiveWithParents, &p.Personality, &traits, &hobbies, &sports,
		&mbti, &p.PartnerCriteria, &partnerTraits, &partnerHobbies, &partnerS,
		&p.Wechat, &p.IDCardLast4, &p.IDCardValid, &p.AccountKind, &p.AccountStatus,
		&p.ContactConsent, &p.ReferralSource, &p.IsForChild, &p.WechatPayment,
		&p.RegisteredAt, &p.LastActiveAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	for _, item := range []struct {
		raw []byte
		dst *[]string
	}{
		{traits, &p.SelfTraits},
		{hobbies, &p.SelfHobbies},
		{sports, &p.SelfSports},
		{mbti, &p.MBTI},
		{partnerTraits, &p.PartnerTraits},
		{partnerHobbies, &p.PartnerHobbies},
		{partnerS, &p.PartnerSports},
	} {
		list, err := decodeStringList(item.raw)
		if err != nil {
			return Profile{}, fmt.Errorf("db: decode profile list: %w", err)
		}
		*item.dst = list
	}
	return p, nil
}

func (d *DB) loadProfilePhotos(ctx context.Context, p *Profile) error {
	photos, err := d.ProfilePhotos(ctx, p.ID)
	if err != nil {
		return err
	}
	p.Photos = photos
	return nil
}

// ProfilePhotos returns photos in display order, cover first.
func (d *DB) ProfilePhotos(ctx context.Context, userID int64) ([]ProfilePhoto, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT pp.id, pp.media_id, pp.sort_order, pp.is_cover, m.width, m.height
		 FROM profile_photos pp
		 JOIN media m ON m.id = pp.media_id
		 WHERE pp.user_id = $1
		 ORDER BY pp.is_cover DESC, pp.sort_order, pp.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list profile photos: %w", err)
	}
	defer rows.Close()

	out := make([]ProfilePhoto, 0, 9)
	for rows.Next() {
		var ph ProfilePhoto
		if err := rows.Scan(&ph.ID, &ph.MediaID, &ph.SortOrder, &ph.IsCover, &ph.Width, &ph.Height); err != nil {
			return nil, fmt.Errorf("db: scan profile photo: %w", err)
		}
		out = append(out, ph)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate profile photos: %w", err)
	}
	return out, nil
}

// GetProfile loads the owner-visible profile and its ordered photos.
func (d *DB) GetProfile(ctx context.Context, userID int64) (Profile, error) {
	p, err := scanProfile(d.pool.QueryRow(ctx,
		"SELECT "+profileColumns+" FROM users WHERE id = $1", userID))
	if err != nil {
		return Profile{}, err
	}
	if err := d.loadProfilePhotos(ctx, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// GetProfileByLegacyID loads a profile by the legacy user id (e.g. U-0006).
func (d *DB) GetProfileByLegacyID(ctx context.Context, legacyID string) (Profile, error) {
	p, err := scanProfile(d.pool.QueryRow(ctx,
		"SELECT "+profileColumns+" FROM users WHERE legacy_user_id = $1", legacyID))
	if err != nil {
		return Profile{}, err
	}
	if err := d.loadProfilePhotos(ctx, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// UpdateProfile validates and applies whitelisted fields. Values that fail
// validation are rejected before any SQL runs.
func (d *DB) UpdateProfile(ctx context.Context, userID int64, upd ProfileUpdate) (Profile, error) {
	if err := validateProfileUpdate(&upd); err != nil {
		return Profile{}, err
	}

	var (
		sets []string
		args []any
	)
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if upd.Nickname != nil {
		add("nickname", *upd.Nickname)
	}
	if upd.HeightCM != nil {
		add("height_cm", *upd.HeightCM)
	}
	if upd.Education != nil {
		add("education", *upd.Education)
	}
	if upd.City != nil {
		add("city", *upd.City)
	}
	if upd.Church != nil {
		add("church", *upd.Church)
	}
	if upd.ChurchGroup != nil {
		add("church_group", *upd.ChurchGroup)
	}
	if upd.ChurchLocation != nil {
		add("church_location", *upd.ChurchLocation)
	}
	if upd.NativePlace != nil {
		add("native_place", *upd.NativePlace)
	}
	if upd.Industry != nil {
		add("industry", *upd.Industry)
	}
	if upd.Position != nil {
		add("position", *upd.Position)
	}
	if upd.Income != nil {
		add("income", *upd.Income)
	}
	if upd.House != nil {
		add("house", *upd.House)
	}
	if upd.Driving != nil {
		add("driving", *upd.Driving)
	}
	if upd.Personality != nil {
		add("personality", *upd.Personality)
	}
	if upd.Family != nil {
		add("family", *upd.Family)
	}
	if upd.LiveWithParents != nil {
		add("live_with_parents", *upd.LiveWithParents)
	}
	if upd.PartnerCriteria != nil {
		add("partner_criteria", *upd.PartnerCriteria)
	}
	if upd.SelfTraits != nil {
		add("self_traits", *upd.SelfTraits)
	}
	if upd.SelfHobbies != nil {
		add("self_hobbies", *upd.SelfHobbies)
	}
	if upd.SelfSports != nil {
		add("self_sports", *upd.SelfSports)
	}
	if upd.MBTI != nil {
		add("mbti", *upd.MBTI)
	}
	if upd.PartnerTraits != nil {
		add("partner_traits", *upd.PartnerTraits)
	}
	if upd.PartnerHobbies != nil {
		add("partner_hobbies", *upd.PartnerHobbies)
	}
	if upd.PartnerSports != nil {
		add("partner_sports", *upd.PartnerSports)
	}
	if upd.Wechat != nil {
		add("wechat", *upd.Wechat)
	}
	if upd.Phone != nil {
		add("phone", *upd.Phone)
	}
	if upd.IDCardCipher != nil {
		add("id_card_ciphertext", upd.IDCardCipher)
		add("id_card_last4", upd.IDCardLast4)
		add("id_card_hash", upd.IDCardHash)
		add("id_card_valid", true)
	}

	if len(sets) == 0 {
		return d.GetProfile(ctx, userID)
	}
	args = append(args, userID)
	query := "UPDATE users SET " + strings.Join(sets, ", ") +
		fmt.Sprintf(", updated_at = now() WHERE id = $%d RETURNING ", len(args)) +
		profileColumns
	p, err := scanProfile(d.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Profile{}, err
		}
		return Profile{}, mapWriteError(err)
	}
	if err := d.loadProfilePhotos(ctx, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// IDCardHashInUse reports whether another account already stores this keyed
// identity hash. The unique index remains the final guard against races.
func (d *DB) IDCardHashInUse(ctx context.Context, hash string, excludeUserID int64) (bool, error) {
	if strings.TrimSpace(hash) == "" {
		return false, nil
	}
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id_card_hash = $1 AND id <> $2)`,
		hash, excludeUserID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: check id card hash: %w", err)
	}
	return exists, nil
}

func validateProfileUpdate(upd *ProfileUpdate) error {
	if err := cleanProfileText("nickname", "昵称", upd.Nickname, 30, false); err != nil {
		return err
	}
	if upd.HeightCM != nil && (*upd.HeightCM < 100 || *upd.HeightCM > 250) {
		return &ValidationError{Field: "height_cm", Message: "身高需在 100-250 公分之间。"}
	}
	if err := validateProfileSelect("education", "学历", upd.Education, ProfileEducationOptions); err != nil {
		return err
	}
	if err := cleanProfileText("city", "城市", upd.City, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("church", "教会", upd.Church, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("church_group", "团体", upd.ChurchGroup, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("church_location", "教会所在市区", upd.ChurchLocation, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("native_place", "家乡", upd.NativePlace, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("industry", "行业", upd.Industry, 200, true); err != nil {
		return err
	}
	if err := cleanProfileText("position", "职位", upd.Position, 200, true); err != nil {
		return err
	}
	if err := validateProfileSelect("income", "年收入", upd.Income, ProfileIncomeOptions); err != nil {
		return err
	}
	if err := validateProfileSelect("house", "房产状况", upd.House, ProfileYesNoOptions); err != nil {
		return err
	}
	if err := validateProfileSelect("driving", "汽车", upd.Driving, ProfileYesNoOptions); err != nil {
		return err
	}
	if err := validateProfileSelect("live_with_parents", "婚后与父母同住", upd.LiveWithParents, ProfileLiveWithOptions); err != nil {
		return err
	}
	if err := cleanProfileText("personality", "我是怎样的人", upd.Personality, 2000, true); err != nil {
		return err
	}
	if err := cleanProfileText("family", "家庭情况", upd.Family, 2000, true); err != nil {
		return err
	}
	if err := cleanProfileText("partner_criteria", "理想型", upd.PartnerCriteria, 2000, true); err != nil {
		return err
	}
	for _, item := range []struct {
		field   string
		label   string
		value   *[]string
		allowed []string
	}{
		{"self_traits", "性格", upd.SelfTraits, ProfileTraitOptions},
		{"self_hobbies", "爱好", upd.SelfHobbies, ProfileHobbyOptions},
		{"self_sports", "运动", upd.SelfSports, ProfileSportOptions},
		{"mbti", "MBTI", upd.MBTI, ProfileMBTIOptions},
		{"partner_traits", "理想型性格", upd.PartnerTraits, ProfileTraitOptions},
		{"partner_hobbies", "理想型爱好", upd.PartnerHobbies, ProfileHobbyOptions},
		{"partner_sports", "理想型运动", upd.PartnerSports, ProfileSportOptions},
	} {
		if err := validateProfileMulti(item.field, item.label, item.value, item.allowed); err != nil {
			return err
		}
	}
	if err := cleanProfileText("wechat", "微信号", upd.Wechat, 200, true); err != nil {
		return err
	}
	if upd.Phone != nil {
		raw := strings.TrimSpace(*upd.Phone)
		if raw == "" {
			return &ValidationError{Field: "phone", Message: "手机号码不可为空。"}
		}
		normalized := auth.NormalizePhone(raw)
		if normalized == "" {
			return &ValidationError{Field: "phone", Message: "手机号码格式不正确。"}
		}
		upd.Phone = &normalized
	}
	return nil
}

func cleanProfileText(field, label string, value *string, maxRunes int, allowEmpty bool) error {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if !allowEmpty && trimmed == "" {
		return &ValidationError{Field: field, Message: label + "不可为空。"}
	}
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return &ValidationError{Field: field, Message: fmt.Sprintf("%s最多 %d 个字。", label, maxRunes)}
	}
	*value = trimmed
	return nil
}

func validateProfileSelect(field, label string, value *string, allowed []string) error {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed != "" && !containsString(allowed, trimmed) {
		return &ValidationError{Field: field, Message: label + "选项不正确。"}
	}
	*value = trimmed
	return nil
}

func validateProfileMulti(field, label string, value *[]string, allowed []string) error {
	if value == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(*value))
	out := make([]string, 0, len(*value))
	for _, raw := range *value {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if utf8.RuneCountInString(item) > 50 {
			return &ValidationError{Field: field, Message: label + "单项过长。"}
		}
		if !containsString(allowed, item) {
			return &ValidationError{Field: field, Message: label + "选项不正确。"}
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) > 30 {
		return &ValidationError{Field: field, Message: label + "选项过多。"}
	}
	*value = out
	return nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func lockProfileUser(ctx context.Context, tx pgx.Tx, userID int64) error {
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("db: lock profile user: %w", err)
	}
	return nil
}

func profilePhotoIDs(ctx context.Context, tx pgx.Tx, userID int64) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT media_id FROM profile_photos
		 WHERE user_id = $1 ORDER BY sort_order, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list profile photo ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: scan profile photo id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate profile photo ids: %w", err)
	}
	return ids, nil
}

func renumberProfilePhotos(ctx context.Context, tx pgx.Tx, userID int64) error {
	ids, err := profilePhotoIDs(ctx, tx, userID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE profile_photos SET sort_order = sort_order + 1000 WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("db: shift profile photo order: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE profile_photos SET is_cover = false WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("db: clear profile photo covers: %w", err)
	}
	for i, mediaID := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE profile_photos SET sort_order = $2, is_cover = $3
			 WHERE user_id = $1 AND media_id = $4`,
			userID, i, i == 0, mediaID); err != nil {
			return fmt.Errorf("db: renumber profile photo: %w", err)
		}
	}
	return nil
}

// AddProfilePhoto attaches an uploaded media item to the profile. The media
// must belong to the same user; the first photo becomes the cover.
func (d *DB) AddProfilePhoto(ctx context.Context, userID int64, mediaID string) (ProfilePhoto, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: begin add profile photo: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockProfileUser(ctx, tx, userID); err != nil {
		return ProfilePhoto{}, err
	}
	var width, height int
	err = tx.QueryRow(ctx,
		`SELECT width, height FROM media WHERE id = $1 AND user_id = $2`,
		mediaID, userID).Scan(&width, &height)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfilePhoto{}, ErrNotFound
	}
	if err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: load media for profile photo: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM profile_photos WHERE user_id = $1`, userID).Scan(&count); err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: count profile photos: %w", err)
	}
	if count >= MaxProfilePhotos {
		return ProfilePhoto{}, ErrPhotoLimit
	}

	var nextOrder int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM profile_photos WHERE user_id = $1`,
		userID).Scan(&nextOrder); err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: next profile photo order: %w", err)
	}
	var hasCover bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM profile_photos WHERE user_id = $1 AND is_cover)`,
		userID).Scan(&hasCover); err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: check profile cover: %w", err)
	}

	photo := ProfilePhoto{MediaID: mediaID, Width: width, Height: height}
	err = tx.QueryRow(ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, sort_order, is_cover`,
		userID, mediaID, nextOrder, !hasCover).
		Scan(&photo.ID, &photo.SortOrder, &photo.IsCover)
	if err != nil {
		return ProfilePhoto{}, mapWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProfilePhoto{}, fmt.Errorf("db: commit add profile photo: %w", err)
	}
	return photo, nil
}

// SetProfilePhotoCover makes the given photo first and the only cover.
func (d *DB) SetProfilePhotoCover(ctx context.Context, userID int64, mediaID string) ([]ProfilePhoto, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: begin set profile cover: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockProfileUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	ids, err := profilePhotoIDs(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, id := range ids {
		if id == mediaID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, ErrNotFound
	}
	if idx > 0 {
		reordered := make([]string, 0, len(ids))
		reordered = append(reordered, mediaID)
		reordered = append(reordered, ids[:idx]...)
		reordered = append(reordered, ids[idx+1:]...)
		ids = reordered
	}
	if _, err := tx.Exec(ctx,
		`UPDATE profile_photos SET sort_order = sort_order + 1000 WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("db: shift profile photo order: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE profile_photos SET is_cover = false WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("db: clear profile photo covers: %w", err)
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE profile_photos SET sort_order = $2, is_cover = $3
			 WHERE user_id = $1 AND media_id = $4`,
			userID, i, i == 0, id); err != nil {
			return nil, fmt.Errorf("db: reorder profile photo: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("db: commit set profile cover: %w", err)
	}
	return d.ProfilePhotos(ctx, userID)
}

// DeleteProfilePhoto removes the caller's attachment to a media row. The media
// row is deleted only when no profile still references it and the caller owns
// it; the bool reports that the row (and therefore the R2 objects) is gone.
//
// Migrated data can share one media row between many users. Deleting the
// attachment must never remove a photo another profile still shows.
func (d *DB) DeleteProfilePhoto(ctx context.Context, userID int64, mediaID string) (Media, bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Media{}, false, fmt.Errorf("db: begin delete profile photo: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockProfileUser(ctx, tx, userID); err != nil {
		return Media{}, false, err
	}
	var m Media
	err = tx.QueryRow(ctx,
		`SELECT m.id, m.user_id, m.original_name, m.object_key, m.thumb_key,
		        m.content_type, m.byte_size, m.thumb_size, m.width, m.height, m.created_at
		 FROM profile_photos pp
		 JOIN media m ON m.id = pp.media_id
		 WHERE pp.user_id = $1 AND pp.media_id = $2
		 FOR UPDATE OF pp, m`, userID, mediaID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, false, ErrNotFound
	}
	if err != nil {
		return Media{}, false, fmt.Errorf("db: load profile photo: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM profile_photos WHERE user_id = $1 AND media_id = $2`, userID, mediaID); err != nil {
		return Media{}, false, fmt.Errorf("db: delete profile photo: %w", err)
	}
	var refs int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM profile_photos WHERE media_id = $1`, mediaID).Scan(&refs); err != nil {
		return Media{}, false, fmt.Errorf("db: count profile photo references: %w", err)
	}
	removed := false
	// Only the media owner may delete the shared row, and only once no profile
	// references it. A non-owner deleting the last attachment leaves the row
	// for its owner (or an offline cleanup job).
	if refs == 0 && m.UserID == userID {
		tag, err := tx.Exec(ctx,
			`DELETE FROM media WHERE id = $1 AND user_id = $2`, mediaID, userID)
		if err != nil {
			return Media{}, false, fmt.Errorf("db: delete profile media: %w", err)
		}
		removed = tag.RowsAffected() > 0
	}
	if err := renumberProfilePhotos(ctx, tx, userID); err != nil {
		return Media{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Media{}, false, fmt.Errorf("db: commit delete profile photo: %w", err)
	}
	return m, removed, nil
}

// RemoveMediaOwned deletes an unattached media row owned by the user. It is
// used to roll back an upload that could not be attached to the profile; the
// bool reports whether the row was actually deleted so the caller only removes
// object-store keys that can no longer be referenced.
func (d *DB) RemoveMediaOwned(ctx context.Context, userID int64, mediaID string) (Media, bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Media{}, false, fmt.Errorf("db: begin remove owned media: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var m Media
	err = tx.QueryRow(ctx,
		"SELECT "+mediaColumns+" FROM media WHERE id = $1 AND user_id = $2 FOR UPDATE",
		mediaID, userID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, false, ErrNotFound
	}
	if err != nil {
		return Media{}, false, fmt.Errorf("db: load owned media: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM media m
		 WHERE m.id = $1 AND m.user_id = $2
		   AND NOT EXISTS (SELECT 1 FROM profile_photos pp WHERE pp.media_id = m.id)`,
		mediaID, userID)
	if err != nil {
		return Media{}, false, fmt.Errorf("db: delete owned media: %w", err)
	}
	removed := tag.RowsAffected() > 0
	if err := tx.Commit(ctx); err != nil {
		return Media{}, false, fmt.Errorf("db: commit remove owned media: %w", err)
	}
	return m, removed, nil
}

// ProfilePhotoFile describes one profile photo for the streaming handler.
type ProfilePhotoFile struct {
	UserID        int64
	AccountKind   string
	AccountStatus string
	ObjectKey     string
	ThumbKey      string
	ContentType   string
	ByteSize      int64
	ThumbSize     int64
	Width         int
	Height        int
}

// GetProfilePhotoFile loads a photo that is actually attached to a profile.
func (d *DB) GetProfilePhotoFile(ctx context.Context, mediaID string) (ProfilePhotoFile, error) {
	var f ProfilePhotoFile
	err := d.pool.QueryRow(ctx,
		`SELECT pp.user_id, u.account_kind, u.account_status,
		        m.object_key, m.thumb_key, m.content_type, m.byte_size, m.thumb_size,
		        m.width, m.height
		 FROM profile_photos pp
		 JOIN media m ON m.id = pp.media_id
		 JOIN users u ON u.id = pp.user_id
		 WHERE pp.media_id = $1`, mediaID).
		Scan(&f.UserID, &f.AccountKind, &f.AccountStatus, &f.ObjectKey, &f.ThumbKey,
			&f.ContentType, &f.ByteSize, &f.ThumbSize, &f.Width, &f.Height)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfilePhotoFile{}, ErrNotFound
	}
	if err != nil {
		return ProfilePhotoFile{}, fmt.Errorf("db: load profile photo file: %w", err)
	}
	return f, nil
}

// GetAccountState returns the two account fields used by routing and media
// gates without loading the full profile.
func (d *DB) GetAccountState(ctx context.Context, userID int64) (kind, status string, err error) {
	err = d.pool.QueryRow(ctx,
		`SELECT account_kind, account_status FROM users WHERE id = $1`, userID).
		Scan(&kind, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("db: load account state: %w", err)
	}
	return kind, status, nil
}

// HasActiveSignups reports whether the user currently holds an active signup.
func (d *DB) HasActiveSignups(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM signups WHERE user_id = $1 AND status = '已报名'
		 )`, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: check active signups: %w", err)
	}
	return exists, nil
}

// SetAccountStatus switches between active and matched under the legacy rule
// that an active activity signup blocks going matched.
func (d *DB) SetAccountStatus(ctx context.Context, userID int64, target string) (string, error) {
	if target != "active" && target != "matched" {
		return "", &ValidationError{Field: "account_status", Message: "账号状态不正确。"}
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("db: begin account status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current, kind string
	err = tx.QueryRow(ctx,
		`SELECT account_status, account_kind FROM users WHERE id = $1 FOR UPDATE`, userID).
		Scan(&current, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load account status: %w", err)
	}
	if current == "observer" || kind == "observer" {
		return "", ErrObserverNoSwitch
	}
	if current != "active" && current != "matched" {
		return "", ErrStatusNotSwitchable
	}
	if current == target {
		return current, nil
	}
	if target == "matched" {
		var hasSignup bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM signups WHERE user_id = $1 AND status = '已报名')`,
			userID).Scan(&hasSignup); err != nil {
			return "", fmt.Errorf("db: check account status signups: %w", err)
		}
		if hasSignup {
			return "", ErrActiveSignupBlocked
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET account_status = $2, updated_at = now() WHERE id = $1`,
		userID, target); err != nil {
		return "", fmt.Errorf("db: update account status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("db: commit account status: %w", err)
	}
	return target, nil
}
