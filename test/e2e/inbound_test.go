package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/nodeox/NodePro/internal/agent"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboundProtocols(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. 启动 Mock 目标服务器 (HTTP)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "success")
	}))
	defer ts.Close()

	// 1.1 启动 Mock UDP Echo 服务器
	udpSrv, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := udpSrv.ReadFromUDP(buf)
			if err != nil { return }
			udpSrv.WriteToUDP(buf[:n], addr)
		}
	}()
	defer udpSrv.Close()
	udpTargetAddr := udpSrv.LocalAddr().String()

	targetAddr := ts.Listener.Addr().String()
	targetHost, targetPortStr, _ := net.SplitHostPort(targetAddr)
	targetPort, _ := strconv.Atoi(targetPortStr)

	// 2. 配置 Agent
	cfg := &common.Config{
		Node: common.NodeConfig{ID: "inbound-test", Type: "ingress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "http", Listen: "127.0.0.1:18080"},
			{Protocol: "socks5", Listen: "127.0.0.1:18081", Auth: common.AuthConfig{Enabled: false}},
			{Protocol: "tcp-forward", Listen: "127.0.0.1:18082", Settings: map[string]interface{}{"target": targetAddr}},
			{Protocol: "tcp-balance", Listen: "127.0.0.1:18083", Settings: map[string]interface{}{"targets": []interface{}{targetAddr}}},
			{Protocol: "udp-forward", Listen: "127.0.0.1:18084", Settings: map[string]interface{}{"target": udpTargetAddr}},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct", Protocol: "direct", Group: "default"},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRuleConfig{{Type: "default", Outbound: "direct"}},
		},
		Limits: common.LimitsConfig{PerUserBandwidthMBps: 100},
	}

	ag, err := agent.New(cfg, logger)
	require.NoError(t, err)

	go ag.Start(ctx)
	
	// 等待服务启动
	time.Sleep(1 * time.Second)

	// 3. 测试 HTTP 代理
	t.Run("HTTP_Proxy", func(t *testing.T) {
		proxyURL, _ := url.Parse("http://127.0.0.1:18080")
		httpClient := &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
			Timeout:   2 * time.Second,
		}
		resp, err := httpClient.Get(ts.URL)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "success", string(body))
		resp.Body.Close()
	})

	// 4. 测试 SOCKS5 代理 (TCP)
	t.Run("SOCKS5_TCP", func(t *testing.T) {
		conn, err := net.Dial("tcp", "127.0.0.1:18081")
		require.NoError(t, err)
		defer conn.Close()

		// 1. Greeting
		conn.Write([]byte{0x05, 0x01, 0x00})
		resp := make([]byte, 2)
		io.ReadFull(conn, resp)
		assert.Equal(t, byte(0x05), resp[0])
		assert.Equal(t, byte(0x00), resp[1])

		// 2. Connection Request
		req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(targetHost))}
		req = append(req, []byte(targetHost)...)
		req = append(req, byte(targetPort>>8), byte(targetPort&0xff))
		conn.Write(req)

		resp = make([]byte, 10)
		io.ReadFull(conn, resp)
		assert.Equal(t, byte(0x05), resp[0])
		assert.Equal(t, byte(0x00), resp[1]) // Success

		// 3. Data
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", targetHost)
		buf, _ := io.ReadAll(conn)
		assert.Contains(t, string(buf), "success")
	})

	t.Run("SOCKS5_UDP", func(t *testing.T) {
		fmt.Println("Starting SOCKS5 UDP test...")
		conn, err := net.Dial("tcp", "127.0.0.1:18081")
		require.NoError(t, err)
		defer conn.Close()

		// 1. SOCKS5 Greeting
		conn.Write([]byte{0x05, 0x01, 0x00})
		resp := make([]byte, 2)
		io.ReadFull(conn, resp)
		fmt.Println("SOCKS5 Greeting OK")

		// 2. UDP Associate
		conn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		resp = make([]byte, 10)
		io.ReadFull(conn, resp)
		assert.Equal(t, byte(0x05), resp[0])
		assert.Equal(t, byte(0x00), resp[1])
		fmt.Println("SOCKS5 UDP Associate OK")

		// 获取代理提供的 UDP 端口
		proxyUDPPort := binary.BigEndian.Uint16(resp[8:])
		proxyUDPAddr, _ := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(proxyUDPPort))))
		fmt.Printf("Proxy UDP Port: %d\n", proxyUDPPort)

		// 3. 发送 UDP 数据包
		c, err := net.ListenUDP("udp", nil)
		require.NoError(t, err)
		defer c.Close()

		// 构造 SOCKS5 UDP 包
		host, portStr, _ := net.SplitHostPort(udpTargetAddr)
		port, _ := strconv.Atoi(portStr)
		header := []byte{0x00, 0x00, 0x00, 0x03, byte(len(host))}
		header = append(header, []byte(host)...)
		header = append(header, byte(port>>8), byte(port&0xff))
		data := append(header, []byte("hello-udp")...)

		fmt.Println("Sending UDP packet...")
		c.WriteToUDP(data, proxyUDPAddr)

		// 接收回传
		buf := make([]byte, 2048)
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, raddr, err := c.ReadFromUDP(buf)
		if err != nil {
			fmt.Printf("ReadFromUDP failed: %v\n", err)
		} else {
			fmt.Printf("Received %d bytes from %v\n", n, raddr)
		}
		require.NoError(t, err)
		// SOCKS5 UDP 回传包也有 Header，所以检查子串即可
		assert.Contains(t, string(buf[:n]), "hello-udp")
	})

	// 5. 测试 TCP Forward
	t.Run("TCP_Forward", func(t *testing.T) {
		conn, err := net.Dial("tcp", "127.0.0.1:18082")
		require.NoError(t, err)
		defer conn.Close()

		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		buf, _ := io.ReadAll(conn)
		assert.Contains(t, string(buf), "success")
	})

	// 6. 测试 TCP Balance
	t.Run("TCP_Balance", func(t *testing.T) {
		conn, err := net.Dial("tcp", "127.0.0.1:18083")
		require.NoError(t, err)
		defer conn.Close()

		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		buf, _ := io.ReadAll(conn)
		assert.Contains(t, string(buf), "success")
	})

	// 7. 测试 UDP Forward
	t.Run("UDP_Forward", func(t *testing.T) {
		conn, err := net.Dial("udp", "127.0.0.1:18084")
		require.NoError(t, err)
		defer conn.Close()

		conn.Write([]byte("ping-udp"))
		buf := make([]byte, 1024)
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := conn.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "ping-udp", string(buf[:n]))
	})

	ag.Stop()
}
