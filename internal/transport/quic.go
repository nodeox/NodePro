package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

// StreamConn 包装 *quic.Stream 为 net.Conn
type StreamConn struct {
	*quic.Stream
	RawConn *quic.Conn
}

func (c *StreamConn) LocalAddr() net.Addr  { return c.RawConn.LocalAddr() }
func (c *StreamConn) RemoteAddr() net.Addr { return c.RawConn.RemoteAddr() }

func (c *StreamConn) SendUDP(p []byte) error {
	return c.RawConn.SendDatagram(p)
}

func (c *StreamConn) ReceiveUDP(ctx context.Context) ([]byte, error) {
	return c.RawConn.ReceiveDatagram(ctx)
}

type QUICDialer struct {
	tlsConfig *tls.Config
	quicConfig *quic.Config
}

func NewQUICDialer(certFile, keyFile, caFile string, serverName string, insecure bool) (*QUICDialer, error) {
	tlsCfg, err := NewClientTLSConfig(certFile, keyFile, caFile, serverName, insecure)
	if err != nil { return nil, err }

	return &QUICDialer{
		tlsConfig: tlsCfg,
		quicConfig: &quic.Config{
			MaxIdleTimeout:             30 * time.Second,
			KeepAlivePeriod:            10 * time.Second,
			EnableDatagrams:            true,
			MaxStreamReceiveWindow:     16 * 1024 * 1024,
			MaxConnectionReceiveWindow: 32 * 1024 * 1024,
		},
	}, nil
}

func (d *QUICDialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	conn, err := quic.DialAddr(ctx, addr, d.tlsConfig, d.quicConfig)
	if err != nil { return nil, err }

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "")
		return nil, err
	}

	return &StreamConn{Stream: stream, RawConn: conn}, nil
}

func NewClientTLSConfig(certFile, keyFile, caFile string, serverName string, insecure bool) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil { return nil, err }
	caCert, err := os.ReadFile(caFile)
	if err != nil { return nil, err }
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		InsecureSkipVerify: insecure,
		NextProtos:   []string{"nodepass-2.0"},
	}, nil
}

func NewServerTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil { return nil, err }
	caCert, err := os.ReadFile(caFile)
	if err != nil { return nil, err }
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		InsecureSkipVerify: false,
		NextProtos:   []string{"nodepass-2.0"},
	}, nil
}
