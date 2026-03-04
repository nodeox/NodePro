package transport

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

type StreamHandler func(net.Conn)
type DatagramHandler func(conn *quic.Conn, data []byte)

type QUICServer struct {
	listenAddr string
	tlsConfig  *tls.Config
	quicConfig *quic.Config
	logger     *zap.Logger
}

func NewQUICServerWithTLS(listenAddr string, tlsCfg *tls.Config, logger *zap.Logger) (*QUICServer, error) {
	return &QUICServer{
		listenAddr: listenAddr,
		tlsConfig:  tlsCfg,
		quicConfig: &quic.Config{
			MaxIdleTimeout:             30000 * 1000000, // 30s
			KeepAlivePeriod:            10000 * 1000000, // 10s
			EnableDatagrams:            true,
			MaxStreamReceiveWindow:     16 * 1024 * 1024,
			MaxConnectionReceiveWindow: 32 * 1024 * 1024,
		},
		logger: logger,
	}, nil
}

func (s *QUICServer) Start(ctx context.Context, sHandler StreamHandler, dHandler DatagramHandler) error {
	listener, err := quic.ListenAddr(s.listenAddr, s.tlsConfig, s.quicConfig)
	if err != nil { return err }
	defer listener.Close()

	s.logger.Info("quic server listening", zap.String("addr", s.listenAddr))

	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			select {
			case <-ctx.Done(): return nil
			default: continue
			}
		}
		go s.handleConnection(ctx, conn, sHandler, dHandler)
	}
}

func (s *QUICServer) handleConnection(ctx context.Context, conn *quic.Conn, sHandler StreamHandler, dHandler DatagramHandler) {
	go func() {
		for {
			data, err := conn.ReceiveDatagram(ctx)
			if err != nil { return }
			if dHandler != nil { dHandler(conn, data) }
		}
	}()

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil { return }
		go sHandler(&StreamConn{Stream: stream, RawConn: conn})
	}
}
