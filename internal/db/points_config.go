package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PointsConfig ports lib/points_config.py. The defaults in code are the source
// of truth; app_config only stores administrator overrides. Keeping every
// number in one place is intentional: the product spec asked for the points
// values (rewards, prices, ratios, deadlines) to live in one config.
type PointsConfig struct {
	InviteRewardFemale int
	InviteRewardMale   int
	InviteConfirmDays  int

	AssistMin         int
	AssistMax         int
	AssistActivityCap int

	RedeemRealLike         int
	RedeemPrioritySignup   int
	RedeemWish             int
	RedeemMatchmaker       int
	PriorityRatio          float64
	PriorityCancelHours    int
	MatchmakerDeadline     int
	MatchmakerConditionMax int

	DailyPraiseLimit int
}

// DefaultPointsConfig mirrors SPECS defaults from lib/points_config.py.
func DefaultPointsConfig() PointsConfig {
	return PointsConfig{
		InviteRewardFemale: 20,
		InviteRewardMale:   15,
		InviteConfirmDays:  7,

		AssistMin:         10,
		AssistMax:         50,
		AssistActivityCap: 50,

		RedeemRealLike:         20,
		RedeemPrioritySignup:   30,
		RedeemWish:             30,
		RedeemMatchmaker:       50,
		PriorityRatio:          0.3,
		PriorityCancelHours:    48,
		MatchmakerDeadline:     14,
		MatchmakerConditionMax: 200,

		DailyPraiseLimit: 10,
	}
}

type pointsConfigKind int

const (
	pointsConfigInt pointsConfigKind = iota
	pointsConfigFloat
)

type pointsConfigSpec struct {
	key      string
	label    string
	kind     pointsConfigKind
	intDef   int
	floatDef float64
	min      float64
	max      float64
}

var pointsConfigSpecs = []pointsConfigSpec{
	{key: "invite_reward_female", label: "邀请女生注册奖励", kind: pointsConfigInt, intDef: 20, min: 0, max: 10000},
	{key: "invite_reward_male", label: "邀请男生注册奖励", kind: pointsConfigInt, intDef: 15, min: 0, max: 10000},
	{key: "invite_confirm_days", label: "邀请奖励确认期（天）", kind: pointsConfigInt, intDef: 7, min: 0, max: 365},

	{key: "assist_min", label: "协助组织单次下限", kind: pointsConfigInt, intDef: 10, min: 0, max: 10000},
	{key: "assist_max", label: "协助组织单次上限", kind: pointsConfigInt, intDef: 50, min: 0, max: 10000},
	{key: "assist_activity_cap", label: "同一活动协助累计上限", kind: pointsConfigInt, intDef: 50, min: 0, max: 10000},

	{key: "redeem_real_like", label: "兑换：额外实名喜欢", kind: pointsConfigInt, intDef: 20, min: 0, max: 10000},
	{key: "redeem_priority_signup", label: "兑换：优先报名名额", kind: pointsConfigInt, intDef: 30, min: 0, max: 10000},
	{key: "redeem_wish", label: "兑换：心愿名额", kind: pointsConfigInt, intDef: 30, min: 0, max: 10000},
	{key: "redeem_matchmaker", label: "兑换：红娘人工推荐", kind: pointsConfigInt, intDef: 50, min: 0, max: 10000},

	{key: "priority_ratio", label: "优先名额占活动总名额比例", kind: pointsConfigFloat, floatDef: 0.3, min: 0, max: 1},
	{key: "priority_cancel_hours", label: "优先名额可退穗的取消提前量（小时）", kind: pointsConfigInt, intDef: 48, min: 0, max: 8760},

	{key: "matchmaker_deadline_days", label: "红娘推荐处理期限（天）", kind: pointsConfigInt, intDef: 14, min: 1, max: 365},
	{key: "matchmaker_condition_max_len", label: "红娘推荐条件字数上限", kind: pointsConfigInt, intDef: 200, min: 1, max: 2000},

	{key: "daily_praise_limit", label: "每天最多点赞次数", kind: pointsConfigInt, intDef: 10, min: 0, max: 1000},
}

var pointsConfigSpecByKey = func() map[string]pointsConfigSpec {
	out := make(map[string]pointsConfigSpec, len(pointsConfigSpecs))
	for _, spec := range pointsConfigSpecs {
		out[spec.key] = spec
	}
	return out
}()

// PointsConfigItem is the rules/admin view of one setting.
type PointsConfigItem struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Value      any    `json:"value"`
	Default    any    `json:"default"`
	Overridden bool   `json:"overridden"`
	Min        any    `json:"min"`
	Max        any    `json:"max"`
}

type pointsRowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func parsePointsConfigInt(raw string, def int) int {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return def
	}
	return int(value)
}

func parsePointsConfigFloat(raw string, def float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return def
	}
	return value
}

// applyPointsConfigOverride returns the default when stored text cannot be
// parsed, matching the old config's fail-safe behaviour.
func applyPointsConfigOverride(key, raw string, cfg PointsConfig) (PointsConfig, bool) {
	spec, ok := pointsConfigSpecByKey[key]
	if !ok {
		return cfg, false
	}
	if spec.kind == pointsConfigFloat {
		value := parsePointsConfigFloat(raw, spec.floatDef)
		// Manual DB edits are untrusted: an out-of-range value must not turn a
		// price negative or a ratio above 100%. Fall back to the default.
		if value < spec.min || value > spec.max {
			return cfg, false
		}
		if key == "priority_ratio" {
			cfg.PriorityRatio = value
		}
		return cfg, true
	}
	intValue := parsePointsConfigInt(raw, spec.intDef)
	if intValue < int(spec.min) || intValue > int(spec.max) {
		return cfg, false
	}
	value := intValue
	switch key {
	case "invite_reward_female":
		cfg.InviteRewardFemale = value
	case "invite_reward_male":
		cfg.InviteRewardMale = value
	case "invite_confirm_days":
		cfg.InviteConfirmDays = value
	case "assist_min":
		cfg.AssistMin = value
	case "assist_max":
		cfg.AssistMax = value
	case "assist_activity_cap":
		cfg.AssistActivityCap = value
	case "redeem_real_like":
		cfg.RedeemRealLike = value
	case "redeem_priority_signup":
		cfg.RedeemPrioritySignup = value
	case "redeem_wish":
		cfg.RedeemWish = value
	case "redeem_matchmaker":
		cfg.RedeemMatchmaker = value
	case "priority_cancel_hours":
		cfg.PriorityCancelHours = value
	case "matchmaker_deadline_days":
		cfg.MatchmakerDeadline = value
	case "matchmaker_condition_max_len":
		cfg.MatchmakerConditionMax = value
	case "daily_praise_limit":
		cfg.DailyPraiseLimit = value
	}
	return cfg, true
}

// loadPointsConfig reads app_config overrides. Unreadable values fall back to
// defaults instead of failing the request, as the old config did.
func loadPointsConfig(ctx context.Context, q pointsRowQuerier) (PointsConfig, map[string]bool, error) {
	cfg := DefaultPointsConfig()
	overridden := make(map[string]bool)
	rows, err := q.Query(ctx, `SELECT key, value FROM app_config`)
	if err != nil {
		return PointsConfig{}, nil, fmt.Errorf("db: read points config: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return PointsConfig{}, nil, fmt.Errorf("db: scan points config: %w", err)
		}
		if _, ok := pointsConfigSpecByKey[key]; !ok {
			continue
		}
		var applied bool
		cfg, applied = applyPointsConfigOverride(key, raw, cfg)
		if applied {
			overridden[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		return PointsConfig{}, nil, fmt.Errorf("db: iterate points config: %w", err)
	}
	return cfg, overridden, nil
}

// GetPointsConfig returns the effective values for the current request.
func (d *DB) GetPointsConfig(ctx context.Context) (PointsConfig, error) {
	cfg, _, err := loadPointsConfig(ctx, d.pool)
	return cfg, err
}

func (c PointsConfig) valueFor(key string) any {
	switch key {
	case "invite_reward_female":
		return c.InviteRewardFemale
	case "invite_reward_male":
		return c.InviteRewardMale
	case "invite_confirm_days":
		return c.InviteConfirmDays
	case "assist_min":
		return c.AssistMin
	case "assist_max":
		return c.AssistMax
	case "assist_activity_cap":
		return c.AssistActivityCap
	case "redeem_real_like":
		return c.RedeemRealLike
	case "redeem_priority_signup":
		return c.RedeemPrioritySignup
	case "redeem_wish":
		return c.RedeemWish
	case "redeem_matchmaker":
		return c.RedeemMatchmaker
	case "priority_ratio":
		return c.PriorityRatio
	case "priority_cancel_hours":
		return c.PriorityCancelHours
	case "matchmaker_deadline_days":
		return c.MatchmakerDeadline
	case "matchmaker_condition_max_len":
		return c.MatchmakerConditionMax
	case "daily_praise_limit":
		return c.DailyPraiseLimit
	default:
		return nil
	}
}

// Values returns the config as the JSON shape used by /api/points/rules.
func (c PointsConfig) Values() map[string]any {
	out := make(map[string]any, len(pointsConfigSpecs))
	for _, spec := range pointsConfigSpecs {
		out[spec.key] = c.valueFor(spec.key)
	}
	return out
}

// DescribePointsConfig lists value/default/override state for rule pages.
func (d *DB) DescribePointsConfig(ctx context.Context) ([]PointsConfigItem, error) {
	cfg, overridden, err := loadPointsConfig(ctx, d.pool)
	if err != nil {
		return nil, err
	}
	out := make([]PointsConfigItem, 0, len(pointsConfigSpecs))
	for _, spec := range pointsConfigSpecs {
		item := PointsConfigItem{
			Key:        spec.key,
			Label:      spec.label,
			Value:      cfg.valueFor(spec.key),
			Overridden: overridden[spec.key],
		}
		if spec.kind == pointsConfigFloat {
			item.Default = spec.floatDef
			item.Min = spec.min
			item.Max = spec.max
		} else {
			item.Default = spec.intDef
			item.Min = int(spec.min)
			item.Max = int(spec.max)
		}
		out = append(out, item)
	}
	return out, nil
}
