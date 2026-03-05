//go:build linux
package inbound

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	SO_ORIGINAL_DST      = 80
	IP6T_SO_ORIGINAL_DST = 80
)

// getOriginalDst 提取 iptables REDIRECT 之前的原始目标地址
func getOriginalDst(conn net.Conn) (string, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return "", err
	}

	var addr string
	var sysErr error

	err = rawConn.Control(func(fd uintptr) {
		// 1. 尝试获取 IPv4 原始目标
		addr, sysErr = getOriginalDstIPv4(fd)
		if sysErr != nil {
			// 2. 尝试获取 IPv6 原始目标
			addr, sysErr = getOriginalDstIPv6(fd)
		}
	})

	if err != nil {
		return "", err
	}
	return addr, sysErr
}

func getOriginalDstIPv4(fd uintptr) (string, error) {
	addr, err := unix.GetsockoptIPv4Mreq(int(fd), unix.IPPROTO_IP, SO_ORIGINAL_DST)
	if err != nil {
		return "", err
	}
	
	ip := net.IPv4(addr.Multiaddr[4], addr.Multiaddr[5], addr.Multiaddr[6], addr.Multiaddr[7])
	port := uint16(addr.Multiaddr[2])<<8 + uint16(addr.Multiaddr[3])
	return net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)), nil
}

func getOriginalDstIPv6(fd uintptr) (string, error) {
	// IPv6 的处理通常涉及 raw syscall
	var addr syscall.RawSockaddrInet6
	size := uint32(unsafe.Sizeof(addr))
	
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, 
		uintptr(unix.IPPROTO_IPV6), uintptr(IP6T_SO_ORIGINAL_DST), 
		uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
	
	if errno != 0 {
		return "", errno
	}

	ip := net.IP(addr.Addr[:])
	port := uint16(addr.Port>>8) | uint16(addr.Port<<8)
	return net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)), nil
}
