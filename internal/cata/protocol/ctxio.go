// Package protocol 承载 cata Unix socket 聊天协议的纯协议层：
//   - 带 ctx 的文件读取（ctxio）
//   - 流式 chat 期间的客户端行复用（connLineReader / socket_readmux）
//   - 执行确认 / 用户选择等待（confirm）
//
// 该层不依赖 server 的业务类型（工具注册、子代理、任务状态机等），
// 是 server 包与「连接/协议」边界的独立单元，便于单测与复用。
package protocol

import (
	"context"
	"io"
	"os"
)

const ctxReadChunkSize = 64 * 1024

// ContextErr 返回 ctx 的取消错误；ctx 为 nil 或未取消时返回 nil。
func ContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// ReadFileWithContext 读取文件至多 maxBytes 字节，分块间检查 ctx 取消。
// maxBytes <= 0 时默认 512KB。
func ReadFileWithContext(ctx context.Context, path string, maxBytes int) ([]byte, error) {
	if err := ContextErr(ctx); err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 0, minInt(ctxReadChunkSize, maxBytes))
	chunk := make([]byte, ctxReadChunkSize)
	for len(buf) < maxBytes {
		if err := ContextErr(ctx); err != nil {
			return nil, err
		}
		n, err := f.Read(chunk)
		if n > 0 {
			remain := maxBytes - len(buf)
			if n > remain {
				n = remain
			}
			buf = append(buf, chunk[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
