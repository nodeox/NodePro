package common

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

var (
	v2Signature = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}
)

// ProxyProtoConn 包装 net.Conn 以处理 PROXY protocol
type ProxyProtoConn struct {
	net.Conn
	reader     *bufio.Reader
	remoteAddr net.Addr
}

func (c *ProxyProtoConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return c.Conn.RemoteAddr()
}

func (c *ProxyProtoConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

// WrapProxyProto 尝试解析 PROXY protocol 头部
func WrapProxyProto(conn net.Conn) (net.Conn, error) {
	br := bufio.NewReader(conn)
	
	// 查看前几个字节判断版本
	peek, err := br.Peek(12)
	if err != nil {
		return nil, err
	}

	pc := &ProxyProtoConn{Conn: conn, reader: br}

	if bytes.Equal(peek[:12], v2Signature) {
		// V2 Binary Protocol
		addr, err := parseV2(br)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy proto v2: %w", err)
		}
		pc.remoteAddr = addr
	} else if bytes.HasPrefix(peek, []byte("PROXY ")) {
		// V1 Text Protocol
		addr, err := parseV1(br)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy proto v1: %w", err)
		}
		pc.remoteAddr = addr
	}

	return pc, nil
}

func parseV1(br *bufio.Reader) (net.Addr, error) {
	// 示例: PROXY TCP4 192.168.0.1 192.168.0.11 56324 443\r\n
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	parts := strings.Split(line, " ")
	if len(parts) < 6 {
		return nil, errors.New("invalid v1 header format")
	}

	srcIP := net.ParseIP(parts[2])
	srcPort, _ := strconv.Atoi(parts[4])
	
	return &net.TCPAddr{
		IP:   srcIP,
		Port: srcPort,
	}, nil
}

func parseV2(br *bufio.Reader) (net.Addr, error) {
	// 跳过签名
	if _, err := br.Discard(12); err != nil {
		return nil, err
	}

	// 13 字节: Version & Command
	vc, err := br.ReadByte()
	if err != nil {
		return nil, err
	}
	if vc>>4 != 2 {
		return nil, errors.New("unsupported version")
	}

	// 14 字节: Address Family & Protocol
	af, err := br.ReadByte()
	if err != nil {
		return nil, err
	}

	// 15-16 字节: Address Length
	var length uint16
	if err := binary.Read(br, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	data := make([]byte, int(length))
	if _, err := io.ReadFull(br, data); err != nil {
		return nil, err
	}

	// 如果 Command 是 LOCAL (0x0)，则不替换地址
	if vc&0x0F == 0x00 {
		return nil, nil
	}

	switch af {
	case 0x11: // TCP over IPv4
		if len(data) < 12 { return nil, errors.New("insufficient data for ipv4") }
		return &net.TCPAddr{
			IP:   net.IP(data[0:4]),
			Port: int(binary.BigEndian.Uint16(data[8:10])),
		}, nil
	case 0x21: // TCP over IPv6
		if len(data) < 36 { return nil, errors.New("insufficient data for ipv6") }
		return &net.TCPAddr{
			IP:   net.IP(data[0:16]),
			Port: int(binary.BigEndian.Uint16(data[32:34])),
		}, nil
	}

	return nil, nil
}

// ProxyProtoListener 包装 net.Listener 以自动处理新连接的 PROXY protocol
type ProxyProtoListener struct {
	net.Listener
}

func (l *ProxyProtoListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return WrapProxyProto(conn)
}
