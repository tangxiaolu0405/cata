// Package clock 提供 Cata 统一时区（默认 Asia/Shanghai），用于日志、llm.log、演进与 short-term 时间戳。
package clock

import (
	"os"
	"sync"
	"time"
)

const (
	EnvTimezone     = "CATA_TIMEZONE"
	DefaultTimezone = "Asia/Shanghai"
)

var (
	mu  sync.Mutex
	loc *time.Location
)

// Init 加载 IANA 时区并设为进程本地时区（影响标准 log 与 time.Now().Format）。
// name 为空时使用 CATA_TIMEZONE 环境变量，再否则 DefaultTimezone。
func Init(name string) error {
	mu.Lock()
	defer mu.Unlock()

	if name == "" {
		name = os.Getenv(EnvTimezone)
	}
	if name == "" {
		name = DefaultTimezone
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		l = time.FixedZone("CST", 8*3600)
	}
	loc = l
	time.Local = l
	return nil
}

// Location 返回当前配置的时区。
func Location() *time.Location {
	mu.Lock()
	defer mu.Unlock()
	if loc == nil {
		_ = Init("")
	}
	return loc
}

// Now 返回配置时区的当前时间。
func Now() time.Time {
	return time.Now().In(Location())
}

// RFC3339 返回配置时区下的 RFC3339 时间字符串（含 +08:00 等偏移）。
func RFC3339() string {
	return Now().Format(time.RFC3339)
}

// Format 在配置时区下格式化当前时间。
func Format(layout string) string {
	return Now().Format(layout)
}

// FormatTime 将 t 格式化为配置时区下的 layout。
func FormatTime(t time.Time, layout string) string {
	return t.In(Location()).Format(layout)
}
