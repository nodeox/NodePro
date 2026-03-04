package benchmark

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/nodeox/NodePro/internal/transport"
	"github.com/quic-go/quic-go"
)

// BenchmarkThroughputTCP 模拟标准 TCP 转发吞吐量
func BenchmarkThroughputTCP(b *testing.B) {
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	data := make([]byte, 32*1024)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs() // 报告内存分配

	go func() {
		for {
			if _, err := io.Copy(io.Discard, s); err != nil { return }
		}
	}()

	for i := 0; i < b.N; i++ {
		c.Write(data)
	}
}

// BenchmarkThroughputQUIC 模拟单流 QUIC 吞吐量
func BenchmarkThroughputQUIC(b *testing.B) {
	tlsCfg, _ := transport.NewServerTLSConfig("../../configs/certs/server.crt", "../../configs/certs/server.key", "../../configs/certs/ca.crt")
	tlsCfg.InsecureSkipVerify = true
	
	ln, _ := quic.ListenAddr("127.0.0.1:0", tlsCfg, &quic.Config{EnableDatagrams: true})
	addr := ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil { return }
			go func() {
				stream, _ := conn.AcceptStream(context.Background())
				io.Copy(io.Discard, stream)
			}()
		}
	}()

	tlsClientCfg, _ := transport.NewClientTLSConfig("../../configs/certs/client.crt", "../../configs/certs/client.key", "../../configs/certs/ca.crt", "localhost", true)
	tlsClientCfg.InsecureSkipVerify = true
	conn, _ := quic.DialAddr(context.Background(), addr, tlsClientCfg, &quic.Config{EnableDatagrams: true})
	stream, _ := conn.OpenStreamSync(context.Background())

	data := make([]byte, 32*1024)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		stream.Write(data)
	}
	stream.Close()
	ln.Close()
}

// BenchmarkParallelQUIC 模拟多流并发场景
func BenchmarkParallelQUIC(b *testing.B) {
	tlsCfg, _ := transport.NewServerTLSConfig("../../configs/certs/server.crt", "../../configs/certs/server.key", "../../configs/certs/ca.crt")
	tlsCfg.InsecureSkipVerify = true
	ln, _ := quic.ListenAddr("127.0.0.1:0", tlsCfg, &quic.Config{EnableDatagrams: true})
	addr := ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil { return }
			go func(c *quic.Conn) {
				for {
					stream, err := c.AcceptStream(context.Background())
					if err != nil { return }
					go io.Copy(io.Discard, stream)
				}
			}(conn)
		}
	}()

	tlsClientCfg, _ := transport.NewClientTLSConfig("../../configs/certs/client.crt", "../../configs/certs/client.key", "../../configs/certs/ca.crt", "localhost", true)
	tlsClientCfg.InsecureSkipVerify = true
	conn, _ := quic.DialAddr(context.Background(), addr, tlsClientCfg, &quic.Config{EnableDatagrams: true})

	b.ResetTimer()
	b.SetBytes(32 * 1024)
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		stream, _ := conn.OpenStreamSync(context.Background())
		data := make([]byte, 32*1024)
		for pb.Next() {
			stream.Write(data)
		}
		stream.Close()
	})
	ln.Close()
}
