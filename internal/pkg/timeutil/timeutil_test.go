package timeutil

import (
	"testing"
	"time"
)

// TestRoundTrip 本地字符串 ⇄ UTC 秒双向无损。
func TestRoundTrip(t *testing.T) {
	cases := []string{
		"2026-10-03 12:30:15",
		"2026-01-01 00:00:00",
		"2026-12-31 23:59:59",
		"2026-02-28 12:00:00",
	}
	for _, s := range cases {
		unix, err := ParseLocal(s)
		if err != nil {
			t.Fatalf("ParseLocal(%q) 失败: %v", s, err)
		}
		if got := FormatLocal(unix); got != s {
			t.Fatalf("双向转换不一致: %q → %d → %q", s, unix, got)
		}
	}
}

// TestKnownValue 已知时刻校验：2026-10-03 12:30:15 (+08:00) == 2026-10-03 04:30:15 UTC。
func TestKnownValue(t *testing.T) {
	unix, err := ParseLocal("2026-10-03 12:30:15")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 3, 4, 30, 15, 0, time.UTC).Unix()
	if unix != want {
		t.Fatalf("ParseLocal = %d, want %d", unix, want)
	}
}

// TestParseTimeOrDate 纯日期补界。
func TestParseTimeOrDate(t *testing.T) {
	start, err := ParseTimeOrDate("2026-09-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatLocal(start); got != "2026-09-01 00:00:00" {
		t.Fatalf("start 补界错误: %q", got)
	}
	end, err := ParseTimeOrDate("2026-09-30", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatLocal(end); got != "2026-09-30 23:59:59" {
		t.Fatalf("end 补界错误: %q", got)
	}
}

// TestLocalDate 本地日期推导。
func TestLocalDate(t *testing.T) {
	unix, _ := ParseLocal("2026-10-03 00:30:00")
	if got := LocalDate(unix); got != "2026-10-03" {
		t.Fatalf("LocalDate = %q", got)
	}
}

// TestWeekStart 周一为一周起点：2026-10-01（周四）所属周起点为 2026-09-28（周一）。
func TestWeekStart(t *testing.T) {
	unix, _ := ParseLocal("2026-10-01 15:00:00")
	ws := WeekStart(unix)
	if got := FormatLocal(ws); got != "2026-09-28 00:00:00" {
		t.Fatalf("WeekStart = %q, want 2026-09-28 00:00:00", got)
	}
	// 周日归本周（周一起点）：2026-10-04（周日）→ 2026-09-28
	sun, _ := ParseLocal("2026-10-04 23:00:00")
	if got := FormatLocal(WeekStart(sun)); got != "2026-09-28 00:00:00" {
		t.Fatalf("周日 WeekStart = %q", got)
	}
	// 周一本身
	mon, _ := ParseLocal("2026-10-05 08:00:00")
	if got := FormatLocal(WeekStart(mon)); got != "2026-10-05 00:00:00" {
		t.Fatalf("周一 WeekStart = %q", got)
	}
}
