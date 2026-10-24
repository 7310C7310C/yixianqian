package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

type socialFixtureOpts struct {
	Gender         string
	AccountStatus  string
	AccountKind    string
	Nickname       string
	Birthday       time.Time
	Height         int
	WithPhoto      bool
	ContactConsent bool
	Wechat         string
}

func openSocialTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	d, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(d.Close)
	// Keep this connection alive for the whole test.
	if err := d.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := d.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d, ctx
}

func createSocialFixtureUser(t *testing.T, d *DB, ctx context.Context, opts socialFixtureOpts) int64 {
	t.Helper()
	if opts.Gender == "" {
		opts.Gender = GenderFemale
	}
	if opts.AccountStatus == "" {
		opts.AccountStatus = "active"
	}
	if opts.AccountKind == "" {
		opts.AccountKind = "user"
	}
	if opts.Nickname == "" {
		opts.Nickname = "social-" + randomDigits(t, 10)
	}
	phone := "1" + randomDigits(t, 10)
	legacy := "U-SOC-" + randomDigits(t, 10)
	openID := "ou_soc_" + randomDigits(t, 16)
	var birthday any
	if !opts.Birthday.IsZero() {
		birthday = opts.Birthday
	}
	var height any
	if opts.Height > 0 {
		height = opts.Height
	}
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO users (legacy_user_id, legacy_open_id, phone, name, nickname, gender,
		                    birthday, height_cm, account_kind, account_status, status,
		                    contact_consent, wechat)
		 VALUES ($1, $2, $3, $4, $4, $5, $6, $7, $8, $9, 'active', $10, $11)
		 RETURNING id`,
		legacy, openID, phone, opts.Nickname, opts.Gender, birthday, height,
		opts.AccountKind, opts.AccountStatus, opts.ContactConsent, opts.Wechat).Scan(&id)
	if err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = d.pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", id)
	})
	if opts.WithPhoto {
		mediaID, err := platform.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.CreateMedia(ctx, Media{
			ID:           mediaID,
			UserID:       id,
			OriginalName: "photo.jpg",
			ObjectKey:    fmt.Sprintf("media/%d/%s.jpg", id, mediaID),
			ThumbKey:     fmt.Sprintf("media/%d/%s_thumb.jpg", id, mediaID),
			ContentType:  "image/jpeg",
			ByteSize:     1000,
			ThumbSize:    100,
			Width:        600,
			Height:       800,
		})
		if err != nil {
			t.Fatalf("create fixture media: %v", err)
		}
		if _, err := d.pool.Exec(ctx,
			`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
			 VALUES ($1, $2, 0, true)`, id, mediaID); err != nil {
			t.Fatalf("create profile photo: %v", err)
		}
	}
	return id
}

func socialDirectLike(t *testing.T, d *DB, ctx context.Context, initiator, target int64,
	likeType, month, status string) {
	t.Helper()
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, message, month)
		 VALUES ($1, $2, $3, $4, '这是一段十个字以上的测试附言', $5)`,
		initiator, target, status, likeType, month); err != nil {
		t.Fatalf("insert direct like: %v", err)
	}
}

func socialMustContain(t *testing.T, cards []SocialProfile, id int64) {
	t.Helper()
	for _, c := range cards {
		if c.ID == id {
			return
		}
	}
	t.Fatalf("card %d missing from %d candidates", id, len(cards))
}

func socialMustNotContain(t *testing.T, cards []SocialProfile, id int64) {
	t.Helper()
	for _, c := range cards {
		if c.ID == id {
			t.Fatalf("card %d should not be in candidates", id)
		}
	}
}

func TestIntegrationSocialCardsLikesMutual(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	now := SocialNow()
	prefix := "it-" + randomDigits(t, 8)

	male := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male", WithPhoto: true,
	})
	female := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-female", WithPhoto: true,
		Birthday: time.Date(1990, 1, 15, 0, 0, 0, 0, time.UTC), Height: 165,
		ContactConsent: true, Wechat: "wx-female",
	})
	sameGender := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male2", WithPhoto: true,
	})
	pending := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, AccountStatus: "pending", Nickname: prefix + "-pending", WithPhoto: true,
	})
	noPhoto := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-nophoto",
	})
	observer := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, AccountStatus: "observer", AccountKind: "observer",
		Nickname: prefix + "-observer", WithPhoto: true,
	})
	young := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-young", WithPhoto: true,
		Birthday: time.Date(2005, 1, 15, 0, 0, 0, 0, time.UTC),
	})
	// Age-filter isolation: only the fixture named exactly prefix+"-female" matches.
	cards, err := d.ListCardCandidates(ctx, male, GenderFemale, CardFilter{
		Nickname: prefix + "-female", AgeMin: 30, AgeMax: 40,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	socialMustContain(t, cards, female)
	for _, id := range []int64{sameGender, pending, noPhoto, observer, young, male} {
		socialMustNotContain(t, cards, id)
	}

	all, err := d.ListCardCandidates(ctx, male, GenderFemale, CardFilter{Nickname: prefix}, now)
	if err != nil {
		t.Fatal(err)
	}
	socialMustContain(t, all, female)
	socialMustContain(t, all, young)
	socialMustNotContain(t, all, noPhoto)
	socialMustNotContain(t, all, pending)
	socialMustNotContain(t, all, observer)

	ageFiltered, err := d.ListCardCandidates(ctx, male, GenderFemale, CardFilter{
		Nickname: prefix, AgeMin: 30, AgeMax: 40,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	socialMustContain(t, ageFiltered, female)
	socialMustNotContain(t, ageFiltered, young)

	// Self / same gender / inactive target are rejected without writing.
	valid := "这是一段十个字以上的喜欢附言"
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: male, LikeType: LikeTypeAnon, Message: valid, Now: now,
	}); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("self like err = %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: sameGender, LikeType: LikeTypeAnon, Message: valid, Now: now,
	}); !errors.Is(err, ErrSameGender) {
		t.Fatalf("same gender like err = %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: pending, LikeType: LikeTypeAnon, Message: valid, Now: now,
	}); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("pending target like err = %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: LikeTypeAnon, Message: "太短", Now: now,
	}); !errors.Is(err, ErrLikeMessageShort) {
		t.Fatalf("short message err = %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: "随便", Message: valid, Now: now,
	}); !errors.Is(err, ErrInvalidLikeType) {
		t.Fatalf("invalid type err = %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: LikeTypeAnon,
		Message: strings.Repeat("长", 501), Now: now,
	}); !errors.Is(err, ErrLikeMessageLong) {
		t.Fatalf("long message err = %v", err)
	}

	first, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: LikeTypeAnon, Message: valid, Now: now,
	})
	if err != nil {
		t.Fatalf("create first like: %v", err)
	}
	if first.Mutual {
		t.Fatal("first like should not be mutual")
	}
	if first.Quota.AnonUsed != 1 || first.Quota.AnonLeft != MonthlyAnonHearts-1 {
		t.Fatalf("first quota = %+v", first.Quota)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: LikeTypeAnon, Message: valid, Now: now,
	}); !errors.Is(err, ErrAlreadyLiked) {
		t.Fatalf("duplicate like err = %v", err)
	}

	var notifCount int
	var notifTitle string
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(title), '') FROM notifications WHERE user_id = $1`,
		female).Scan(&notifCount, &notifTitle); err != nil {
		t.Fatal(err)
	}
	if notifCount != 1 {
		t.Fatalf("female notifications = %d, want 1", notifCount)
	}
	if notifTitle != "有人匿名喜欢了你" {
		t.Fatalf("anonymous notification title = %q (must not identify sender)", notifTitle)
	}

	// Reciprocal like flips both rows and notifies both sides.
	second, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: female, TargetID: male, LikeType: LikeTypeReal, Message: valid, Now: now,
	})
	if err != nil {
		t.Fatalf("create reciprocal like: %v", err)
	}
	if !second.Mutual {
		t.Fatal("reciprocal like should be mutual")
	}
	var mutualRows int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM likes
		 WHERE status = $1 AND ((initiator_id = $2 AND target_id = $3) OR (initiator_id = $3 AND target_id = $2))`,
		LikeStatusMutual, male, female).Scan(&mutualRows); err != nil {
		t.Fatal(err)
	}
	if mutualRows != 2 {
		t.Fatalf("mutual rows = %d, want 2", mutualRows)
	}
	var mutualNotif int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'mutual'`, male).Scan(&mutualNotif); err != nil {
		t.Fatal(err)
	}
	if mutualNotif != 1 {
		t.Fatalf("male mutual notifications = %d, want 1", mutualNotif)
	}

	overview, err := d.SocialOverview(ctx, male, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.LikesMe) != 1 || !overview.LikesMe[0].Mutual {
		t.Fatalf("male likes-me = %+v", overview.LikesMe)
	}
	if len(overview.MyLikes) != 1 || !overview.MyLikes[0].Mutual {
		t.Fatalf("male my-likes = %+v", overview.MyLikes)
	}
	if !overview.LikesMe[0].Other.ContactConsent || overview.LikesMe[0].Other.Wechat != "wx-female" {
		t.Fatalf("mutual contact fixture missing: %+v", overview.LikesMe[0].Other)
	}

	// Anonymous quota: nine more distinct targets exhaust the ten monthly slots.
	var extraTargets []int64
	for i := 0; i < 9; i++ {
		id := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
			Gender: GenderFemale, Nickname: fmt.Sprintf("%s-extra-%d", prefix, i), WithPhoto: true,
		})
		extraTargets = append(extraTargets, id)
		if _, err := d.CreateLike(ctx, CreateLikeParams{
			InitiatorID: male, TargetID: id, LikeType: LikeTypeAnon, Message: valid, Now: now,
		}); err != nil {
			t.Fatalf("create quota like %d: %v", i, err)
		}
	}
	quota, err := d.LikeQuotaFor(ctx, male, now)
	if err != nil {
		t.Fatal(err)
	}
	if quota.AnonLeft != 0 || quota.AnonUsed != 10 {
		t.Fatalf("exhausted quota = %+v", quota)
	}
	overLimit := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-over", WithPhoto: true,
	})
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: overLimit, LikeType: LikeTypeAnon, Message: valid, Now: now,
	}); !errors.Is(err, ErrLikeQuota) {
		t.Fatalf("over-limit like err = %v", err)
	}

	// A three-month-old anonymous single like refunds one slot in this month.
	socialDirectLike(t, d, ctx, male, overLimit, LikeTypeAnon, MonthAdd(MonthKey(now), -AnonExpireMonths), LikeStatusSingle)
	quota, err = d.LikeQuotaFor(ctx, male, now)
	if err != nil {
		t.Fatal(err)
	}
	if quota.AnonLeft != 1 {
		t.Fatalf("refunded anon left = %d, want 1", quota.AnonLeft)
	}
	_ = extraTargets

	// Real-name quota is one per month.
	realTarget := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-real", WithPhoto: true,
	})
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: realTarget, LikeType: LikeTypeReal, Message: valid, Now: now,
	}); err != nil {
		t.Fatalf("first real like: %v", err)
	}
	realSecond := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-real2", WithPhoto: true,
	})
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: realSecond, LikeType: LikeTypeReal, Message: valid, Now: now,
	}); !errors.Is(err, ErrLikeQuota) {
		t.Fatalf("second real like err = %v", err)
	}
}

func TestIntegrationSocialPraise(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	now := SocialNow()
	prefix := "itp-" + randomDigits(t, 8)

	male := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male", WithPhoto: true,
	})
	male2 := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male2", WithPhoto: true,
	})
	var targets []int64
	for i := 0; i < 12; i++ {
		targets = append(targets, createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
			Gender: GenderFemale, Nickname: fmt.Sprintf("%s-f%d", prefix, i), WithPhoto: true,
		}))
	}
	pending := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, AccountStatus: "pending", Nickname: prefix + "-pending", WithPhoto: true,
	})

	if _, err := d.CreatePraise(ctx, male, male, now); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("self praise err = %v", err)
	}
	if _, err := d.CreatePraise(ctx, male, male2, now); !errors.Is(err, ErrSameGender) {
		t.Fatalf("same gender praise err = %v", err)
	}
	if _, err := d.CreatePraise(ctx, male, pending, now); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("pending praise err = %v", err)
	}

	result, err := d.CreatePraise(ctx, male, targets[0], now)
	if err != nil {
		t.Fatalf("create praise: %v", err)
	}
	if result.Already || result.State.Used != 1 || result.State.Left != DailyPraiseLimit-1 {
		t.Fatalf("praise state = %+v", result)
	}
	if len(result.State.Praised) != 1 || result.State.Praised[0] != targets[0] {
		t.Fatalf("praised = %v", result.State.Praised)
	}

	dup, err := d.CreatePraise(ctx, male, targets[0], now)
	if err != nil {
		t.Fatalf("duplicate praise: %v", err)
	}
	if !dup.Already || dup.State.Used != 1 {
		t.Fatalf("duplicate praise state = %+v", dup)
	}

	received, err := d.PraiseStateFor(ctx, targets[0], now)
	if err != nil {
		t.Fatal(err)
	}
	if received.ReceivedWeek != 1 {
		t.Fatalf("received week = %d, want 1", received.ReceivedWeek)
	}

	cancelled, err := d.CancelPraise(ctx, male, targets[0], now)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Used != 0 || cancelled.Left != DailyPraiseLimit || len(cancelled.Praised) != 0 {
		t.Fatalf("cancel state = %+v", cancelled)
	}
	received, err = d.PraiseStateFor(ctx, targets[0], now)
	if err != nil {
		t.Fatal(err)
	}
	if received.ReceivedWeek != 0 {
		t.Fatalf("received after cancel = %d, want 0", received.ReceivedWeek)
	}
	if _, err := d.CancelPraise(ctx, male, targets[0], now); err != nil {
		t.Fatalf("cancel idempotent: %v", err)
	}

	// Fill the daily quota with distinct targets.
	for i := 0; i < DailyPraiseLimit; i++ {
		if _, err := d.CreatePraise(ctx, male, targets[i], now); err != nil {
			t.Fatalf("praise %d: %v", i, err)
		}
	}
	state, err := d.PraiseStateFor(ctx, male, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.Left != 0 || state.Used != DailyPraiseLimit {
		t.Fatalf("full praise state = %+v", state)
	}
	if _, err := d.CreatePraise(ctx, male, targets[DailyPraiseLimit], now); !errors.Is(err, ErrPraiseQuota) {
		t.Fatalf("over-limit praise err = %v", err)
	}
	if _, err := d.CancelPraise(ctx, male, targets[0], now); err != nil {
		t.Fatal(err)
	}
	last, err := d.CreatePraise(ctx, male, targets[DailyPraiseLimit], now)
	if err != nil {
		t.Fatalf("praise after cancel: %v", err)
	}
	if last.State.Left != 0 {
		t.Fatalf("left after refill = %d, want 0", last.State.Left)
	}
}

func TestIntegrationSocialOverviewMessageVisibility(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	now := SocialNow()
	prefix := "ito-" + randomDigits(t, 8)

	me := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-me", WithPhoto: true,
	})
	anon := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-anon", WithPhoto: true,
	})
	real := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-real", WithPhoto: true,
	})
	socialDirectLike(t, d, ctx, anon, me, LikeTypeAnon, MonthKey(now), LikeStatusSingle)
	socialDirectLike(t, d, ctx, real, me, LikeTypeReal, MonthKey(now), LikeStatusSingle)

	overview, err := d.SocialOverview(ctx, me, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.LikesMe) != 2 {
		t.Fatalf("likes-me = %d, want 2", len(overview.LikesMe))
	}
	for _, pair := range overview.LikesMe {
		if pair.Like.InitiatorID == anon && pair.Like.LikeType != LikeTypeAnon {
			t.Fatal("anonymous like type changed")
		}
	}

	// Expired anonymous outgoing likes stay in MyLikes but are marked inactive.
	socialDirectLike(t, d, ctx, me, anon, LikeTypeAnon, MonthAdd(MonthKey(now), -AnonExpireMonths), LikeStatusSingle)
	overview, err = d.SocialOverview(ctx, me, now)
	if err != nil {
		t.Fatal(err)
	}
	var sawExpired bool
	for _, pair := range overview.MyLikes {
		if pair.Like.TargetID == anon && !pair.Active {
			sawExpired = true
		}
	}
	if !sawExpired {
		t.Fatal("expired outgoing anonymous like missing or wrongly active")
	}
}

func TestIntegrationCardCandidateIDsMatchProfiles(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	now := SocialNow()
	prefix := "itids-" + randomDigits(t, 8)

	male := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male", WithPhoto: true,
	})
	female := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-female", WithPhoto: true, Height: 165,
	})
	pending := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, AccountStatus: "pending", Nickname: prefix + "-pending", WithPhoto: true,
	})
	noPhoto := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-nophoto",
	})
	filters := []CardFilter{
		{Nickname: prefix},
		{Nickname: prefix, HeightMin: 160},
		{City: prefix, Nickname: prefix},
	}
	for _, filter := range filters {
		profiles, err := d.ListCardCandidates(ctx, male, GenderFemale, filter, now)
		if err != nil {
			t.Fatalf("profiles: %v", err)
		}
		ids, err := d.ListCardCandidateIDs(ctx, male, GenderFemale, filter, now)
		if err != nil {
			t.Fatalf("ids: %v", err)
		}
		if len(ids) != len(profiles) {
			t.Fatalf("filter %+v: ids=%d profiles=%d", filter, len(ids), len(profiles))
		}
		for i := range ids {
			if ids[i] != profiles[i].ID {
				t.Fatalf("filter %+v: position %d id=%d profile=%d", filter, i, ids[i], profiles[i].ID)
			}
		}
	}
	ids, err := d.ListCardCandidateIDs(ctx, male, GenderFemale, CardFilter{Nickname: prefix}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != female {
		t.Fatalf("ids = %v, want exactly female %d (pending=%d nophoto=%d)", ids, female, pending, noPhoto)
	}
}

func TestIntegrationBackfillMutualWechatNotifications(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	prefix := "itwx-" + randomDigits(t, 8)
	male := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderMale, Nickname: prefix + "-male", WithPhoto: true,
		Wechat: "wx-backfill-m", ContactConsent: true,
	})
	female := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{
		Gender: GenderFemale, Nickname: prefix + "-female", WithPhoto: true,
		Wechat: "wx-backfill-f", ContactConsent: true,
	})
	if _, err := d.pool.Exec(ctx,
		`DELETE FROM notifications WHERE user_id = ANY($1)`, []int64{male, female}); err != nil {
		t.Fatal(err)
	}

	const message = "很高兴认识你，希望有机会聊聊。"
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: male, TargetID: female, LikeType: LikeTypeReal,
		Message: message, Now: SocialNow(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: female, TargetID: male, LikeType: LikeTypeAnon,
		Message: message, Now: SocialNow(),
	}); err != nil {
		t.Fatal(err)
	}

	created, err := d.BackfillMutualWechatNotifications(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if created < 2 {
		t.Fatalf("created = %d, want >= 2", created)
	}
	var n int
	var body string
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(body), '') FROM notifications
		  WHERE user_id = $1 AND kind = 'wechat_exchange'`, male).Scan(&n, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(body, "wx-backfill-f") {
		t.Fatalf("male notifications count=%d body=%q", n, body)
	}
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(body), '') FROM notifications
		  WHERE user_id = $1 AND kind = 'wechat_exchange'`, female).Scan(&n, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(body, "wx-backfill-m") {
		t.Fatalf("female notifications count=%d body=%q", n, body)
	}

	// Idempotent: a second sweep creates nothing for this pair.
	if _, err := d.BackfillMutualWechatNotifications(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications
		  WHERE user_id = ANY($1) AND kind = 'wechat_exchange'`,
		[]int64{male, female}).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("wechat notifications after second sweep = %d, want 2", n)
	}
}
