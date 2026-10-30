package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func activityViewTimePtr(t time.Time) *time.Time { return &t }

func TestBuildActivityViewDerivesEndedStatus(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, activityTimeZone)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	cases := []struct {
		name       string
		status     string
		endAt      *time.Time
		wantStatus string
		wantClass  string
		wantSignup bool
	}{
		{"open before end", db.ActivityStatusOpen, &future, db.ActivityStatusOpen, "is-open", true},
		{"open at end", db.ActivityStatusOpen, &now, db.ActivityStatusFinished, "is-ended", false},
		{"open after end", db.ActivityStatusOpen, &past, db.ActivityStatusFinished, "is-ended", false},
		{"full after end", db.ActivityStatusFull, &past, db.ActivityStatusFinished, "is-ended", false},
		{"cancelled stays", db.ActivityStatusCancelled, &past, db.ActivityStatusCancelled, "is-ended", false},
		{"finished stays", db.ActivityStatusFinished, &past, db.ActivityStatusFinished, "is-ended", false},
		{"not started stays", db.ActivityStatusNotStarted, &past, db.ActivityStatusNotStarted, "is-soon", false},
		{"no end stays", db.ActivityStatusOpen, nil, db.ActivityStatusOpen, "is-open", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := buildActivityView(db.Activity{
				ID: 1, ActivityNo: "A-TEST", Status: tc.status, MaxSignup: 10, EndAt: tc.endAt,
			}, now)
			if view.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", view.Status, tc.wantStatus)
			}
			if view.StatusClass != tc.wantClass {
				t.Fatalf("status class = %q, want %q", view.StatusClass, tc.wantClass)
			}
			if view.CanSignup != tc.wantSignup {
				t.Fatalf("can_signup = %v, want %v", view.CanSignup, tc.wantSignup)
			}
		})
	}
}

func TestApplySignupToViewClearsCapabilitiesAfterEnd(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, activityTimeZone)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	ended := buildActivityView(db.Activity{
		ID: 1, ActivityNo: "A-ENDED", Status: db.ActivityStatusOpen, MaxSignup: 10,
		CurrentSignup: 1, SigninEnabled: true, SigninCode: "0123", StartAt: &past, EndAt: &past,
	}, now)
	applySignupToView(&ended, db.SignupStatusSigned, "")
	if ended.Status != db.ActivityStatusFinished {
		t.Fatalf("ended status = %q", ended.Status)
	}
	if ended.CanSignup || ended.CanCancel || ended.CanSelfSignin {
		t.Fatalf("ended capabilities = signup:%v cancel:%v self_signin:%v",
			ended.CanSignup, ended.CanCancel, ended.CanSelfSignin)
	}

	live := buildActivityView(db.Activity{
		ID: 2, ActivityNo: "A-LIVE", Status: db.ActivityStatusOpen, MaxSignup: 10,
		CurrentSignup: 1, SigninEnabled: true, SigninCode: "0123", StartAt: &past, EndAt: &future,
	}, now)
	applySignupToView(&live, db.SignupStatusSigned, "")
	if !live.CanCancel || live.CanSignup {
		t.Fatalf("live signup capabilities = signup:%v cancel:%v", live.CanSignup, live.CanCancel)
	}
	if !live.CanSelfSignin {
		t.Fatal("live signed-up activity should allow self sign-in")
	}
}

func TestCanSelfSigninBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, activityTimeZone)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name       string
		status     string
		startAt    *time.Time
		endAt      *time.Time
		signin     bool
		applySig   bool
		signup     string
		attendance db.Attendance
		want       bool
	}{
		{"inside window", db.ActivityStatusOpen, &past, &future, true, true, db.SignupStatusSigned, "", true},
		{"start nil acts unbounded", db.ActivityStatusOpen, nil, &future, true, true, db.SignupStatusSigned, "", true},
		{"end nil acts unbounded", db.ActivityStatusOpen, &past, nil, true, true, db.SignupStatusSigned, "", true},
		{"full inside window", db.ActivityStatusFull, &past, &future, true, true, db.SignupStatusSigned, "", true},
		{"not signed up", db.ActivityStatusOpen, &past, &future, true, false, "", "", false},
		{"cancelled signup", db.ActivityStatusOpen, &past, &future, true, true, db.SignupStatusCancelled, "", false},
		{"signin disabled", db.ActivityStatusOpen, &past, &future, false, true, db.SignupStatusSigned, "", false},
		{"already on time", db.ActivityStatusOpen, &past, &future, true, true, db.SignupStatusSigned, db.AttendanceOnTime, false},
		{"already late", db.ActivityStatusOpen, &past, &future, true, true, db.SignupStatusSigned, db.AttendanceLate, false},
		{"marked absent", db.ActivityStatusOpen, &past, &future, true, true, db.SignupStatusSigned, db.AttendanceAbsent, false},
		{"not started", db.ActivityStatusOpen, &future, &future, true, true, db.SignupStatusSigned, "", false},
		{"start equals now", db.ActivityStatusOpen, &now, &future, true, true, db.SignupStatusSigned, "", true},
		{"end equals now", db.ActivityStatusOpen, &past, &now, true, true, db.SignupStatusSigned, "", false},
		{"ended by schedule", db.ActivityStatusOpen, &past, &past, true, true, db.SignupStatusSigned, "", false},
		{"finished status", db.ActivityStatusFinished, nil, nil, true, true, db.SignupStatusSigned, "", false},
		{"cancelled status", db.ActivityStatusCancelled, &past, &future, true, true, db.SignupStatusSigned, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := buildActivityView(db.Activity{
				ID: 1, ActivityNo: "A-TEST", Status: tc.status, MaxSignup: 10,
				SigninEnabled: tc.signin, SigninCode: "0123", StartAt: tc.startAt, EndAt: tc.endAt,
			}, now)
			if tc.applySig {
				applySignupToView(&view, tc.signup, tc.attendance)
			}
			if view.CanSelfSignin != tc.want {
				t.Fatalf("can_self_signin = %v, want %v (status=%q)", view.CanSelfSignin, tc.want, view.Status)
			}
		})
	}

	// An activity can carry signin_enabled with no code (legacy data or an
	// admin who toggled the flag before setting the code), or with a legacy
	// non-4-digit code. Self sign-in can never succeed in those states, so the
	// capability must stay false.
	for _, code := range []string{"", "12", "12345", "abcd"} {
		noCode := buildActivityView(db.Activity{
			ID: 9, ActivityNo: "A-NO-CODE", Status: db.ActivityStatusOpen, MaxSignup: 10,
			SigninEnabled: true, SigninCode: code, StartAt: &past, EndAt: &future,
		}, now)
		applySignupToView(&noCode, db.SignupStatusSigned, "")
		if noCode.CanSelfSignin {
			t.Fatalf("activity with sign-in code %q must not advertise self sign-in", code)
		}
	}
}

// A view built without a request clock (for example a grouping result literal)
// must never advertise self sign-in.
func TestCanSelfSigninRequiresObservedClock(t *testing.T) {
	start := time.Date(2026, 10, 27, 18, 0, 0, 0, activityTimeZone)
	end := start.Add(time.Hour)
	view := ActivityView{
		SigninEnabled: true, MySignup: true, Status: db.ActivityStatusOpen,
		StartsAt: &start, EndsAt: &end,
	}
	if view.selfSigninAllowed() {
		t.Fatal("zero observedAt must not allow self sign-in")
	}
}

// ActivityView JSON keeps the capability field and never leaks the raw times.
func TestActivityViewJSONFields(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, activityTimeZone)
	end := now.Add(time.Hour)
	view := buildActivityView(db.Activity{
		ID: 1, ActivityNo: "A-JSON", Status: db.ActivityStatusOpen,
		SigninEnabled: true, EndAt: &end,
	}, now)
	view.CanSelfSignin = true
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if !strings.Contains(body, `"can_self_signin":true`) {
		t.Fatalf("json missing can_self_signin: %s", body)
	}
	if strings.Contains(body, `"StartsAt"`) || strings.Contains(body, `"EndsAt"`) ||
		strings.Contains(body, `"observedAt"`) {
		t.Fatalf("json leaked raw schedule fields: %s", body)
	}
}

// Ended activities close the user-side grouping / one-to-one entries while the
// stored DB flags stay untouched for the admin console.
func TestEndedActivityClosesUserMatchingEntries(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, activityTimeZone)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	cases := []struct {
		name       string
		status     string
		endAt      *time.Time
		wantGroup  bool
		wantOneto1 bool
	}{
		{"open before end", db.ActivityStatusOpen, &future, true, true},
		{"open at end", db.ActivityStatusOpen, &now, false, false},
		{"open after end", db.ActivityStatusOpen, &past, false, false},
		{"full after end", db.ActivityStatusFull, &past, false, false},
		{"finished status", db.ActivityStatusFinished, nil, false, false},
		{"cancelled status", db.ActivityStatusCancelled, &future, false, false},
		{"open without end", db.ActivityStatusOpen, nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := buildActivityView(db.Activity{
				ID: 1, ActivityNo: "A-TEST", Status: tc.status,
				GroupEnabled: true, OnetooneEnabled: true, EndAt: tc.endAt,
			}, now)
			if view.GroupEnabled != tc.wantGroup || view.OnetooneEnabled != tc.wantOneto1 {
				t.Fatalf("matching flags = group:%v onetoone:%v, want %v/%v",
					view.GroupEnabled, view.OnetooneEnabled, tc.wantGroup, tc.wantOneto1)
			}
		})
	}
}
