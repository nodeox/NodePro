package npchain

import (
	"errors"

	"github.com/google/uuid"
)

// PackDatagram 封装 NP-Chain 数据报 (SessionID + Payload)
func PackDatagram(sessionID string, payload []byte) ([]byte, error) {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, err
	}
	res := make([]byte, 16+len(payload))
	copy(res[:16], id[:])
	copy(res[16:], payload)
	return res, nil
}

// UnpackDatagram 解析 NP-Chain 数据报
func UnpackDatagram(data []byte) (sessionID string, payload []byte, err error) {
	if len(data) < 16 {
		return "", nil, errors.New("datagram too short")
	}
	id, err := uuid.FromBytes(data[:16])
	if err != nil {
		return "", nil, err
	}
	return id.String(), data[16:], nil
}
