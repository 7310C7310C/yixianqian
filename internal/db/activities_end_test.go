package db

import (
	"testing"
	"time"
)

func TestActivityEndedAt(t *testing.T) {
	now := time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	equal := now

	cases := []struct {
		name   string
		status string
		endAt  *time.Time
		want   bool
	}{
		{"open without end", ActivityStatusOpen, nil, false},
		{"open before end", ActivityStatusOpen, &future, false},
		{"open at end", ActivityStatusOpen, &equal, true},
		{"open after end", ActivityStatusOpen, &past, true},
		{"full before end", ActivityStatusFull, &future, false},
		{"full after end", ActivityStatusFull, &past, true},
		{"finished status without end", ActivityStatusFinished, nil, true},
		{"finished status before end", ActivityStatusFinished, &future, true},
		{"cancelled status without end", ActivityStatusCancelled, nil, true},
		{"cancelled status before end", ActivityStatusCancelled, &future, true},
		{"not started with past end", ActivityStatusNotStarted, &past, true},
		{"not started without end", ActivityStatusNotStarted, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			activity := Activity{Status: tc.status, EndAt: tc.endAt}
			if got := activity.EndedAt(now); got != tc.want {
				t.Fatalf("EndedAt = %v, want %v", got, tc.want)
			}
		})
	}
}
