package inbound

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

type targetStatus struct {
	outbound common.OutboundHandler
	score    float64
	updated  time.Time
}

// TCPBalanceHandler 支持健康检查和负载均衡的 TCP 转发器
type TCPBalanceHandler struct {
	listenAddr    string
	useProxyProto bool
	targets       []string
	counter       atomic.Uint64
	listener      net.Listener
	logger        *zap.Logger
	
	mu             sync.RWMutex
	targetStatuses map[string]*targetStatus
}

func NewTCPBalanceHandler(listen string, useProxyProto bool, targets []string, logger *zap.Logger) *TCPBalanceHandler {
	return &TCPBalanceHandler{
		listenAddr:     listen,
		useProxyProto:  useProxyProto,
		targets:        targets,
		logger:         logger,
		targetStatuses: make(map[string]*targetStatus),
	}
}

func (h *TCPBalanceHandler) Start(ctx context.Context, router common.Router) error {
	var err error
	h.listener, err = common.Listen("tcp", h.listenAddr, h.useProxyProto)
	if err != nil { return err }

	h.logger.Info("TCP Balance Forwarding started", zap.String("addr", h.listenAddr), zap.Bool("proxy_protocol", h.useProxyProto))

	// 启动后台健康检查任务
	go h.healthCheckLoop(ctx, router)

	for {
		conn, err := h.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done(): return nil
			default: continue
			}
		}
		go h.handle(conn, router)
	}
}

func (h *TCPBalanceHandler) healthCheckLoop(ctx context.Context, router common.Router) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	check := func() {
		for _, target := range h.targets {
			meta := common.SessionMeta{Target: target}
			out, err := router.Route(meta)
			if err != nil { continue }
			
			score := out.HealthCheck(ctx)
			h.mu.Lock()
			h.targetStatuses[target] = &targetStatus{
				outbound: out,
				score:    score,
				updated:  time.Now(),
			}
			h.mu.Unlock()
		}
	}

	check() // 初始检查
	for {
		select {
		case <-ticker.C: check()
		case <-ctx.Done(): return
		}
	}
}

func (h *TCPBalanceHandler) handle(conn net.Conn, router common.Router) {
	defer conn.Close()

	// 1. 尝试嗅探域名
	sniffedConn, result := common.SniffConn(conn)
	conn = sniffedConn

	var bestTarget string
	var bestOutbound common.OutboundHandler
	maxScore := -1.0

	h.mu.RLock()
	for t, s := range h.targetStatuses {
		if s.score > maxScore {
			maxScore = s.score
			bestTarget = t
			bestOutbound = s.outbound
		}
	}
	h.mu.RUnlock()

	// 兜底：如果得分都太低或没初始化，使用轮询
	if maxScore < 0.1 {
		idx := h.counter.Add(1) % uint64(len(h.targets))
		bestTarget = h.targets[idx]
		meta := common.SessionMeta{Target: bestTarget}
		bestOutbound, _ = router.Route(meta)
	}

	if bestOutbound == nil { return }

	target := bestTarget
	if result != nil && result.Domain != "" {
		_, port, _ := net.SplitHostPort(target)
		target = net.JoinHostPort(result.Domain, port)
	}

	sessionID := uuid.New().String()
	meta := common.SessionMeta{ID: sessionID, Source: conn.RemoteAddr(), Target: target, Network: "tcp"}
	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	
	targetConn, err := bestOutbound.Dial(ctx, meta)
	if err != nil { return }
	defer targetConn.Close()

	common.DualRelay(ctx, conn, targetConn, "anonymous", nil, nil)
}

func (h *TCPBalanceHandler) Stop() error {
	if h.listener != nil { return h.listener.Close() }
	return nil
}

func (h *TCPBalanceHandler) Addr() net.Addr { return h.listener.Addr() }
