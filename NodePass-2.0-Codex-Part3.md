# NodePass 2.0 Codex 提示词 - 第3部分：传输层与多路复用

## 目标
实现 QUIC/TCP 传输池、多路复用和带宽聚合功能。

---

## 传输层架构

```
Transport Pool
├── QUIC Pool        # QUIC 连接池
├── TCP Pool         # TCP 连接池
├── Connection Mux   # 连接复用
└── Aggregator       # 带宽聚合
```

---

## 1. QUIC 传输实现

**internal/transport/quic.go**

```go
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

// QUICPool QUIC 连接池
type QUICPool struct {
	address   string
	tlsConfig *tls.Config
	quicConfig *quic.Config
	logger    *zap.Logger

	mu    sync.RWMutex
	conns []quic.Connection
	
	maxConns int
	idleTimeout time.Duration
}

// NewQUICPool 创建 QUIC 连接池
func NewQUICPool(address string, tlsConfig *tls.Config, logger *zap.Logger) *QUICPool {
	return &QUICPool{
		address:   address,
		tlsConfig: tlsConfig,
		logger:    logger,
		conns:     make([]quic.Connection, 0),
		maxConns:  10,
		idleTimeout: 30 * time.Second,
		quicConfig: &quic.Config{
			MaxIdleTimeout:  30 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
			EnableDatagrams: true,
		},
	}
}

// Get 获取或创建连接
func (p *QUICPool) Get(ctx context.Context) (quic.Connection, error) {
	// 尝试复用现有连接
	p.mu.RLock()
	for _, conn := range p.conns {
		if conn.Context().Err() == nil {
			p.mu.RUnlock()
			return conn, nil
		}
	}
	p.mu.RUnlock()

	// 创建新连接
	return p.dial(ctx)
}

// dial 拨号建立 QUIC 连接
func (p *QUICPool) dial(ctx context.Context) (quic.Connection, error) {
	p.logger.Debug("dialing QUIC connection", zap.String("address", p.address))

	conn, err := quic.DialAddr(ctx, p.address, p.tlsConfig, p.quicConfig)
	if err != nil {
		return nil, fmt.Errorf("dial QUIC: %w", err)
	}

	// 添加到池中
	p.mu.Lock()
	if len(p.conns) < p.maxConns {
		p.conns = append(p.conns, conn)
	}
	p.mu.Unlock()

	p.logger.Debug("QUIC connection established", zap.String("address", p.address))

	return conn, nil
}

// OpenStream 打开新流
func (p *QUICPool) OpenStream(ctx context.Context) (quic.Stream, error) {
	conn, err := p.Get(ctx)
	if err != nil {
		return nil, err
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return stream, nil
}

// Close 关闭连接池
func (p *QUICPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.conns {
		conn.CloseWithError(0, "pool closed")
	}

	p.conns = nil
	return nil
}

// QUICConn QUIC 流包装为 net.Conn
type QUICConn struct {
	stream quic.Stream
	conn   quic.Connection
}

// NewQUICConn 创建 QUIC 连接包装
func NewQUICConn(stream quic.Stream, conn quic.Connection) *QUICConn {
	return &QUICConn{
		stream: stream,
		conn:   conn,
	}
}

func (qc *QUICConn) Read(b []byte) (int, error) {
	return qc.stream.Read(b)
}

func (qc *QUICConn) Write(b []byte) (int, error) {
	return qc.stream.Write(b)
}

func (qc *QUICConn) Close() error {
	return qc.stream.Close()
}

func (qc *QUICConn) LocalAddr() net.Addr {
	return qc.conn.LocalAddr()
}

func (qc *QUICConn) RemoteAddr() net.Addr {
	return qc.conn.RemoteAddr()
}

func (qc *QUICConn) SetDeadline(t time.Time) error {
	return qc.stream.SetDeadline(t)
}

func (qc *QUICConn) SetReadDeadline(t time.Time) error {
	return qc.stream.SetReadDeadline(t)
}

func (qc *QUICConn) SetWriteDeadline(t time.Time) error {
	return qc.stream.SetWriteDeadline(t)
}
```

---

## 2. TCP 连接池

**internal/transport/tcp.go**

```go
package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TCPPool TCP 连接池
type TCPPool struct {
	address string
	logger  *zap.Logger

	mu    sync.RWMutex
	conns []net.Conn
	
	maxConns int
	maxIdle  int
}

// NewTCPPool 创建 TCP 连接池
func NewTCPPool(address string, logger *zap.Logger) *TCPPool {
	return &TCPPool{
		address:  address,
		logger:   logger,
		conns:    make([]net.Conn, 0),
		maxConns: 100,
		maxIdle:  10,
	}
}

// Get 获取连接
func (p *TCPPool) Get(ctx context.Context) (net.Conn, error) {
	// 尝试从池中获取
	p.mu.Lock()
	if len(p.conns) > 0 {
		conn := p.conns[len(p.conns)-1]
		p.conns = p.conns[:len(p.conns)-1]
		p.mu.Unlock()

		// 检查连接是否有效
		if err := p.checkConn(conn); err == nil {
			return conn, nil
		}
		conn.Close()
	} else {
		p.mu.Unlock()
	}

	// 创建新连接
	return p.dial(ctx)
}

// Put 归还连接
func (p *TCPPool) Put(conn net.Conn) {
	if conn == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.conns) < p.maxIdle {
		p.conns = append(p.conns, conn)
	} else {
		conn.Close()
	}
}

// dial 拨号
func (p *TCPPool) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, fmt.Errorf("dial TCP: %w", err)
	}

	return conn, nil
}

// checkConn 检查连接是否有效
func (p *TCPPool) checkConn(conn net.Conn) error {
	// 设置短超时
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	defer conn.SetReadDeadline(time.Time{})

	// 尝试读取一个字节（不阻塞）
	one := make([]byte, 1)
	_, err := conn.Read(one)
	
	if err == nil {
		// 不应该读到数据
		return fmt.Errorf("unexpected data")
	}

	// 超时是正常的
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return nil
	}

	return err
}

// Close 关闭连接池
func (p *TCPPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.conns {
		conn.Close()
	}

	p.conns = nil
	return nil
}
```

---

## 3. 带宽聚合实现

**internal/mux/aggregator.go**

```go
package mux

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Aggregator 带宽聚合器
type Aggregator struct {
	transports []net.Conn
	logger     *zap.Logger
	
	nextConn atomic.Int32
	chunkSize int
}

// NewAggregator 创建聚合器
func NewAggregator(transports []net.Conn, logger *zap.Logger) *Aggregator {
	return &Aggregator{
		transports: transports,
		logger:     logger,
		chunkSize:  64 * 1024, // 64KB
	}
}

// Write 写入数据（分片并聚合）
func (a *Aggregator) Write(sessionID uuid.UUID, data []byte) error {
	seq := uint32(0)

	for len(data) > 0 {
		size := min(len(data), a.chunkSize)
		chunk := data[:size]
		data = data[size:]

		// 构造分片
		buf := new(bytes.Buffer)
		
		// Session ID (16 bytes)
		buf.Write(sessionID[:])
		
		// Sequence Number (4 bytes)
		binary.Write(buf, binary.BigEndian, seq)
		
		// Chunk Size (2 bytes)
		binary.Write(buf, binary.BigEndian, uint16(size))
		
		// Flags (1 byte)
		flags := byte(0)
		if len(data) == 0 {
			flags |= 0x01 // FIN
		}
		buf.WriteByte(flags)
		
		// Reserved (1 byte)
		buf.WriteByte(0)
		
		// Data
		buf.Write(chunk)

		// 轮询选择物理连接
		connIdx := a.nextConn.Add(1) % int32(len(a.transports))
		conn := a.transports[connIdx]

		if _, err := conn.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("write to transport %d: %w", connIdx, err)
		}

		seq++
	}

	return nil
}

// Close 关闭所有传输
func (a *Aggregator) Close() error {
	for _, conn := range a.transports {
		conn.Close()
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

---

## 4. 重组器实现

**internal/mux/reassembler.go**

```go
package mux

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Reassembler 重组器
type Reassembler struct {
	sessions sync.Map // map[uuid.UUID]*SessionBuffer
	logger   *zap.Logger
}

// SessionBuffer 会话缓冲区
type SessionBuffer struct {
	chunks    map[uint32][]byte
	nextSeq   uint32
	output    chan []byte
	flowCtrl  *WindowFlowController
	
	mu sync.Mutex
}

// NewReassembler 创建重组器
func NewReassembler(logger *zap.Logger) *Reassembler {
	return &Reassembler{
		logger: logger,
	}
}

// Process 处理接收到的分片
func (r *Reassembler) Process(data []byte) error {
	buf := bytes.NewReader(data)

	// 解析 Header
	sessionID := make([]byte, 16)
	if _, err := io.ReadFull(buf, sessionID); err != nil {
		return fmt.Errorf("read session ID: %w", err)
	}

	var seq uint32
	if err := binary.Read(buf, binary.BigEndian, &seq); err != nil {
		return fmt.Errorf("read sequence: %w", err)
	}

	var chunkSize uint16
	if err := binary.Read(buf, binary.BigEndian, &chunkSize); err != nil {
		return fmt.Errorf("read chunk size: %w", err)
	}

	flags, err := buf.ReadByte()
	if err != nil {
		return fmt.Errorf("read flags: %w", err)
	}

	buf.ReadByte() // Skip Reserved

	chunk := make([]byte, chunkSize)
	if _, err := io.ReadFull(buf, chunk); err != nil {
		return fmt.Errorf("read chunk data: %w", err)
	}

	// 获取或创建 Session Buffer
	sid, _ := uuid.FromBytes(sessionID)
	val, _ := r.sessions.LoadOrStore(sid, &SessionBuffer{
		chunks:   make(map[uint32][]byte),
		output:   make(chan []byte, 100),
		flowCtrl: NewWindowFlowController(16 * 1024 * 1024), // 16MB
	})
	sb := val.(*SessionBuffer)

	// 存储分片
	sb.mu.Lock()
	sb.chunks[seq] = chunk
	sb.mu.Unlock()

	// 尝试重组
	return r.tryReassemble(sb, flags)
}

// tryReassemble 尝试重组
func (r *Reassembler) tryReassemble(sb *SessionBuffer, flags byte) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for {
		// 检查背压
		if sb.flowCtrl.ShouldPause() {
			r.logger.Debug("flow control paused")
			break
		}

		// 查找下一个序号的分片
		chunk, ok := sb.chunks[sb.nextSeq]
		if !ok {
			break // 等待缺失的分片
		}

		// 发送到输出通道
		select {
		case sb.output <- chunk:
			sb.flowCtrl.buffered.Add(int64(len(chunk)))
			delete(sb.chunks, sb.nextSeq)
			sb.nextSeq++

			// 检查 FIN
			if flags&0x01 != 0 {
				close(sb.output)
				return nil
			}

		default:
			// 输出通道满，暂停
			return nil
		}
	}

	return nil
}

// GetOutput 获取会话的输出通道
func (r *Reassembler) GetOutput(sessionID uuid.UUID) <-chan []byte {
	val, ok := r.sessions.Load(sessionID)
	if !ok {
		return nil
	}

	sb := val.(*SessionBuffer)
	return sb.output
}

// OnConsumed 通知已消费数据
func (r *Reassembler) OnConsumed(sessionID uuid.UUID, n int) {
	val, ok := r.sessions.Load(sessionID)
	if !ok {
		return
	}

	sb := val.(*SessionBuffer)
	sb.flowCtrl.OnConsumed(n)
}
```

---

## 5. 流控实现

**internal/mux/flowcontrol.go**

```go
package mux

import (
	"sync/atomic"
)

// WindowFlowController 窗口式流控
type WindowFlowController struct {
	maxWindow int64
	consumed  atomic.Int64
	buffered  atomic.Int64
}

// NewWindowFlowController 创建流控器
func NewWindowFlowController(maxWindow int64) *WindowFlowController {
	return &WindowFlowController{
		maxWindow: maxWindow,
	}
}

// ShouldPause 是否应该暂停读取
func (w *WindowFlowController) ShouldPause() bool {
	return w.buffered.Load() > w.maxWindow
}

// OnConsumed 通知已消费 n 字节
func (w *WindowFlowController) OnConsumed(n int) {
	w.consumed.Add(int64(n))
	w.buffered.Add(-int64(n))
}

// WindowSize 获取当前窗口大小
func (w *WindowFlowController) WindowSize() int {
	remaining := w.maxWindow - w.buffered.Load()
	if remaining < 0 {
		return 0
	}
	return int(remaining)
}

// Reset 重置窗口
func (w *WindowFlowController) Reset() {
	w.consumed.Store(0)
	w.buffered.Store(0)
}

// GetStats 获取统计信息
func (w *WindowFlowController) GetStats() (consumed, buffered int64) {
	return w.consumed.Load(), w.buffered.Load()
}
```

---

## 6. 使用示例

### QUIC 连接池使用

```go
// 创建 TLS 配置
tlsConfig := &tls.Config{
	InsecureSkipVerify: true, // 仅用于测试
	NextProtos:         []string{"nodepass"},
}

// 创建 QUIC 池
pool := transport.NewQUICPool("example.com:443", tlsConfig, logger)
defer pool.Close()

// 打开流
ctx := context.Background()
stream, err := pool.OpenStream(ctx)
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

// 使用流
stream.Write([]byte("hello"))
```

### 带宽聚合使用

```go
// 创建多个物理连接
var transports []net.Conn
for i := 0; i < 3; i++ {
	conn, _ := net.Dial("tcp", "example.com:443")
	transports = append(transports, conn)
}

// 创建聚合器
agg := mux.NewAggregator(transports, logger)
defer agg.Close()

// 写入数据（自动分片并聚合）
sessionID := uuid.New()
data := make([]byte, 1024*1024) // 1MB
agg.Write(sessionID, data)
```

### 重组器使用

```go
// 创建重组器
reassembler := mux.NewReassembler(logger)

// 接收端处理分片
go func() {
	for {
		buf := make([]byte, 65536)
		n, _ := conn.Read(buf)
		reassembler.Process(buf[:n])
	}
}()

// 读取重组后的数据
sessionID := uuid.New()
output := reassembler.GetOutput(sessionID)

for chunk := range output {
	// 处理数据
	process(chunk)
	
	// 通知已消费
	reassembler.OnConsumed(sessionID, len(chunk))
}
```

---

## 测试

### 带宽聚合测试

```go
func TestAggregator(t *testing.T) {
	// 创建模拟连接
	var transports []net.Conn
	for i := 0; i < 3; i++ {
		client, server := net.Pipe()
		transports = append(transports, client)
		
		// 接收端
		go func(conn net.Conn) {
			io.Copy(io.Discard, conn)
		}(server)
	}

	logger, _ := zap.NewDevelopment()
	agg := mux.NewAggregator(transports, logger)
	defer agg.Close()

	// 测试写入
	sessionID := uuid.New()
	data := make([]byte, 1024*1024)
	
	err := agg.Write(sessionID, data)
	require.NoError(t, err)
}
```

---

## 下一步

继续阅读：
- **第4部分**: 路由层与协议层实现
