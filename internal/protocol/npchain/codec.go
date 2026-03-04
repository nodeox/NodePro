package npchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
)

const (
	Magic        = uint32(0x4E504332) // "NPC2"
	Version      = byte(0x01)
	HeaderSize   = 24
	HopEntrySize = 34
)

// Header NP-Chain 协议头部
type Header struct {
	Magic     uint32
	Version   byte
	NHops     byte
	Reserved  uint16
	SessionID [16]byte
}

// HopEntry 跳数条目
type HopEntry struct {
	Type    byte // 0x01: IPv4, 0x02: IPv6, 0x03: Domain
	Port    uint16
	AddrLen byte
	Address [30]byte
}

// EncodeHeader 编码协议头部和跳数列表
func EncodeHeader(meta common.SessionMeta) ([]byte, error) {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.BigEndian, Magic)
	buf.WriteByte(Version)
	buf.WriteByte(byte(len(meta.HopChain)))
	binary.Write(buf, binary.BigEndian, uint16(0))

	id, err := uuid.Parse(meta.ID)
	if err != nil {
		id = uuid.New()
	}
	buf.Write(id[:])

	for _, hop := range meta.HopChain {
		host, portStr, err := net.SplitHostPort(hop)
		if err != nil {
			return nil, fmt.Errorf("invalid hop address: %w", err)
		}
		port, _ := strconv.Atoi(portStr)

		entry := HopEntry{Port: uint16(port)}
		var addrBytes []byte
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() != nil {
				entry.Type = 0x01
				addrBytes = ip.To4()
			} else {
				entry.Type = 0x02
				addrBytes = ip.To16()
			}
		} else {
			entry.Type = 0x03
			addrBytes = []byte(host)
		}

		addrLen := len(addrBytes)
		if addrLen > 30 {
			addrLen = 30 // 防止切片溢出
		}
		entry.AddrLen = byte(addrLen)
		copy(entry.Address[:], addrBytes[:addrLen])

		buf.WriteByte(entry.Type)
		binary.Write(buf, binary.BigEndian, entry.Port)
		buf.WriteByte(entry.AddrLen)
		buf.Write(entry.Address[:])
	}

	return buf.Bytes(), nil
}

// DecodeNextHop 解析当前数据包并提取下一跳信息及 SessionID
func DecodeNextHop(r io.Reader) (nextHop string, sessionID string, remainingHeader []byte, err error) {
	var h Header
	if err := binary.Read(r, binary.BigEndian, &h.Magic); err != nil {
		return "", "", nil, err
	}
	if h.Magic != Magic {
		return "", "", nil, errors.New("invalid magic")
	}

	binary.Read(r, binary.BigEndian, &h.Version)
	binary.Read(r, binary.BigEndian, &h.NHops)
	binary.Read(r, binary.BigEndian, &h.Reserved)
	if _, err := io.ReadFull(r, h.SessionID[:]); err != nil {
		return "", "", nil, err
	}

	sid, _ := uuid.FromBytes(h.SessionID[:])
	sessionID = sid.String()

	if h.NHops == 0 {
		return "", sessionID, nil, errors.New("no more hops")
	}

	var entry HopEntry
	if err := binary.Read(r, binary.BigEndian, &entry.Type); err != nil {
		return "", sessionID, nil, err
	}
	if err := binary.Read(r, binary.BigEndian, &entry.Port); err != nil {
		return "", sessionID, nil, err
	}
	if err := binary.Read(r, binary.BigEndian, &entry.AddrLen); err != nil {
		return "", sessionID, nil, err
	}

	if entry.AddrLen > 30 {
		return "", sessionID, nil, errors.New("malformed packet: AddrLen exceeds maximum")
	}

	if _, err := io.ReadFull(r, entry.Address[:]); err != nil {
		return "", sessionID, nil, err
	}

	var host string
	switch entry.Type {
	case 0x01:
		if entry.AddrLen < 4 {
			return "", sessionID, nil, errors.New("invalid IPv4 length")
		}
		host = net.IP(entry.Address[:4]).String()
	case 0x02:
		if entry.AddrLen < 16 {
			return "", sessionID, nil, errors.New("invalid IPv6 length")
		}
		host = net.IP(entry.Address[:16]).String()
	case 0x03:
		host = string(entry.Address[:entry.AddrLen])
	default:
		return "", sessionID, nil, fmt.Errorf("unknown address type: %d", entry.Type)
	}
	nextHop = net.JoinHostPort(host, strconv.Itoa(int(entry.Port)))

	newHeader := new(bytes.Buffer)
	binary.Write(newHeader, binary.BigEndian, Magic)
	newHeader.WriteByte(h.Version)
	newHeader.WriteByte(h.NHops - 1)
	binary.Write(newHeader, binary.BigEndian, uint16(0))
	newHeader.Write(h.SessionID[:])

	if h.NHops > 1 {
		remainingHops := make([]byte, int(h.NHops-1)*HopEntrySize)
		if _, err := io.ReadFull(r, remainingHops); err != nil {
			return nextHop, sessionID, nil, err
		}
		newHeader.Write(remainingHops)
	}

	return nextHop, sessionID, newHeader.Bytes(), nil
}
