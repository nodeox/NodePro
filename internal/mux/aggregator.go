package mux

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
)

// Aggregator 负责将数据分片并分发到多个连接
type Aggregator struct {
	conns      []net.Conn
	nextConn   atomic.Int32
	sessionID  [16]byte
	chunkSize  int
}

// NewAggregator 创建一个新的带宽聚合器
func NewAggregator(conns []net.Conn, sessionID [16]byte, chunkSize int) *Aggregator {
	if chunkSize <= 0 {
		chunkSize = 64 * 1024 // 默认 64KB
	}
	return &Aggregator{
		conns:     conns,
		sessionID: sessionID,
		chunkSize: chunkSize,
	}
}

// Write 将大块数据分片并写入各个底层连接
func (a *Aggregator) Write(data []byte) (int, error) {
	totalLen := len(data)
	seq := uint32(0)
	
	for len(data) > 0 {
		size := len(data)
		if size > a.chunkSize {
			size = a.chunkSize
		}
		
		chunk := data[:size]
		data = data[size:]
		
		// 构造聚合协议头部 (根据 NodePass-2.0-Protocol.md)
		// Header: SessionID(16) + Seq(4) + ChunkSize(2) + Flags(1) + Reserved(1) = 24 bytes
		buf := new(bytes.Buffer)
		buf.Write(a.sessionID[:])
		binary.Write(buf, binary.BigEndian, seq)
		binary.Write(buf, binary.BigEndian, uint16(size))
		
		flags := byte(0)
		if len(data) == 0 {
			flags |= 0x01 // FIN
		}
		buf.WriteByte(flags)
		buf.WriteByte(0) // Reserved
		buf.Write(chunk)
		
		// 轮询选择物理连接
		idx := int(a.nextConn.Add(1)-1) % len(a.conns)
		conn := a.conns[idx]
		
		if _, err := conn.Write(buf.Bytes()); err != nil {
			return 0, fmt.Errorf("failed to write chunk to connection %d: %w", idx, err)
		}
		
		seq++
	}
	
	return totalLen, nil
}
