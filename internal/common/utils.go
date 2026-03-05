package common

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("nodepass-relay")

type SessionInfo struct {
	ID         string             `json:"id"`
	UserID     string             `json:"user_id"`
	Src        string             `json:"src"`
	Dst        string             `json:"dst"`
	StartTime  time.Time          `json:"start_time"`
	BytesSent  int64              `json:"bytes_sent"`
	CancelFunc context.CancelFunc `json:"-"`
}

var (
	ActiveSessionMap sync.Map // map[sessionID]*SessionInfo
)

// Relay 定义为 7 参数以覆盖所有功能
func Relay(ctx context.Context, src, dst net.Conn, name string, userID string, limiter *BandwidthLimiter, quota *QuotaManager) error {
	sessionID, _ := ctx.Value("session_id").(string)
	
	_, span := tracer.Start(ctx, "relay."+name, trace.WithAttributes(
		attribute.String("src", MaskAddr(src.RemoteAddr().String())),
		attribute.String("dst", MaskAddr(dst.RemoteAddr().String())),
		attribute.String("user_id", userID),
	))
	defer span.End()

	var reader io.Reader = src
	if limiter != nil {
		reader = NewLimitReader(ctx, src, limiter)
	}

	buf := GetBuf()
	defer PutBuf(buf)

	var totalRead int64
	defer func() {
		if sessionID != "" && totalRead > 0 {
			if val, ok := ActiveSessionMap.Load(sessionID); ok {
				s := val.(*SessionInfo)
				s.BytesSent += totalRead
			}
		}
	}()

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if quota != nil && userID != "" {
				if exceeded := quota.AddUsage(userID, int64(n)); exceeded {
					RelayErrors.WithLabelValues("quota_exceeded").Inc()
					return errors.New("traffic quota exceeded")
				}
			}

			totalRead += int64(n)

			BytesTransferred.WithLabelValues(name, "relay").Add(float64(n))
			if name == "upstream" {
				AddBytesIn(int64(n))
			} else {
				AddBytesOut(int64(n))
			}
			
			if _, werr := dst.Write(buf[:n]); werr != nil {
				span.RecordError(werr)
				RelayErrors.WithLabelValues("write").Inc()
				return werr
			}
		}
		if err != nil {
			if err != io.EOF {
				span.RecordError(err)
				RelayErrors.WithLabelValues("read").Inc()
			}
			return err
		}
	}
}

// DualRelay 定义为 6 参数
func DualRelay(parentCtx context.Context, c1, c2 net.Conn, userID string, limiter *BandwidthLimiter, quota *QuotaManager) {
	SetupTCP(c1)
	SetupTCP(c2)
	IncActiveSessions()
	defer DecActiveSessions()

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	sessionID, _ := ctx.Value("session_id").(string)
	if sessionID != "" {
		info := &SessionInfo{
			ID:         sessionID,
			UserID:     userID,
			Src:        MaskAddr(c1.RemoteAddr().String()),
			Dst:        MaskAddr(c2.RemoteAddr().String()),
			StartTime:  time.Now(),
			CancelFunc: cancel,
		}
		ActiveSessionMap.Store(sessionID, info)
		defer ActiveSessionMap.Delete(sessionID)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- Relay(ctx, c1, c2, "upstream", userID, limiter, quota) }()
	go func() { errCh <- Relay(ctx, c2, c1, "downstream", userID, limiter, quota) }()
	<-errCh
}

// Listen 创建一个监听器，并根据配置决定是否开启 Proxy Protocol 支持
func Listen(network, address string, useProxyProto bool) (net.Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	if useProxyProto {
		return &ProxyProtoListener{Listener: ln}, nil
	}
	return ln, nil
}

// SetupTCP 优化 TCP 连接性能
func SetupTCP(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(3 * time.Minute)
	}
}

// MaskAddr 遮掩地址以保护隐私
func MaskAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil { return "***" }
	parts := strings.Split(host, ".")
	if len(parts) >= 4 {
		return parts[0] + "." + parts[1] + "." + parts[2] + ".*:" + port
	}
	if len(host) > 4 { return host[:4] + "***:" + port }
	return "***:" + port
}
