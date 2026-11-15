package sheetparse

import (
	"testing"

	"ledger/internal/pkg/timeutil"
)

// TestDistributeOffsetBoundaries §4.1 边界表逐值断言。
func TestDistributeOffsetBoundaries(t *testing.T) {
	cases := []struct {
		name string
		n, i int
		want int
	}{
		{"单条", 1, 0, 43200},              // 12:00:00
		{"两条-0", 2, 0, 21600},            // 06:00:00
		{"两条-1", 2, 1, 64800},            // 18:00:00
		{"三条-0", 3, 0, 14400},            // 04:00:00
		{"三条-1", 3, 1, 43200},            // 12:00:00
		{"三条-2", 3, 2, 72000},            // 20:00:00
		{"极多", 86400, 86399, 86399},      // 23:59:59
		{"超过一天秒数", 100000, 99999, 86399}, // clamp
	}
	for _, c := range cases {
		if got := DistributeOffset(c.i, c.n); got != c.want {
			t.Errorf("%s: DistributeOffset(%d,%d) = %d，want %d", c.name, c.i, c.n, got, c.want)
		}
	}
	if got := DistributeOffset(0, 0); got != 43200 {
		t.Errorf("n=0 兜底应为 43200，实得 %d", got)
	}
}

// TestDistributeMonotonic 单调性：同日顺序 == 源顺序。
func TestDistributeMonotonic(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7, 100, 86400} {
		prev := -1
		for i := 0; i < n; i++ {
			step := DistributeOffset(i, n)
			if step <= prev {
				t.Fatalf("n=%d i=%d step=%d 未严格递增（prev=%d）", n, i, step, prev)
			}
			if step > 86399 {
				t.Fatalf("n=%d i=%d step=%d 越界", n, i, step)
			}
			prev = step
		}
	}
}

// TestDistributeDayTimes 跨日不影响：不同 local_date 各自分布，日期不变。
func TestDistributeDayTimes(t *testing.T) {
	d1, err := timeutil.ParseDateStart("2018-10-10")
	if err != nil {
		t.Fatal(err)
	}
	d2, err := timeutil.ParseDateStart("2018-10-11")
	if err != nil {
		t.Fatal(err)
	}
	times := DistributeDayTimes(d1, 3)
	want := []string{"2018-10-10 04:00:00", "2018-10-10 12:00:00", "2018-10-10 20:00:00"}
	if len(times) != 3 {
		t.Fatalf("期望 3 个时间，实得 %d", len(times))
	}
	for i, wantStr := range want {
		if got := timeutil.FormatLocal(times[i]); got != wantStr {
			t.Errorf("第 %d 条 = %s，want %s", i, got, wantStr)
		}
		if got := timeutil.LocalDate(times[i]); got != "2018-10-10" {
			t.Errorf("第 %d 条日期漂移: %s", i, got)
		}
	}
	// 另一天的分布不受影响
	other := DistributeDayTimes(d2, 1)
	if got := timeutil.FormatLocal(other[0]); got != "2018-10-11 12:00:00" {
		t.Errorf("跨日分布 = %s，want 2018-10-11 12:00:00", got)
	}
}

// TestDistributeOverflow N > 86400 时给出 warning 标记且时间仍不越界。
func TestDistributeOverflow(t *testing.T) {
	if !DistributeOverflowWarning(100000) {
		t.Error("N=100000 应触发 overflow warning")
	}
	if DistributeOverflowWarning(86400) {
		t.Error("N=86400 不应触发 warning")
	}
	d, _ := timeutil.ParseDateStart("2020-01-01")
	times := DistributeDayTimes(d, 3)
	for _, ts := range times {
		if got := timeutil.LocalDate(ts); got != "2020-01-01" {
			t.Errorf("时间越界到 %s", got)
		}
	}
}
