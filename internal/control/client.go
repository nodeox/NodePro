package control

import (
	"context"
	"crypto/tls"
	"time"

	pb "github.com/nodeox/NodePro/api/proto"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"gopkg.in/yaml.v3"
)

// ControlClient 负责与 Controller 通信，支持热更新和远程指令
type ControlClient struct {
	cfg       *common.ControllerConfig
	logger    *zap.Logger
	conn      *grpc.ClientConn
	client    pb.ControlPlaneClient
	token     string
	nodeID    string
	nodeType  string
	ctx       context.Context
	cancel    context.CancelFunc
	
	onConfigUpdate func(*common.Config)
	onCommand      func(string)
	onPolicyUpdate func([]*pb.PolicyUpdate)
}

func NewControlClient(cfg *common.ControllerConfig, nodeID string, nodeType string, logger *zap.Logger) *ControlClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &ControlClient{cfg: cfg, nodeID: nodeID, nodeType: nodeType, logger: logger, ctx: ctx, cancel: cancel}
}

func (c *ControlClient) SetHandlers(cfgFn func(*common.Config), cmdFn func(string), polyFn func([]*pb.PolicyUpdate)) {
	c.onConfigUpdate = cfgFn
	c.onCommand = cmdFn
	c.onPolicyUpdate = polyFn
}

func (c *ControlClient) Start() error {
	var opts []grpc.DialOption
	if c.cfg.Insecure {
		opts = append(opts, grpc.WithInsecure())
	} else {
		tlsCfg := &tls.Config{InsecureSkipVerify: false} // 修复：在生产环境中应严格验证
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	}
	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time: 10 * time.Second, Timeout: 3 * time.Second, PermitWithoutStream: true,
	}))

	go func() {
		backoff := 1 * time.Second
		for {
			// 修复：每次重连前先关闭旧连接，防止泄露
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}

			conn, err := grpc.DialContext(c.ctx, c.cfg.Address, opts...)
			if err == nil {
				c.conn = conn
				c.client = pb.NewControlPlaneClient(conn)
				if rerr := c.register(); rerr == nil {
					go c.heartbeatLoop()
					return
				}
			}
			
			select {
			case <-time.After(backoff):
				backoff *= 2
				if backoff > 1*time.Minute { backoff = 1 * time.Minute }
			case <-c.ctx.Done(): return
			}
		}
	}()
	return nil
}

func (c *ControlClient) register() error {
	resp, err := c.client.Register(c.ctx, &pb.RegisterRequest{NodeId: c.nodeID, NodeType: c.nodeType})
	if err != nil { return err }
	c.token = resp.Token
	return nil
}

func (c *ControlClient) getContext() context.Context {
	return metadata.AppendToOutgoingContext(c.ctx, "authorization", "Bearer "+c.token)
}

func (c *ControlClient) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			resp, err := c.client.Heartbeat(c.getContext(), &pb.HeartbeatRequest{
				NodeId: c.nodeID, Token: c.token, Status: &pb.NodeStatus{ActiveSessions: common.GetActiveSessions()},
			})
			if err != nil { continue }
			if resp.ConfigUpdated && c.onConfigUpdate != nil { c.fetchAndApplyConfig() }
			if resp.Command != "" && c.onCommand != nil { c.onCommand(resp.Command) }
			if len(resp.Policies) > 0 && c.onPolicyUpdate != nil { c.onPolicyUpdate(resp.Policies) }
		case <-c.ctx.Done(): return
		}
	}
}

func (c *ControlClient) fetchAndApplyConfig() {
	resp, err := c.client.GetConfig(c.getContext(), &pb.GetConfigRequest{NodeId: c.nodeID, Token: c.token})
	if err != nil { return }
	var newCfg common.Config
	if err := yaml.Unmarshal(resp.ConfigData, &newCfg); err == nil { c.onConfigUpdate(&newCfg) }
}

func (c *ControlClient) Stop() error {
	c.cancel()
	if c.conn != nil { return c.conn.Close() }
	return nil
}
