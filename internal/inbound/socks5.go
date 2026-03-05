package inbound

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/protocol/socks5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// Socks5Handler 入站处理器
type Socks5Handler struct {
	listenAddr     string
	useProxyProto  bool
	listener       net.Listener
	logger         *zap.Logger
	auth           common.AuthConfig
	limiterManager *common.LimiterManager
	quotaManager   *common.QuotaManager
	sm             *UDPSessionManager
}

func NewSocks5Handler(listenAddr string, useProxyProto bool, auth common.AuthConfig, lm *common.LimiterManager, qm *common.QuotaManager, logger *zap.Logger) *Socks5Handler {
	return &Socks5Handler{
		listenAddr:     listenAddr,
		useProxyProto:  useProxyProto,
		auth:           auth,
		limiterManager: lm,
		quotaManager:   qm,
		logger:         logger,
		sm:             NewUDPSessionManager(0),
	}
}

func (s *Socks5Handler) Start(ctx context.Context, router common.Router) error {
	var err error
	s.listener, err = common.Listen("tcp", s.listenAddr, s.useProxyProto)
	if err != nil {
		return fmt.Errorf("socks5 listen failed: %w", err)
	}

	s.logger.Info("socks5 listening", zap.String("addr", s.listenAddr), zap.Bool("auth", s.auth.Enabled), zap.Bool("proxy_protocol", s.useProxyProto))

	go s.sm.CleanupLoop(ctx)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				s.logger.Error("accept error", zap.Error(err))
				continue
			}
		}
		go s.handleConnection(conn, router)
	}
}

func (s *Socks5Handler) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Socks5Handler) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

func (s *Socks5Handler) handleConnection(conn net.Conn, router common.Router) {
	defer conn.Close()

	buf := make([]byte, 256)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil || buf[0] != 0x05 { return }

	nMethods := int(buf[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil { return }

	userID := "anonymous"
	if s.auth.Enabled {
		conn.Write([]byte{0x05, 0x02})
		if _, err := io.ReadFull(conn, buf[:2]); err != nil || buf[0] != 0x01 { return }
		uLen := int(buf[1])
		io.ReadFull(conn, buf[:uLen]); username := string(buf[:uLen])
		io.ReadFull(conn, buf[:1])
		pLen := int(buf[0])
		io.ReadFull(conn, buf[:pLen]); password := string(buf[:pLen])

		authenticated := false
		for _, user := range s.auth.Users {
			if user.Username == username {
				if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err == nil {
					authenticated = true
					break
				}
			}
		}
		if !authenticated {
			conn.Write([]byte{0x01, 0x01})
			return
		}
		userID = username
		conn.Write([]byte{0x01, 0x00})
	} else {
		conn.Write([]byte{0x05, 0x00})
	}

	if _, err := io.ReadFull(conn, buf[:4]); err != nil { return }
	userLimiter := s.limiterManager.GetOrCreate(userID)

	switch buf[1] {
	case 0x01: // CONNECT
		s.handleConnect(conn, buf[3], router, userID, userLimiter)
	case 0x03: // UDP ASSOCIATE
		s.handleUDPAssociate(conn, router, userID, userLimiter)
	default:
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	}
}

func (s *Socks5Handler) handleConnect(conn net.Conn, addrType byte, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	buf := make([]byte, 256)
	var targetHost string
	switch addrType {
	case 0x01:
		io.ReadFull(conn, buf[:4]); targetHost = net.IP(buf[:4]).String()
	case 0x03:
		io.ReadFull(conn, buf[:1]); l := int(buf[0]); io.ReadFull(conn, buf[:l]); targetHost = string(buf[:l])
	case 0x04:
		io.ReadFull(conn, buf[:16]); targetHost = net.IP(buf[:16]).String()
	}
	io.ReadFull(conn, buf[:2])
	port := binary.BigEndian.Uint16(buf[:2])
	target := net.JoinHostPort(targetHost, strconv.Itoa(int(port)))

	sessionID := uuid.New().String()
	meta := common.SessionMeta{ID: sessionID, Source: conn.RemoteAddr(), Target: target, HopChain: []string{target}, UserID: userID, Network: "tcp", CreatedAt: time.Now()}
	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	
	outbound, err := router.Route(meta)
	if err != nil {
		s.logger.Error("no route for socks5 connect", zap.String("target", target), zap.Error(err))
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil {
		s.logger.Error("failed to dial for socks5 connect", zap.String("target", target), zap.Error(err))
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer targetConn.Close()

	// 成功响应
	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	// 尝试嗅探域名 (仅当客户端之前提供的是 IP 时)
	if addrType != 0x03 {
		sniffedConn, result := common.SniffConn(conn)
		conn = sniffedConn
		if result != nil && result.Domain != "" {
			s.logger.Debug("Sniffed domain from SOCKS5 established connection", zap.String("domain", result.Domain))
		}
	}

	common.DualRelay(ctx, conn, targetConn, userID, limiter, s.quotaManager)
}

func (s *Socks5Handler) handleUDPAssociate(conn net.Conn, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	uconn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0})
	if err != nil { return }
	defer uconn.Close()

	laddr := uconn.LocalAddr().(*net.UDPAddr)
	// VER(1) REP(1) RSV(1) ATYP(1) BND.ADDR(4) BND.PORT(2)
	reply := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(reply[8:], uint16(laddr.Port))
	conn.Write(reply)

	go func() {
		buf := common.GetBuf()
		defer common.PutBuf(buf)
		for {
			n, raddr, err := uconn.ReadFromUDP(buf)
			if err != nil { return }
			p, err := socks5.ParseUDPPacket(buf[:n])
			if err != nil { continue }

			srcKey := raddr.String() + "-" + p.Address
			sess, _, err := s.sm.GetOrCreate(srcKey, p.Address, raddr, uconn, true, router)
			if err != nil { continue }

			go s.sm.ForwardPacket(context.Background(), sess, append([]byte{}, p.Data...), p.Address, userID, limiter, s.quotaManager)
		}
	}()

	io.Copy(io.Discard, conn)
}
