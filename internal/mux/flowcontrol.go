package mux

import (
	"sync/atomic"
)

// WindowFlowController 实现窗口式背压流控
type WindowFlowController struct {
	maxWindow int64         // 最大允许缓冲区大小 (16MB)
	buffered  atomic.Int64  // 当前已缓冲未消费的字节数
	consumed  atomic.Int64  // 累计已消费字节数
}

// NewWindowFlowController 创建流控器
func NewWindowFlowController(maxWindow int64) *WindowFlowController {
	if maxWindow <= 0 {
		maxWindow = 16 * 1024 * 1024 // 默认 16MB
	}
	return &WindowFlowController{
		maxWindow: maxWindow,
	}
}

// ShouldPause 判断是否应该暂停上游读取
func (w *WindowFlowController) ShouldPause() bool {
	return w.buffered.Load() >= w.maxWindow
}

// OnBuffered 当数据进入缓冲区时调用
func (w *WindowFlowController) OnBuffered(n int) {
	w.buffered.Add(int64(n))
}

// OnConsumed 当数据被读取消费时调用
func (w *WindowFlowController) OnConsumed(n int) {
	w.consumed.Add(int64(n))
	w.buffered.Add(-int64(n))
}

// WindowSize 获取当前窗口可用容量
func (w *WindowFlowController) WindowSize() int {
	remain := w.maxWindow - w.buffered.Load()
	if remain < 0 {
		return 0
	}
	return int(remain)
}

// Reset 重置流控器状态
func (w *WindowFlowController) Reset() {
	w.buffered.Store(0)
	w.consumed.Store(0)
}
