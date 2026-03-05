package npchain

import (
	"bytes"
	"crypto/rand"
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

const (
	FlagTCP = byte(0x00)
	FlagUDP = byte(0x01)
	FlagPad = byte(0x80) // 新增：填充标志位
)

// Header NP-Chain 协议头部
type Header struct {
	Magic     uint32
	Version   byte
	NHops     byte
	Flags     byte     // 协议标志位
	PadLen    byte     // 使用 Reserved 作为 PadLen
	SessionID [16]byte
}

// HopEntry 跳数条目
type HopEntry struct {
	Type    byte // 0x01: IPv4, 0x02: IPv6, 0x03: Domain
	Port    uint16
	AddrLen byte
	Address [30]byte
}

// EncodeHeader 编码协议头部和跳数列表，支持随机填充混淆
func EncodeHeader(meta common.SessionMeta) ([]byte, error) {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.BigEndian, Magic)
	buf.WriteByte(Version)
	buf.WriteByte(byte(len(meta.HopChain)))
	
	// 设置标志位
	flags := FlagTCP
	if meta.Network == "udp" {
		flags = FlagUDP
	}
	
	// 如果需要填充（此处简单判断，后期可根据 meta 动态决定）
	padLen := 0
	if meta.ID != "" { // 临时逻辑：如果 meta 正常则尝试随机填充 0-64 字节
		flags |= FlagPad
		p := make([]byte, 1)
		rand.Read(p)
		padLen = int(p[0] % 64) 
	}

	buf.WriteByte(flags)
	buf.WriteByte(byte(padLen))

	id, err := uuid.Parse(meta.ID)
	if err != nil {
		id = uuid.New()
	}
	buf.Write(id[:])

	// 写入跳数信息
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
			addrLen = 30
		}
		entry.AddrLen = byte(addrLen)
		copy(entry.Address[:], addrBytes[:addrLen])

		buf.WriteByte(entry.Type)
		binary.Write(buf, binary.BigEndian, entry.Port)
		buf.WriteByte(entry.AddrLen)
		buf.Write(entry.Address[:])
	}

	// 写入随机填充数据
	if padLen > 0 {
		padding := make([]byte, padLen)
		rand.Read(padding)
		buf.Write(padding)
	}

	return buf.Bytes(), nil
}

// DecodeNextHop 解析当前数据包并提取下一跳信息及 SessionID，并自动剥离填充
func DecodeNextHop(r io.Reader) (nextHop string, sessionID string, network string, remainingHeader []byte, err error) {
	var h Header
	if err := binary.Read(r, binary.BigEndian, &h.Magic); err != nil {
		return "", "", "", nil, err
	}
	if h.Magic != Magic {
		return "", "", "", nil, errors.New("invalid magic")
	}

	binary.Read(r, binary.BigEndian, &h.Version)
	binary.Read(r, binary.BigEndian, &h.NHops)
	binary.Read(r, binary.BigEndian, &h.Flags)
	binary.Read(r, binary.BigEndian, &h.PadLen)
	if _, err := io.ReadFull(r, h.SessionID[:]); err != nil {
		return "", "", "", nil, err
	}

	sid, _ := uuid.FromBytes(h.SessionID[:])
	sessionID = sid.String()
	
	network = "tcp"
	if h.Flags&FlagUDP != 0 {
		network = "udp"
	}

	if h.NHops == 0 {
		return "", sessionID, network, nil, errors.New("no more hops")
	}

	var entry HopEntry
	if err := binary.Read(r, binary.BigEndian, &entry.Type); err != nil {
		return "", sessionID, network, nil, err
	}
	if err := binary.Read(r, binary.BigEndian, &entry.Port); err != nil {
		return "", sessionID, network, nil, err
	}
	if err := binary.Read(r, binary.BigEndian, &entry.AddrLen); err != nil {
		return "", sessionID, network, nil, err
	}

	if entry.AddrLen > 30 {
		return "", sessionID, network, nil, errors.New("malformed packet: AddrLen exceeds maximum")
	}

	if _, err := io.ReadFull(r, entry.Address[:]); err != nil {
		return "", sessionID, network, nil, err
	}

	var host string
	switch entry.Type {
	case 0x01:
		host = net.IP(entry.Address[:4]).String()
	case 0x02:
		host = net.IP(entry.Address[:16]).String()
	case 0x03:
		host = string(entry.Address[:entry.AddrLen])
	default:
		return "", sessionID, network, nil, fmt.Errorf("unknown address type: %d", entry.Type)
	}
	nextHop = net.JoinHostPort(host, strconv.Itoa(int(entry.Port)))

	// 如果有填充，读取并丢弃
	if h.Flags&FlagPad != 0 && h.PadLen > 0 {
		padding := make([]byte, int(h.PadLen))
		if _, err := io.ReadFull(r, padding); err != nil {
			return nextHop, sessionID, network, nil, err
		}
	}

	newHeader := new(bytes.Buffer)
	binary.Write(newHeader, binary.BigEndian, Magic)
	newHeader.WriteByte(h.Version)
	newHeader.WriteByte(h.NHops - 1)
	newHeader.WriteByte(h.Flags)
	newHeader.WriteByte(h.PadLen)
	newHeader.Write(h.SessionID[:])

	if h.NHops > 1 {
		remainingHops := make([]byte, int(h.NHops-1)*HopEntrySize)
		if _, err := io.ReadFull(r, remainingHops); err != nil {
			return nextHop, sessionID, network, nil, err
		}
		newHeader.Write(remainingHops)
		
		// 如果下一跳也需要填充，我们已经在 EncodeHeader 中处理了
		// 但注意：每一级节点都会根据自己的 flags 解析并剥离它那一级的填充
	}

	return nextHop, sessionID, network, newHeader.Bytes(), nil
}
