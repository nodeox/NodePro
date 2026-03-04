package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// UDPPacket SOCKS5 UDP 数据包结构
type UDPPacket struct {
	Frag     byte
	AddrType byte
	Address  string
	Port     uint16
	Data     []byte
}

// ParseUDPPacket 解析来自客户端的 UDP 数据包，增加边界校验
func ParseUDPPacket(b []byte) (*UDPPacket, error) {
	if len(b) < 10 {
		return nil, errors.New("packet too short")
	}
	p := &UDPPacket{
		Frag:     b[2],
		AddrType: b[3],
	}

	offset := 4
	var host string
	switch p.AddrType {
	case 0x01: // IPv4
		if len(b) < offset+4+2 { return nil, errors.New("invalid ipv4 packet") }
		host = net.IP(b[offset : offset+4]).String()
		offset += 4
	case 0x03: // Domain
		if len(b) < offset+1 { return nil, errors.New("invalid domain packet") }
		l := int(b[offset])
		offset++
		if len(b) < offset+l+2 { return nil, errors.New("domain name too long") }
		host = string(b[offset : offset+l])
		offset += l
	case 0x04: // IPv6
		if len(b) < offset+16+2 { return nil, errors.New("invalid ipv6 packet") }
		host = net.IP(b[offset : offset+16]).String()
		offset += 16
	default:
		return nil, fmt.Errorf("unsupported addr type: %d", p.AddrType)
	}

	p.Port = binary.BigEndian.Uint16(b[offset : offset+2])
	p.Address = net.JoinHostPort(host, fmt.Sprintf("%d", p.Port))
	p.Data = b[offset+2:]

	return p, nil
}

// PackUDPPacket ... (保持不变)
func PackUDPPacket(addr string, data []byte) ([]byte, error) {
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := net.LookupPort("udp", portStr)
	res := []byte{0x00, 0x00, 0x00} 
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			res = append(res, 0x01); res = append(res, ip4...)
		} else {
			res = append(res, 0x04); res = append(res, ip.To16()...)
		}
	} else {
		res = append(res, 0x03); res = append(res, byte(len(host))); res = append(res, host...)
	}
	pBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(pBuf, uint16(port))
	res = append(res, pBuf...); res = append(res, data...)
	return res, nil
}
