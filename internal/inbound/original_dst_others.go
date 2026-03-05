//go:build !linux
package inbound

import (
	"fmt"
	"net"
)

// getOriginalDst 在非 Linux 平台直接返回错误
func getOriginalDst(conn net.Conn) (string, error) {
	return "", fmt.Errorf("transparent proxy (REDIRECT) is only supported on Linux")
}
