package core

import (
	"fmt"
	"strings"
	"time"

	// 内嵌时区数据库,保证 Windows 等无系统时区库的平台也能解析香港时区
	_ "time/tzdata"
)

// TargetLocation 开售时间固定按香港时区理解;时区库缺失属于环境损坏,就地崩溃
var TargetLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Hong_Kong")
	if err != nil {
		panic(err)
	}
	return loc
}()

// ParseTargetTime 把 "2006-01-02 15:04:05" 格式的时间字符串按香港时区解析
func ParseTargetTime(timeStr string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", timeStr, TargetLocation)
	if err != nil {
		return time.Time{}, fmt.Errorf("时间格式应为 YYYY-MM-DD HH:MM:SS: %w", err)
	}
	return t, nil
}

// Timestamp 返回当前时刻的 HH:MM:SS.mmm 字符串,用于日志前缀
func Timestamp() string {
	return time.Now().Format("15:04:05.000")
}

// Logf 格式化一条日志,加上毫秒级时间戳前缀后交给 send;多行消息逐行发送,空行跳过
func Logf(send func(string), format string, args ...any) {
	for _, line := range strings.Split(fmt.Sprintf(format, args...), "\n") {
		if line != "" {
			send(fmt.Sprintf("[%s] %s", Timestamp(), line))
		}
	}
}
