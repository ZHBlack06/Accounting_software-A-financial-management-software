package sheetparse

// DistributeOffset 返回同一天内第 i 条（共 n 条）相对当日 00:00:00 的秒偏移。
//
//	step = (i*86400 + 43200) / n    // 整数除法 == floor((i+0.5)*86400/n)
//	step = min(step, 86399)         // 兜底：永不超过 23:59:59
//
// 加 0.5 让采样点落在每个等分桶的中心：n=1 → 12:00:00，n=2 → 06:00/18:00。
// 单调性：n <= 86400 时 step 严格递增，保证同日顺序 == 源顺序。
func DistributeOffset(i, n int) int {
	if n <= 0 {
		return 43200
	}
	if i < 0 {
		i = 0
	}
	step := (i*86400 + 43200) / n
	if step > 86399 {
		step = 86399
	}
	if step < 0 {
		step = 0
	}
	return step
}

// DistributeDayTimes 返回 dayStart 起、同日 n 条记录的全部分布时间（UTC 秒）。
// dayStart 必须是本地当天 00:00:00 的 UTC 秒（timeutil.ParseDateStart）。
func DistributeDayTimes(dayStart int64, n int) []int64 {
	if n <= 0 {
		return nil
	}
	out := make([]int64, n)
	for i := 0; i < n; i++ {
		out[i] = dayStart + int64(DistributeOffset(i, n))
	}
	return out
}

// DistributeOverflowWarning 当日记录数超过 86400 条时返回 true（时间仅用于排序）。
func DistributeOverflowWarning(n int) bool { return n > 86400 }
