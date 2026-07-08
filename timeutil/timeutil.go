// Package timeutil 提供面向用户展示的统一时间格式。
//
// 仓库内时间展示曾混用 time.RFC3339（"2006-01-02T15:04:05Z"）、
// "2006-01-02 15:04:05"（部分 UTC、部分本地时区）以及前端
// toLocaleString 产生的斜杠格式，导致同一时刻出现多种显示。
// 本包统一约定：所有面向用户展示的时间一律使用 DisplayLayout，
// 并以 UTC 输出，避免时区漂移。
package timeutil

import "time"

// DisplayLayout 是面向用户展示的统一时间格式：2006-01-02 15:04:05。
const DisplayLayout = "2006-01-02 15:04:05"

// Display 将时间格式化为统一的展示格式，统一以 UTC 输出。
func Display(t time.Time) string {
	return t.UTC().Format(DisplayLayout)
}

// NowDisplay 返回当前时间的统一展示格式字符串。
func NowDisplay() string {
	return Display(time.Now())
}
