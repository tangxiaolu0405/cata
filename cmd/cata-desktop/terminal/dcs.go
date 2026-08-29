package terminal

import (
	"encoding/hex"
	"log"
)

func (t *Terminal) handleDCS(code string) {
	if len(code) >= 2 && code[:2] == "+q" {
		query, _ := hex.DecodeString(code[2:]) // strip the +q
		if t.debug {
			log.Println("unhandled DCS query", query)
		}

		// DECRQSS（终端能力查询）：本终端未实现这些扩展能力，按标准用 DCS 0+r 应答
		// "not recognised"（而非静默忽略），避免 shell/程序等待应答挂起。
		_, _ = t.in.Write([]byte{asciiEscape})
		_, _ = t.in.Write([]byte("P0+r"))
		_, _ = t.in.Write([]byte{asciiEscape, '\\', 0})
	} else {
		if t.debug {
			log.Println("unknown DCS query", code)
		}
	}
}
