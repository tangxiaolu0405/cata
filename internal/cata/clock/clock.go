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

// init 在**包初始化阶段**（单线程，先于任何 goroutine）就把时区落到全局 time.Local。
//
// 为什么不能懒加载：time.Local 是标准库全局变量，会被 time.Now() / time.Unix() /
// time.Since() 等并发读取（net/http、log 都在读）。若推迟到首次调用 clock.Now() 才写，
// 就会与其它 goroutine 的 time.Now() 形成数据竞争（-race 可复现，服务器进程里几乎必然踩中）。
func init() {
	mu.Lock()
	defer mu.Unlock()
	ensureLocationLocked("")
}

// Init 加载 IANA 时区并设为进程本地时区（影响标准 log 与 time.Now().Format）。
// name 为空时使用 CATA_TIMEZONE 环境变量，再否则 DefaultTimezone。
func Init(name string) error {
	mu.Lock()
	defer mu.Unlock()
	ensureLocationLocked(name)
	return nil
}

func ensureLocationLocked(name string) {
	// name 为空 = 用已解析的值（无则环境变量 → 默认）；非空 = 显式指定（配置的 server.timezone）。
	if name == "" && loc != nil {
		return
	}
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
	if loc != nil && loc.String() == l.String() {
		return // 同一时区：不重复写全局 time.Local（避免与并发时间调用竞态）
	}
	loc = l
	time.Local = l
}

// Location 返回当前配置的时区。
func Location() *time.Location {
	mu.Lock()
	defer mu.Unlock()
	ensureLocationLocked("")
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
