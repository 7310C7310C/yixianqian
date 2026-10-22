package db

import (
	"testing"
	"time"
)

func TestMonthBuckets(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"2026-01", 3, "2026-04"},
		{"2026-01", -1, "2025-12"},
		{"2026-12", 1, "2027-01"},
		{"2026-03", -14, "2025-01"},
		{"", 1, ""},
		{"bad", 1, ""},
	}
	for _, tc := range cases {
		if got := MonthAdd(tc.in, tc.n); got != tc.want {
			t.Errorf("MonthAdd(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
	if got, ok := MonthsBetween("2026-01", "2026-04"); !ok || got != 3 {
		t.Fatalf("MonthsBetween = %d,%v", got, ok)
	}
	if got, ok := MonthsBetween("2026-04", "2026-01"); !ok || got != -3 {
		t.Fatalf("MonthsBetween reverse = %d,%v", got, ok)
	}
	if _, ok := MonthsBetween("", "2026-01"); ok {
		t.Fatal("empty month should not parse")
	}
}

func TestIsLikeActive(t *testing.T) {
	const ym = "2026-10"
	cases := []struct {
		name   string
		status string
		typ    string
		month  string
		want   bool
	}{
		{"mutual never expires", LikeStatusMutual, LikeTypeAnon, "2026-01", true},
		{"real never expires", LikeStatusSingle, LikeTypeReal, "2026-01", true},
		{"anon current", LikeStatusSingle, LikeTypeAnon, "2026-10", true},
		{"anon two months", LikeStatusSingle, LikeTypeAnon, "2026-08", true},
		{"anon three months expires", LikeStatusSingle, LikeTypeAnon, "2026-07", false},
		{"anon missing month kept", LikeStatusSingle, LikeTypeAnon, "", true},
		{"anon malformed month kept", LikeStatusSingle, LikeTypeAnon, "oops", true},
		{"rejected inactive", LikeStatusRejected, LikeTypeReal, "2026-10", false},
		{"unknown status inactive", "已失效", LikeTypeReal, "2026-10", false},
		{"empty status inactive", "", LikeTypeReal, "2026-10", false},
	}
	for _, tc := range cases {
		if got := IsLikeActive(tc.status, tc.typ, tc.month, ym); got != tc.want {
			t.Errorf("%s: IsLikeActive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestComputeLikeQuotaParity(t *testing.T) {
	const ym = "2026-10"
	record := func(typ, month, status string) LikeRecord {
		return LikeRecord{LikeType: typ, Month: month, Status: status}
	}
	t.Run("fresh month", func(t *testing.T) {
		q := ComputeLikeQuota(nil, 0, ym)
		if q.AnonLeft != 10 || q.AnonUsed != 0 || q.RealLeft != 1 || q.RealTotal != 1 {
			t.Fatalf("unexpected fresh quota: %+v", q)
		}
	})
	t.Run("anon used this month only", func(t *testing.T) {
		records := []LikeRecord{
			record(LikeTypeAnon, "2026-10", LikeStatusSingle),
			record(LikeTypeAnon, "2026-10", LikeStatusSingle),
			record(LikeTypeAnon, "2026-09", LikeStatusSingle),   // last month: no carry
			record(LikeTypeAnon, "2026-07", LikeStatusSingle),   // expires this month: refund
			record(LikeTypeAnon, "2026-06", LikeStatusSingle),   // expired long ago: no repeated refund
			record(LikeTypeAnon, "2026-10", LikeStatusRejected), // invalid
		}
		q := ComputeLikeQuota(records, 0, ym)
		if q.AnonUsed != 2 {
			t.Fatalf("anon used = %d, want 2", q.AnonUsed)
		}
		if q.AnonLeft != 9 {
			t.Fatalf("anon left = %d, want 9 (10 + 1 refund - 2 used)", q.AnonLeft)
		}
	})
	t.Run("quota never negative", func(t *testing.T) {
		var records []LikeRecord
		for i := 0; i < 12; i++ {
			records = append(records, record(LikeTypeAnon, "2026-10", LikeStatusSingle))
		}
		q := ComputeLikeQuota(records, 0, ym)
		if q.AnonLeft != 0 {
			t.Fatalf("anon left = %d, want 0", q.AnonLeft)
		}
	})
	t.Run("real quota with permanent slots", func(t *testing.T) {
		records := []LikeRecord{
			record(LikeTypeReal, "2026-10", LikeStatusSingle),
			record(LikeTypeReal, "2026-09", LikeStatusSingle),
		}
		q := ComputeLikeQuota(records, 2, ym)
		if q.RealTotal != 3 || q.RealLeft != 2 {
			t.Fatalf("real quota = %+v, want total 3 left 2", q)
		}
	})
	t.Run("mutual anon still counts month slots", func(t *testing.T) {
		records := []LikeRecord{
			record(LikeTypeAnon, "2026-10", LikeStatusMutual),
			record(LikeTypeAnon, "2026-07", LikeStatusMutual),
		}
		q := ComputeLikeQuota(records, 0, ym)
		if q.AnonUsed != 1 || q.AnonLeft != 10 {
			t.Fatalf("mutual anon quota = %+v, want used 1 left 10", q)
		}
	})
}

func TestSocialDayWeekBucketsUseShanghai(t *testing.T) {
	// 2026-01-01 00:30 Shanghai = 2025-12-31 16:30 UTC.
	utc := time.Date(2025, 12, 31, 16, 30, 0, 0, time.UTC)
	if got := DayKey(utc); got != "2026-01-01" {
		t.Fatalf("DayKey = %q, want 2026-01-01", got)
	}
	want := "2026-W01"
	if got := WeekKey(utc); got != want {
		t.Fatalf("WeekKey = %q, want %q", got, want)
	}
	if got := MonthKey(utc); got != "2026-01" {
		t.Fatalf("MonthKey = %q, want 2026-01", got)
	}
}
