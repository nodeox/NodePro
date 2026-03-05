//go:build !linux
package inbound

import (
	"context"
	"fmt"
	"net"

	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

type TProxyHandler struct {
	listenAddr    string
	useProxyProto bool
	logger        *zap.Logger
}

func NewTProxyHandler(listen string, useProxyProto bool, logger *zap.Logger) *TProxyHandler {
	return &TProxyHandler{listenAddr: listen, useProxyProto: useProxyProto, logger: logger}
}

func (h *TProxyHandler) Start(ctx context.Context, router common.Router) error {
	return fmt.Errorf("TPROXY is only supported on Linux")
}

func (h *TProxyHandler) Stop() error { return nil }
func (h *TProxyHandler) Addr() net.Addr { return nil }
