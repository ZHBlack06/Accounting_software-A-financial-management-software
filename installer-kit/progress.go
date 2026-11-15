package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// LogLevel 是日志级别，前端据此着色。
type LogLevel string

// 日志级别常量。
const (
	LevelInfo  LogLevel = "info"
	LevelStep  LogLevel = "step"
	LevelOK    LogLevel = "ok"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// LogLine 是单条进度日志。
type LogLine struct {
	Index   int      `json:"index"`
	Level   LogLevel `json:"level"`
	Message string   `json:"message"`
	Time    string   `json:"time"`
}

// Progress 线程安全地记录安装/卸载进度、日志与最终结果。
// 图形界面通过 Snapshot 增量拉取；静默模式通过 echo 直接打印到标准输出。
type Progress struct {
	mu         sync.Mutex
	lines      []LogLine
	percent    int
	done       bool
	ok         bool
	errMessage string
	resultDir  string
	echo       bool
}

// NewProgress 创建一个进度记录器。echo 为 true 时日志会同时写入标准输出。
func NewProgress(echo bool) *Progress {
	return &Progress{echo: echo, percent: 0}
}

// Log 追加一条日志。
func (p *Progress) Log(level LogLevel, format string, args ...interface{}) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	p.mu.Lock()
	line := LogLine{
		Index:   len(p.lines),
		Level:   level,
		Message: msg,
		Time:    time.Now().Format("15:04:05"),
	}
	p.lines = append(p.lines, line)
	echo := p.echo
	p.mu.Unlock()

	if echo {
		prefix := "[INFO] "
		switch level {
		case LevelStep:
			prefix = "[STEP] "
		case LevelOK:
			prefix = "[ OK ] "
		case LevelWarn:
			prefix = "[WARN] "
		case LevelError:
			prefix = "[FAIL] "
		}
		fmt.Fprintf(os.Stdout, "%s%s\n", prefix, msg)
	}
}

// Step 记录一个阶段性动作，并同步推进进度条。
func (p *Progress) Step(percent int, format string, args ...interface{}) {
	p.SetPercent(percent)
	p.Log(LevelStep, format, args...)
}

// SetPercent 设置进度百分比（0-100，自动收敛到合法区间）。
func (p *Progress) SetPercent(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	p.mu.Lock()
	if v > p.percent {
		p.percent = v
	}
	p.mu.Unlock()
}

// Percent 返回当前进度百分比。
func (p *Progress) Percent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.percent
}

// Finish 标记流程结束。err 为 nil 表示成功。
func (p *Progress) Finish(err error, dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.resultDir = dir
	if err == nil {
		p.ok = true
		p.percent = 100
	} else {
		p.ok = false
		p.errMessage = err.Error()
	}
}

// Snapshot 返回 since 之后的增量日志与整体状态。
func (p *Progress) Snapshot(since int) (lines []LogLine, percent int, done bool, ok bool, errMessage string, resultDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if since < 0 {
		since = 0
	}
	if since < len(p.lines) {
		lines = append(lines, p.lines[since:]...)
	}
	return lines, p.percent, p.done, p.ok, p.errMessage, p.resultDir
}

// LineCount 返回已记录的日志条数，前端用作增量游标。
func (p *Progress) LineCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.lines)
}

// Done 判断流程是否已经结束。
func (p *Progress) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// Reset 清空状态（用于「重新尝试」场景）。
func (p *Progress) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = nil
	p.percent = 0
	p.done = false
	p.ok = false
	p.errMessage = ""
	p.resultDir = ""
}
