package common

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"time"
)

// ObfsConn 包装 net.Conn 以实现数据包长度和时序混淆
// 协议格式：[PayloadLen (2 bytes)] [PadLen (1 byte)] [Payload] [Padding]
// PayloadLen == 0 表示这是一个纯心跳/伪造包，接收端应直接丢弃
type ObfsConn struct {
	net.Conn
	maxPad   int
	interval time.Duration
	dummy    int
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewObfsConn(conn net.Conn, cfg ObfsConfig) net.Conn {
	if cfg.Type != "padding" && cfg.Interval <= 0 {
		return conn
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	c := &ObfsConn{
		Conn:     conn,
		maxPad:   cfg.MaxPad,
		interval: time.Duration(cfg.Interval) * time.Millisecond,
		dummy:    cfg.DummySize,
		ctx:      ctx,
		cancel:   cancel,
	}

	if c.interval > 0 {
		go c.heartbeatLoop()
	}
	
	return c
}

func (c *ObfsConn) heartbeatLoop() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 发送伪造包 (PayloadLen = 0)
			c.sendFrame(nil, c.dummy)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *ObfsConn) sendFrame(p []byte, forcePad int) error {
	payloadLen := len(p)
	
	padBuf := make([]byte, 1)
	rand.Read(padBuf)
	padLen := int(padBuf[0]) % (c.maxPad + 1)
	if forcePad > padLen {
		padLen = forcePad
	}

	header := make([]byte, 3)
	binary.BigEndian.PutUint16(header[0:2], uint16(payloadLen))
	header[2] = byte(padLen)

	buf := make([]byte, 3+payloadLen+padLen)
	copy(buf[0:3], header)
	if payloadLen > 0 {
		copy(buf[3:3+payloadLen], p)
	}
	if padLen > 0 {
		rand.Read(buf[3+payloadLen:])
	}

	_, err := c.Conn.Write(buf)
	return err
}

func (c *ObfsConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	err := c.sendFrame(p, 0)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *ObfsConn) Read(p []byte) (int, error) {
	for {
		header := make([]byte, 3)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}

		payloadLen := int(binary.BigEndian.Uint16(header[0:2]))
		padLen := int(header[2])

		// 如果 payloadLen 为 0，说明是伪造包，跳过负载读取直接丢弃 Padding 并继续循环
		if payloadLen == 0 {
			if padLen > 0 {
				if _, err := io.CopyN(io.Discard, c.Conn, int64(padLen)); err != nil {
					return 0, err
				}
			}
			continue
		}

		if payloadLen > len(p) {
			return 0, io.ErrShortBuffer
		}

		if _, err := io.ReadFull(c.Conn, p[:payloadLen]); err != nil {
			return 0, err
		}

		if padLen > 0 {
			if _, err := io.CopyN(io.Discard, c.Conn, int64(padLen)); err != nil {
				return 0, err
			}
		}

		return payloadLen, nil
	}
}

func (c *ObfsConn) Close() error {
	c.cancel()
	return c.Conn.Close()
}
