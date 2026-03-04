package transport

import (
	"context"
	"crypto/tls"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

type QUICPool struct {
	tlsConfig  *tls.Config
	quicConfig *quic.Config
	
	mu    sync.Mutex
	conns map[string]*quic.Conn
	
	ctx    context.Context
	cancel context.CancelFunc
}

func NewQUICPool(tlsCfg *tls.Config, quicCfg *quic.Config) *QUICPool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &QUICPool{
		tlsConfig:  tlsCfg,
		quicConfig: quicCfg,
		conns:      make(map[string]*quic.Conn),
		ctx:        ctx,
		cancel:     cancel,
	}
	go p.cleanupLoop()
	return p
}

func (p *QUICPool) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			for addr, conn := range p.conns {
				select {
				case <-conn.Context().Done():
					delete(p.conns, addr)
				default:
				}
			}
			p.mu.Unlock()
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *QUICPool) Get(ctx context.Context, addr string) (*quic.Conn, error) {
	p.mu.Lock()
	conn, ok := p.conns[addr]
	p.mu.Unlock()

	if ok {
		return conn, nil
	}

	newConn, err := quic.DialAddr(ctx, addr, p.tlsConfig, p.quicConfig)
	if err != nil { return nil, err }

	p.mu.Lock()
	p.conns[addr] = newConn
	p.mu.Unlock()
	return newConn, nil
}

func (p *QUICPool) Close() error {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.conns {
		conn.CloseWithError(0, "")
	}
	p.conns = nil
	return nil
}
