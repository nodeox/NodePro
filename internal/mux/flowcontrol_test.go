package mux

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestWindowFlowController(t *testing.T) {
	maxWindow := int64(1024)
	fc := NewWindowFlowController(maxWindow)

	// 1. 初始状态
	assert.Equal(t, int(maxWindow), fc.WindowSize())
	assert.False(t, fc.ShouldPause())

	// 2. 缓冲数据
	fc.OnBuffered(512)
	assert.Equal(t, 512, fc.WindowSize())
	assert.False(t, fc.ShouldPause())

	// 3. 达到阈值
	fc.OnBuffered(512)
	assert.True(t, fc.ShouldPause())
	assert.Equal(t, 0, fc.WindowSize())

	// 4. 消费数据
	fc.OnConsumed(256)
	assert.False(t, fc.ShouldPause())
	assert.Equal(t, 256, fc.WindowSize())

	// 5. 重置
	fc.Reset()
	assert.Equal(t, int(maxWindow), fc.WindowSize())
}
