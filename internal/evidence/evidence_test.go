package evidence

import (
	"testing"
	"time"
)

func TestNewID(t *testing.T) {
	cases := map[int]string{1: "ev-001", 12: "ev-012", 123: "ev-123", 1234: "ev-1234"}
	for n, want := range cases {
		if got := NewID(n); got != want {
			t.Fatalf("NewID(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHash(t *testing.T) {
	// Known SHA-256 of "abc" (FIPS 180-4 test vector).
	want := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := Hash([]byte("abc")); got != want {
		t.Fatalf("Hash(abc) = %q, want %q", got, want)
	}
}

func TestParseAndFormatTime(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-10-30T14:00:00Z", "2026-10-30T14:00:00Z"},
		{"2026-10-30T22:00:00+08:00", "2026-10-30T14:00:00Z"},
		{"2026-10-30T14:00:00.123456789Z", "2026-10-30T14:00:00Z"},
		{" 2026-10-30T14:00:00Z ", "2026-10-30T14:00:00Z"},
	}
	for _, c := range cases {
		got, err := ParseTime(c.in)
		if err != nil {
			t.Fatalf("ParseTime(%q): %v", c.in, err)
		}
		if formatted := FormatTime(got); formatted != c.want {
			t.Fatalf("FormatTime(ParseTime(%q)) = %q, want %q", c.in, formatted, c.want)
		}
	}
	for _, bad := range []string{"", "  ", "yesterday", "2026-10-30", "2026-10-30 14:00:00"} {
		if _, err := ParseTime(bad); err == nil {
			t.Fatalf("ParseTime(%q) succeeded, want error", bad)
		}
	}
}

func TestMarkTimingStale(t *testing.T) {
	now := time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC)
	fresh := Item{ObservedAt: "2026-10-30T13:59:59Z", CollectedAt: "2026-10-30T14:00:00Z"}
	fresh.MarkTiming(now)
	if fresh.Stale {
		t.Fatalf("expected fresh evidence, got stale: %+v", fresh)
	}

	edge := Item{ObservedAt: "2026-10-30T13:45:00Z", CollectedAt: "2026-10-30T13:45:00Z"}
	edge.MarkTiming(now)
	if edge.Stale {
		t.Fatalf("exactly 15m old must not be stale")
	}

	old := Item{ObservedAt: "2026-10-30T13:44:59Z", CollectedAt: "2026-10-30T13:44:59Z"}
	old.MarkTiming(now)
	if !old.Stale {
		t.Fatalf("expected stale evidence older than 15m")
	}

	oldCollected := Item{ObservedAt: "2026-10-30T13:59:00Z", CollectedAt: "2026-10-30T13:00:00Z"}
	oldCollected.MarkTiming(now)
	if !oldCollected.Stale {
		t.Fatalf("expected stale when collected_at is old")
	}
}

func TestMarkTimingSkew(t *testing.T) {
	now := time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		observed  string
		collected string
		want      int
	}{
		// Clock skew: observed_at later than collected_at beyond MaxSkew.
		{"observed 30s later", "2026-10-30T13:59:30Z", "2026-10-30T13:59:00Z", 30},
		{"observed 10s later", "2026-10-30T13:59:10Z", "2026-10-30T13:59:00Z", 10},
		{"observed exactly 9s later", "2026-10-30T13:59:09Z", "2026-10-30T13:59:00Z", 0},
		// Normal collection delay: collected_at at or after observed_at is not skew.
		{"collected 30s later", "2026-10-30T13:59:00Z", "2026-10-30T13:59:30Z", 0},
		{"collected 5s later", "2026-10-30T13:59:00Z", "2026-10-30T13:59:05Z", 0},
		{"identical timestamps", "2026-10-30T13:59:00Z", "2026-10-30T13:59:00Z", 0},
		// A long collection delay is not clock skew either.
		{"collected 2h later", "2026-10-30T12:00:00Z", "2026-10-30T14:00:00Z", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := Item{ObservedAt: c.observed, CollectedAt: c.collected}
			it.MarkTiming(now)
			if it.SkewSeconds != c.want {
				t.Fatalf("skew for %s/%s = %d, want %d", c.observed, c.collected, it.SkewSeconds, c.want)
			}
		})
	}
}

func TestMarkTimingToleratesBadTimestamps(t *testing.T) {
	it := Item{ObservedAt: "not-a-time", CollectedAt: "also-bad"}
	it.MarkTiming(time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC))
	if it.Stale || it.SkewSeconds != 0 {
		t.Fatalf("bad timestamps must not mark stale/skew: %+v", it)
	}
}
