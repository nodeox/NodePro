package common

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"time"
)

// SniffedResult 嗅探到的元数据
type SniffedResult struct {
	Protocol string // "tls", "http"
	Domain   string // SNI 或 Host
}

// SniffConn 尝试嗅探连接中的域名信息
// 它会读取初始数据包并返回一个包装后的 net.Conn，确保数据不会丢失
func SniffConn(conn net.Conn) (net.Conn, *SniffedResult) {
	// 设置一个极短的超时，防止 Peek 永久阻塞
	originalDeadline := time.Time{}
	if _, ok := conn.(*net.TCPConn); ok {
		// 记录原始 Deadline (虽然 net.Conn 没有直接获取方法，此处简化处理)
	}
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	defer conn.SetReadDeadline(originalDeadline)

	br := bufio.NewReader(conn)
	peek, err := br.Peek(5)
	if err != nil {
		return &PeekedConn{Reader: br, Conn: conn}, nil
	}

	result := &SniffedResult{}

	// 1. 尝试识别 TLS (0x16 是 Handshake)
	if peek[0] == 0x16 && peek[1] == 0x03 {
		if domain, ok := sniffTLS(br); ok {
			result.Protocol = "tls"
			result.Domain = domain
			return &PeekedConn{Reader: br, Conn: conn}, result
		}
	}

	// 2. 尝试识别 HTTP
	methods := []string{"GET ", "POST", "HEAD", "PUT ", "DELE", "CONN"}
	isHTTP := false
	for _, m := range methods {
		if string(peek[:4]) == m {
			isHTTP = true
			break
		}
	}
	if isHTTP {
		if domain, ok := sniffHTTP(br); ok {
			result.Protocol = "http"
			result.Domain = domain
			return &PeekedConn{Reader: br, Conn: conn}, result
		}
	}

	return &PeekedConn{Reader: br, Conn: conn}, nil
}

func sniffTLS(br *bufio.Reader) (string, bool) {
	header, err := br.Peek(5)
	if err != nil { return "", false }
	
	length := int(binary.BigEndian.Uint16(header[3:5]))
	if length > 4096 { length = 4096 }

	// 再次 Peek 实际负载
	data, err := br.Peek(5 + length)
	if err != nil {
		// 如果数据不够，尝试按实际已有的数据解析
		data, err = br.Peek(br.Buffered())
		if err != nil || len(data) < 5 { return "", false }
	}

	data = data[5:]
	if len(data) < 4 || data[0] != 0x01 { return "", false }

	pos := 4 + 2 + 32
	if len(data) < pos+1 { return "", false }
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	if len(data) < pos+2 { return "", false }
	cipherLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2 + cipherLen

	if len(data) < pos+1 { return "", false }
	compLen := int(data[pos])
	pos += 1 + compLen

	if len(data) < pos+2 { return "", false }
	extLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	end := pos + extLen
	if end > len(data) { end = len(data) }

	for pos+4 <= end {
		eType := binary.BigEndian.Uint16(data[pos : pos+2])
		eLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if eType == 0x00 {
			if pos+eLen > end { return "", false }
			snPos := pos + 2
			if snPos+3 > pos+eLen { return "", false }
			nameLen := int(binary.BigEndian.Uint16(data[snPos+1 : snPos+3]))
			if snPos+3+nameLen > pos+eLen { return "", false }
			return string(data[snPos+3 : snPos+3+nameLen]), true
		}
		pos += eLen
	}
	return "", false
}

func sniffHTTP(br *bufio.Reader) (string, bool) {
	// 使用 Buffered() 避免 Peek 阻塞等待 4096 字节
	n := br.Buffered()
	if n < 16 { n = 1024 } // 至少尝试读一些，如果还没缓存
	if n > 4096 { n = 4096 }
	
	peek, _ := br.Peek(n)
	lines := strings.Split(string(peek), "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			host := strings.TrimSpace(line[5:])
			if h, _, err := net.SplitHostPort(host); err == nil {
				return h, true
			}
			return host, true
		}
	}
	return "", false
}

// PeekedConn 包装 net.Conn 以支持回放 Peek 过的数据
type PeekedConn struct {
	net.Conn
	Reader io.Reader
}

func (c *PeekedConn) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}
