package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// WSConn 包装 gorilla.Websocket 为 net.Conn
type WSConn struct {
	*websocket.Conn
	rb []byte
}

func (w *WSConn) Read(b []byte) (int, error) {
	if len(w.rb) == 0 {
		_, msg, err := w.Conn.ReadMessage()
		if err != nil { return 0, err }
		w.rb = msg
	}
	n := copy(b, w.rb)
	w.rb = w.rb[n:]
	return n, nil
}

func (w *WSConn) Write(b []byte) (int, error) {
	err := w.Conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil { return 0, err }
	return len(b), nil
}

func (w *WSConn) LocalAddr() net.Addr  { return w.Conn.LocalAddr() }
func (w *WSConn) RemoteAddr() net.Addr { return w.Conn.RemoteAddr() }
func (w *WSConn) SetDeadline(t time.Time) error {
	w.Conn.SetReadDeadline(t); w.Conn.SetWriteDeadline(t); return nil
}
func (w *WSConn) SetReadDeadline(t time.Time) error  { return w.Conn.SetReadDeadline(t) }
func (w *WSConn) SetWriteDeadline(t time.Time) error { return w.Conn.SetWriteDeadline(t) }

// GenericDialer 支持 TCP, TLS, WS, WSS
type GenericDialer struct {
	TLSConfig *tls.Config
}

func (d *GenericDialer) Dial(ctx context.Context, transport, addr, path string, sni string, headers map[string]string) (net.Conn, error) {
	tlsCfg := d.TLSConfig
	if sni != "" {
		tlsCfg = d.TLSConfig.Clone()
		tlsCfg.ServerName = sni
	}

	switch transport {
	case "tcp":
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", addr)

	case "tls":
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil { return nil, err }
		return tls.Client(conn, tlsCfg), nil

	case "ws", "wss":
		scheme := "ws"
		if transport == "wss" { scheme = "wss" }
		if path == "" { path = "/ws" }
		u := fmt.Sprintf("%s://%s%s", scheme, addr, path)
		
		h := http.Header{}
		for k, v := range headers { h.Add(k, v) }

		dialer := websocket.Dialer{
			TLSClientConfig:  tlsCfg,
			HandshakeTimeout: 10 * time.Second,
		}
		ws, _, err := dialer.Dial(u, h)
		if err != nil { return nil, err }
		return &WSConn{Conn: ws}, nil

	default:
		return nil, fmt.Errorf("unsupported transport: %s", transport)
	}
}
