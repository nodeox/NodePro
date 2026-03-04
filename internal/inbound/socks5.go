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
	"github.com/nodeox/NodePro/internal/observability"
	"github.com/nodeox/NodePro/internal/protocol/socks5"
	"github.com/nodeox/NodePro/internal/transport"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// Socks5Handler 入站处理器
type Socks5Handler struct {
	listenAddr     string
	listener       net.Listener
	logger         *zap.Logger
	auth           common.AuthConfig
	limiterManager *common.LimiterManager
	quotaManager   *common.QuotaManager
}

func NewSocks5Handler(listenAddr string, auth common.AuthConfig, lm *common.LimiterManager, qm *common.QuotaManager, logger *zap.Logger) *Socks5Handler {
	return &Socks5Handler{
		listenAddr:     listenAddr,
		auth:           auth,
		limiterManager: lm,
		quotaManager:   qm,
		logger:         logger,
	}
}

func (s *Socks5Handler) Start(ctx context.Context, router common.Router) error {
	var err error
	s.listener, err = net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("socks5 listen failed: %w", err)
	}

	s.logger.Info("socks5 listening", zap.String("addr", s.listenAddr), zap.Bool("auth", s.auth.Enabled))

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

	// 设置握手超时
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// 1. SOCKS5 握手
	buf := make([]byte, 256)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}

	nMethods := int(buf[1])
	if nMethods <= 0 || nMethods > 255 {
		return // 拒绝异常的 nMethods
	}

	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	userID := "anonymous"
	if s.auth.Enabled {
		hasUserPass := false
		for _, m := range methods {
			if m == 0x02 {
				hasUserPass = true
				break
			}
		}
		if !hasUserPass {
			conn.Write([]byte{0x05, 0xFF})
			return
		}
		conn.Write([]byte{0x05, 0x02})

		// 处理认证请求
		if _, err := io.ReadFull(conn, buf[:2]); err != nil || buf[0] != 0x01 {
			return
		}
		uLen := int(buf[1])
		if _, err := io.ReadFull(conn, buf[:uLen]); err != nil {
			return
		}
		username := string(buf[:uLen])

		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		pLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:pLen]); err != nil {
			return
		}
		password := string(buf[:pLen])

		authenticated := false
		for _, user := range s.auth.Users {
			if user.Username == username {
				// 修复：使用 bcrypt 进行安全比对
				if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err == nil {
					authenticated = true
					break
				}
			}
		}

		if !authenticated {
			observability.Audit("auth_failed", false, map[string]interface{}{"user": username, "remote": conn.RemoteAddr().String()})
			conn.Write([]byte{0x01, 0x01})
			return
		}
		userID = username
		observability.Audit("auth_success", true, map[string]interface{}{"user": username})
		conn.Write([]byte{0x01, 0x00})
	} else {
		conn.Write([]byte{0x05, 0x00})
	}

	// 取消后续转发的读取超时，由 Relay 逻辑自行处理
	conn.SetReadDeadline(time.Time{})

	// 2. 处理命令
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}

	userLimiter := s.limiterManager.GetOrCreate(userID)

	switch buf[1] {
	case 0x01: // CONNECT
		s.handleConnect(conn, buf[3], router, userID, userLimiter)
	case 0x03: // UDP ASSOCIATE
		s.handleUDPAssociate(conn, router, userID, userLimiter)
	}
}

func (s *Socks5Handler) handleConnect(conn net.Conn, addrType byte, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	buf := make([]byte, 256)
	var target string
	switch addrType {
	case 0x01:
		io.ReadFull(conn, buf[:4])
		target = net.IP(buf[:4]).String()
	case 0x03:
		io.ReadFull(conn, buf[:1])
		len := int(buf[0])
		io.ReadFull(conn, buf[:len])
		target = string(buf[:len])
	case 0x04:
		io.ReadFull(conn, buf[:16])
		target = net.IP(buf[:16]).String()
	}
	io.ReadFull(conn, buf[:2])
	port := binary.BigEndian.Uint16(buf[:2])
	target = net.JoinHostPort(target, strconv.Itoa(int(port)))

	sessionID := uuid.New().String()
	meta := common.SessionMeta{
		ID: sessionID, Source: conn.RemoteAddr(), Target: target, HopChain: []string{target}, CreatedAt: time.Now(),
	}

	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	outbound, err := router.Route(meta)
	if err != nil {
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil {
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer targetConn.Close()

	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	common.DualRelay(ctx, conn, targetConn, userID, limiter, s.quotaManager)
}

func (s *Socks5Handler) handleUDPAssociate(conn net.Conn, router common.Router, userID string, limiter *common.BandwidthLimiter) {
	uconn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		s.logger.Error("failed to listen udp", zap.Error(err))
		return
	}
	defer uconn.Close()

	laddr := uconn.LocalAddr().(*net.UDPAddr)
	observability.Audit("udp_associate", true, map[string]interface{}{"local_port": laddr.Port, "client": conn.RemoteAddr().String()})

	reply := []byte{0x05, 0x00, 0x00, 0x01}
	reply = append(reply, laddr.IP.To4()...)
	reply = binary.BigEndian.AppendUint16(reply, uint16(laddr.Port))
	conn.Write(reply)

	go func() {
		buf := common.GetBuf()
		defer common.PutBuf(buf)
		for {
			n, _, err := uconn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p, err := socks5.ParseUDPPacket(buf[:n])
			if err != nil {
				continue
			}

			meta := common.SessionMeta{ID: uuid.New().String(), Target: p.Address, HopChain: []string{p.Address}}
			if out, err := router.Route(meta); err == nil {
				if outConn, err := out.Dial(context.Background(), meta); err == nil {
					if qc, ok := outConn.(*transport.StreamConn); ok {
						if limiter != nil {
							limiter.Wait(context.Background(), len(p.Data))
						}
						qc.SendUDP(p.Data)
						common.BytesTransferred.WithLabelValues("udp", "ingress").Add(float64(len(p.Data)))
					}
					outConn.Close()
				}
			}
		}
	}()

	io.Copy(io.Discard, conn)
	observability.Audit("udp_session_end", true, map[string]interface{}{"client": conn.RemoteAddr().String()})
}
