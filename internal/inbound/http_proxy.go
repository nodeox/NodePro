package inbound

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// HTTPProxyHandler HTTP 代理处理器
type HTTPProxyHandler struct {
	listenAddr     string
	useProxyProto  bool
	auth           common.AuthConfig
	limiterManager *common.LimiterManager
	quotaManager   *common.QuotaManager
	listener       net.Listener
	logger         *zap.Logger
}

func NewHTTPProxyHandler(listen string, useProxyProto bool, auth common.AuthConfig, lm *common.LimiterManager, qm *common.QuotaManager, logger *zap.Logger) *HTTPProxyHandler {
	return &HTTPProxyHandler{
		listenAddr:     listen,
		useProxyProto:  useProxyProto,
		auth:           auth,
		limiterManager: lm,
		quotaManager:   qm,
		logger:         logger,
	}
}

func (h *HTTPProxyHandler) Start(ctx context.Context, router common.Router) error {
	var err error
	h.listener, err = common.Listen("tcp", h.listenAddr, h.useProxyProto)
	if err != nil {
		return err
	}

	h.logger.Info("HTTP Proxy listening", zap.String("addr", h.listenAddr), zap.Bool("auth", h.auth.Enabled), zap.Bool("proxy_protocol", h.useProxyProto))

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.handleHTTP(w, r, router)
		}),
	}

	go func() {
		<-ctx.Done()
		server.Close()
	}()

	return server.Serve(h.listener)
}

func (h *HTTPProxyHandler) handleHTTP(w http.ResponseWriter, r *http.Request, router common.Router) {
	// 1. 身份验证
	userID := "anonymous"
	if h.auth.Enabled {
		authHeader := r.Header.Get("Proxy-Authorization")
		if authHeader == "" {
			w.Header().Set("Proxy-Authenticate", `Basic realm="NodePro Proxy"`)
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Basic" {
			http.Error(w, "Invalid Proxy-Authorization Header", http.StatusBadRequest)
			return
		}

		payload, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			http.Error(w, "Failed to decode Proxy-Authorization", http.StatusBadRequest)
			return
		}

		pair := strings.SplitN(string(payload), ":", 2)
		if len(pair) != 2 {
			http.Error(w, "Invalid Proxy-Authorization credentials", http.StatusBadRequest)
			return
		}

		username, password := pair[0], pair[1]
		authenticated := false
		for _, user := range h.auth.Users {
			if user.Username == username {
				if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err == nil {
					authenticated = true
					break
				}
			}
		}

		if !authenticated {
			h.logger.Warn("Auth failed", zap.String("user", username), zap.String("remote", r.RemoteAddr))
			http.Error(w, "Forbidden: Invalid credentials", http.StatusForbidden)
			return
		}
		userID = username
	}

	limiter := h.limiterManager.GetOrCreate(userID)

	// 2. 处理 CONNECT 方法 (HTTPS 隧道)
	if r.Method == http.MethodConnect {
		h.handleConnect(w, r, router, userID, limiter)
		return
	}

	// 3. 处理普通的 HTTP 代理请求 (GET/POST...)
	h.handlePlainHTTP(w, r, router, userID, limiter)
}

func (h *HTTPProxyHandler) handleConnect(w http.ResponseWriter, r *http.Request, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	sessionID := uuid.New().String()
	target := r.URL.Host
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, "443")
	}

	meta := common.SessionMeta{
		ID:        sessionID,
		Source:    clientConn.RemoteAddr(),
		Target:    target,
		Network:   "tcp",
		HopChain:  []string{target},
		CreatedAt: time.Now(),
		UserID:    userID,
	}

	outbound, err := router.Route(meta)
	if err != nil {
		h.logger.Error("no route for http connect", zap.String("target", target), zap.Error(err))
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil {
		h.logger.Error("failed to dial for http connect", zap.String("target", target), zap.Error(err))
		clientConn.Write([]byte("HTTP/1.1 504 Gateway Timeout\r\n\r\n"))
		return
	}
	defer targetConn.Close()

	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// 尝试嗅探域名 (SNI)
	sniffedConn, result := common.SniffConn(clientConn)
	clientConn = sniffedConn
	if result != nil && result.Domain != "" {
		h.logger.Debug("Sniffed domain from HTTP CONNECT", zap.String("domain", result.Domain))
	}

	common.DualRelay(ctx, clientConn, targetConn, userID, limiter, h.quotaManager)
}

func (h *HTTPProxyHandler) handlePlainHTTP(w http.ResponseWriter, r *http.Request, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	target := r.Host
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, "80")
	}

	sessionID := uuid.New().String()
	meta := common.SessionMeta{
		ID:        sessionID,
		Target:    target,
		Network:   "tcp",
		HopChain:  []string{target},
		CreatedAt: time.Now(),
		UserID:    userID,
	}

	outbound, err := router.Route(meta)
	if err != nil {
		h.logger.Error("no route for plain http", zap.String("target", target), zap.Error(err))
		http.Error(w, "Gateway Error", http.StatusBadGateway)
		return
	}

	ctx := context.WithValue(r.Context(), "session_id", sessionID)
	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil {
		h.logger.Error("failed to dial for plain http", zap.String("target", target), zap.Error(err))
		http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
		return
	}
	defer targetConn.Close()

	// 清理 Hop-by-hop 头
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")

	// 如果能 Hijack，直接交给 DualRelay 处理，性能最优
	hijacker, ok := w.(http.Hijacker)
	if ok {
		clientConn, _, err := hijacker.Hijack()
		if err == nil {
			defer clientConn.Close()
			// 先手动写出请求
			if err := r.Write(targetConn); err != nil {
				return
			}
			common.DualRelay(ctx, clientConn, targetConn, userID, limiter, h.quotaManager)
			return
		}
	}
	
	// 降级：手动转发响应 (用于不支持 Hijack 的环境)
	if err := r.Write(targetConn); err != nil {
		h.logger.Error("failed to write request to target", zap.Error(err))
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(targetConn), r)
	if err != nil {
		h.logger.Error("failed to read response from target", zap.Error(err))
		http.Error(w, "Remote Service Unreachable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		for _, vv := range v { w.Header().Add(k, vv) }
	}
	w.WriteHeader(resp.StatusCode)
	// TODO: 这种降级模式目前没有应用 DualRelay 的限流，仅作为保底
}

func (h *HTTPProxyHandler) Stop() error {
	if h.listener != nil {
		return h.listener.Close()
	}
	return nil
}

func (h *HTTPProxyHandler) Addr() net.Addr { return h.listener.Addr() }
