// Package timeutil 统一 UTC 秒（DB）⇄ 本地字符串（API，Asia/Shanghai）转换，
// 以及日/周/月/年分桶辅助。时区加载失败时回退固定 +08:00（Windows 无 tzdata）。
package timeutil

import (
	"strings"
	"time"
)

const (
	// Layout 是 API 层时间字符串格式。
	Layout = "2006-01-02 15:04:05"
	// DateLayout 是 API 层纯日期格式。
	DateLayout = "2006-01-02"
)

var local *time.Location

func init() {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	local = loc
}

// Local 返回应用时区。
func Local() *time.Location { return local }

// NowUnix 返回当前 UTC 秒。
func NowUnix() int64 { return time.Now().Unix() }

// ParseLocal 解析 'YYYY-MM-DD HH:mm:ss' 为 UTC 秒。
func ParseLocal(s string) (int64, error) {
	t, err := time.ParseInLocation(Layout, strings.TrimSpace(s), local)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// ParseDateStart 解析纯日期为当天 00:00:00 的 UTC 秒。
func ParseDateStart(s string) (int64, error) {
	t, err := time.ParseInLocation(DateLayout, strings.TrimSpace(s), local)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// ParseDateEnd 解析纯日期为当天 23:59:59 的 UTC 秒。
func ParseDateEnd(s string) (int64, error) {
	start, err := ParseDateStart(s)
	if err != nil {
		return 0, err
	}
	return start + 86399, nil
}

// ParseTimeOrDate 兼容两种输入：纯日期（isEnd 决定补 00:00:00 / 23:59:59）或完整时间。
func ParseTimeOrDate(s string, isEnd bool) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == len(DateLayout) {
		if isEnd {
			return ParseDateEnd(s)
		}
		return ParseDateStart(s)
	}
	return ParseLocal(s)
}

// FormatLocal 把 UTC 秒格式化为 'YYYY-MM-DD HH:mm:ss'（本地时区）。
func FormatLocal(unix int64) string {
	return time.Unix(unix, 0).In(local).Format(Layout)
}

// FormatLocalPtr 处理可空时间戳；nil/0 返回空串。
func FormatLocalPtr(unix *int64) string {
	if unix == nil || *unix == 0 {
		return ""
	}
	return FormatLocal(*unix)
}

// LocalDate 返回 UTC 秒对应的本地日期 'YYYY-MM-DD'。
func LocalDate(unix int64) string {
	return time.Unix(unix, 0).In(local).Format(DateLayout)
}

// LocalTime 返回 UTC 秒对应的本地 time.Time。
func LocalTime(unix int64) time.Time {
	return time.Unix(unix, 0).In(local)
}

// DayStart 返回本地某日 00:00:00 的 UTC 秒。
func DayStart(unix int64) int64 {
	t := LocalTime(unix)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, local).Unix()
}

// WeekStart 返回本地时间所属周（周一为起点）00:00:00 的 UTC 秒。
func WeekStart(unix int64) int64 {
	t := LocalTime(unix)
	y, m, d := t.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, local)
	offset := (int(midnight.Weekday()) + 6) % 7 // Mon=0 ... Sun=6
	return midnight.AddDate(0, 0, -offset).Unix()
}

// MonthStart 返回本地时间所属月 1 日 00:00:00 的 UTC 秒。
func MonthStart(unix int64) int64 {
	t := LocalTime(unix)
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, local).Unix()
}

// YearStart 返回本地时间所属年 1 月 1 日 00:00:00 的 UTC 秒。
func YearStart(unix int64) int64 {
	t := LocalTime(unix)
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, local).Unix()
}

// AddDays 在本地日历上加减天数后取 00:00:00 的 UTC 秒。
func AddDays(unix int64, days int) int64 {
	return LocalTime(unix).AddDate(0, 0, days).Unix()
}
