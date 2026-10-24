package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestIntegrationExtraRealLikeQuota proves a paid real_like order actually
// raises CreateLike's quota, and that refunding it removes the extra slot.
func TestIntegrationExtraRealLikeQuota(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	me := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	first := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	second := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})

	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: me,
		TargetID:    first,
		LikeType:    LikeTypeReal,
		Message:     "第一次实名喜欢，测试额度。",
		Now:         SocialNow(),
	}); err != nil {
		t.Fatalf("first real like: %v", err)
	}
	// The free monthly slot is now used, so a second real like is blocked.
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: me,
		TargetID:    second,
		LikeType:    LikeTypeReal,
		Message:     "第二次实名喜欢，应该被拦截。",
		Now:         SocialNow(),
	}); err != ErrLikeQuota {
		t.Fatalf("second real like err = %v, want ErrLikeQuota", err)
	}
	quota, err := d.LikeQuotaFor(ctx, me, time.Now())
	if err != nil {
		t.Fatalf("quota before redeem: %v", err)
	}
	if quota.RealTotal != MonthlyRealHearts || quota.RealLeft != 0 || quota.RealPermanent != 0 {
		t.Fatalf("quota before redeem = %+v", quota)
	}

	pointsGrant(t, d, ctx, me, 100, "extra-real-like")
	order, err := d.RedeemPoints(ctx, me, RedeemRequest{Item: RedeemItemRealLike})
	if err != nil {
		t.Fatalf("redeem real_like: %v", err)
	}
	if order.Status != RedeemStatusActive {
		t.Fatalf("redeemed order status = %q", order.Status)
	}
	quota, err = d.LikeQuotaFor(ctx, me, time.Now())
	if err != nil {
		t.Fatalf("quota after redeem: %v", err)
	}
	if quota.RealPermanent != 1 || quota.RealTotal != MonthlyRealHearts+1 || quota.RealLeft != 1 {
		t.Fatalf("quota after redeem = %+v", quota)
	}
	result, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: me,
		TargetID:    second,
		LikeType:    LikeTypeReal,
		Message:     "兑换之后，这次实名喜欢应该成功。",
		Now:         SocialNow(),
	})
	if err != nil {
		t.Fatalf("paid real like: %v", err)
	}
	if result.Quota.RealPermanent != 1 || result.Quota.RealLeft != 0 {
		t.Fatalf("paid like quota = %+v", result.Quota)
	}

	// Refunding the order removes the permanent slot immediately.
	if _, err := d.RefundRedemption(ctx, order.ID, "测试退款", PointsKindRefund, nil, RedeemStatusRefunded); err != nil {
		t.Fatalf("refund real_like: %v", err)
	}
	quota, err = d.LikeQuotaFor(ctx, me, time.Now())
	if err != nil {
		t.Fatalf("quota after refund: %v", err)
	}
	if quota.RealPermanent != 0 || quota.RealTotal != MonthlyRealHearts || quota.RealLeft != 0 {
		t.Fatalf("quota after refund = %+v", quota)
	}
	if extra, err := d.ExtraRealLikeQuota(ctx, me); err != nil || extra != 0 {
		t.Fatalf("extra quota after refund = %d err=%v", extra, err)
	}
}

// TestIntegrationSignupLikesNotifications ports v1's two-way signup notice:
// named to people who liked the signup user, anonymous to people the signup
// user liked, with mutual pairs receiving only the named notice.
func TestIntegrationSignupLikesNotifications(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	signer := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale, Nickname: "报名男生"})
	liker := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	liked := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	mutual := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})

	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: liker, TargetID: signer, LikeType: LikeTypeAnon,
		Message: "这是一条匿名的喜欢留言。", Now: SocialNow(),
	}); err != nil {
		t.Fatalf("liker like: %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: signer, TargetID: liked, LikeType: LikeTypeAnon,
		Message: "报名男生匿名喜欢她的留言。", Now: SocialNow(),
	}); err != nil {
		t.Fatalf("liked like: %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: signer, TargetID: mutual, LikeType: LikeTypeAnon,
		Message: "报名男生喜欢相互的人的留言。", Now: SocialNow(),
	}); err != nil {
		t.Fatalf("mutual like: %v", err)
	}
	if _, err := d.CreateLike(ctx, CreateLikeParams{
		InitiatorID: mutual, TargetID: signer, LikeType: LikeTypeAnon,
		Message: "相互的人回应报名男生的留言。", Now: SocialNow(),
	}); err != nil {
		t.Fatalf("mutual response: %v", err)
	}

	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{MaxSignup: 10})
	signup, err := d.SignupActivity(ctx, signer, activity.ActivityNo)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	sent, err := d.NotifySignupLikes(ctx, signup.ID, signer, activity.ID)
	if err != nil {
		t.Fatalf("notify signup likes: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent = %d, want 3", sent)
	}
	assertNotificationCount(t, ctx, d, liker, "signup", 1)
	assertNotificationCount(t, ctx, d, liked, "signup", 1)
	// The mutual pair is in likers, so it gets exactly one named notice.
	assertNotificationCount(t, ctx, d, mutual, "signup", 1)

	var title, body, payload string
	if err := d.pool.QueryRow(ctx,
		`SELECT title, body, payload::text FROM notifications
		  WHERE user_id = $1 AND kind = 'signup'`, liked).Scan(&title, &body, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(title, "喜欢你的人报名了") {
		t.Fatalf("anonymous title = %q", title)
	}
	for _, leak := range []string{"报名男生", signerPhone(d, ctx, signer)} {
		if leak != "" && (strings.Contains(title, leak) || strings.Contains(body, leak) || strings.Contains(payload, leak)) {
			t.Fatalf("anonymous notification leaked %q: title=%q body=%q payload=%s", leak, title, body, payload)
		}
	}
	if strings.Contains(payload, `"initiator_id"`) {
		t.Fatalf("anonymous notification payload carries an initiator: %s", payload)
	}

	// Re-running the job must not duplicate any notice.
	sent, err = d.NotifySignupLikes(ctx, signup.ID, signer, activity.ID)
	if err != nil {
		t.Fatalf("notify signup likes again: %v", err)
	}
	if sent != 0 {
		t.Fatalf("second run sent = %d, want 0", sent)
	}
	assertNotificationCount(t, ctx, d, liker, "signup", 1)
	assertNotificationCount(t, ctx, d, mutual, "signup", 1)
}

func assertNotificationCount(t *testing.T, ctx context.Context, d *DB, userID int64, kind string, want int) {
	t.Helper()
	var got int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = $2`,
		userID, kind).Scan(&got); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if got != want {
		t.Fatalf("notifications user=%d kind=%s = %d, want %d", userID, kind, got, want)
	}
}

func signerPhone(d *DB, ctx context.Context, userID int64) string {
	var phone string
	_ = d.pool.QueryRow(ctx, `SELECT phone FROM users WHERE id = $1`, userID).Scan(&phone)
	return phone
}
