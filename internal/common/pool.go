package common

import "sync"

const (
	// BufferSize 标准缓冲区大小 (32KB)
	BufferSize = 32 * 1024
)

var (
	// BufPool 全局缓冲区池
	BufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, BufferSize)
		},
	}
)

// GetBuf 从池中获取一个缓冲区
func GetBuf() []byte {
	return BufPool.Get().([]byte)
}

// PutBuf 将缓冲区归还到池中
func PutBuf(buf []byte) {
	if len(buf) != BufferSize {
		return
	}
	// 修复：归还前清零数据，防止敏感信息泄漏
	for i := range buf {
		buf[i] = 0
	}
	BufPool.Put(buf)
}
