package inbound

import (
	"context"
	"net"

	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/protocol/npchain"
	"go.uber.org/zap"
)

// NPChainInboundHandler 中继/出口入站处理器
type NPChainInboundHandler struct {
	obfs   common.ObfsConfig
	logger *zap.Logger
}

func NewNPChainInboundHandler(obfs common.ObfsConfig, logger *zap.Logger) *NPChainInboundHandler {
	return &NPChainInboundHandler{
		obfs:   obfs,
		logger: logger,
	}
}

func (n *NPChainInboundHandler) Start(ctx context.Context, router common.Router) error {
	return nil
}

func (n *NPChainInboundHandler) Stop() error {
	return nil
}

func (n *NPChainInboundHandler) Addr() net.Addr {
	return nil
}

func (n *NPChainInboundHandler) HandleRelay(conn net.Conn, router common.Router) {
	defer conn.Close()

	// 如果开启了混淆，包装连接
	if n.obfs.Type == "padding" || n.obfs.Interval > 0 {
		conn = common.NewObfsConn(conn, n.obfs)
	}

	nextHop, sessionID, network, remainingHeader, err := npchain.DecodeNextHop(conn)
	if err != nil {
		n.logger.Error("failed to decode np-chain header", zap.Error(err))
		return
	}

	nHopsLeft := remainingHeader[5]
	ctx := context.WithValue(context.Background(), "session_id", sessionID)

	if nHopsLeft == 0 {
		n.logger.Debug("acting as egress", zap.String("target", nextHop), zap.String("network", network), zap.String("session_id", sessionID))
		dialer := &net.Dialer{Timeout: common.DialTimeout}
		if network == "" {
			network = "tcp"
		}
		targetConn, err := dialer.DialContext(ctx, network, nextHop)
		if err != nil {
			n.logger.Error("egress dial failed", zap.String("target", nextHop), zap.Error(err))
			return
		}
		defer targetConn.Close()

		common.DualRelay(ctx, conn, targetConn, "relay-system", nil, nil)
	} else {
		n.logger.Debug("acting as relay", zap.String("next_hop", nextHop), zap.String("network", network), zap.String("session_id", sessionID))
		meta := common.SessionMeta{ID: sessionID, Target: nextHop, Network: network}
		outbound, err := router.Route(meta)
		if err != nil {
			n.logger.Error("relay route failed", zap.String("next", nextHop), zap.Error(err))
			return
		}
		targetConn, err := outbound.Dial(context.Background(), meta)
		if err != nil {
			n.logger.Error("relay dial failed", zap.String("next", nextHop), zap.Error(err))
			return
		}
		defer targetConn.Close()

		if _, err := targetConn.Write(remainingHeader); err != nil { return }
		common.DualRelay(ctx, conn, targetConn, "relay-system", nil, nil)
	}
}
