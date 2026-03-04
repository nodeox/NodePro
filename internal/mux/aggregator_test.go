package mux

import (
	"io"
	"net"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/google/uuid"
)

func TestAggregatorReassembler(t *testing.T) {
	// 1. 创建模拟连接
	p1Client, p1Server := net.Pipe()
	p2Client, p2Server := net.Pipe()
	
	sessionID := [16]byte(uuid.New())
	chunkSize := 1024
	
	// 2. 初始化聚合器和重组器
	agg := NewAggregator([]net.Conn{p1Client, p2Client}, sessionID, chunkSize)
	
	fc := NewWindowFlowController(1024 * 1024)
	reas := NewReassembler(fc)
	
	// 3. 启动接收协程 (模拟两个物理连接接收分片)
	go func() {
		for _, conn := range []net.Conn{p1Server, p2Server} {
			go func(c net.Conn) {
				buf := make([]byte, chunkSize + 100)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					// 仅传递读到的字节副本
					tmp := make([]byte, n)
					copy(tmp, buf[:n])
					reas.Push(tmp)
				}
			}(conn)
		}
	}()
	
	// 4. 发送大块数据
	testData := make([]byte, 5000)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	n, err := agg.Write(testData)
	assert.NoError(t, err)
	assert.Equal(t, 5000, n)
	
	// 5. 读取重组后的数据
	receivedData := make([]byte, 5000)
	totalRead := 0
	for totalRead < 5000 {
		n, err = reas.Read(receivedData[totalRead:])
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)
		totalRead += n
	}
	
	// 6. 验证一致性
	assert.Equal(t, 5000, totalRead)
	assert.Equal(t, testData, receivedData)
}
