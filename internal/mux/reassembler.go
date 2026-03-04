package mux

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// Reassembler 负责在接收端重组分片，修复部分读取导致的数据丢失
type Reassembler struct {
	mu        sync.Mutex
	chunks    map[uint32][]byte
	nextSeq   uint32
	isClosed  bool
	output    chan []byte
	flowCtrl  *WindowFlowController
	
	pending   []byte // 暂存未读完的分片数据
}

func NewReassembler(flowCtrl *WindowFlowController) *Reassembler {
	return &Reassembler{
		chunks:   make(map[uint32][]byte),
		output:   make(chan []byte, 1024),
		flowCtrl: flowCtrl,
	}
}

func (r *Reassembler) Push(data []byte) error {
	if len(data) < 24 { return fmt.Errorf("chunk data too short") }
	seq := binary.BigEndian.Uint32(data[16:20])
	chunkSize := binary.BigEndian.Uint16(data[20:22])
	flags := data[22]
	if len(data) < 24+int(chunkSize) { return fmt.Errorf("size mismatch") }
	chunk := data[24 : 24+int(chunkSize)]

	r.mu.Lock()
	defer r.mu.Unlock()
	if seq < r.nextSeq { return nil }
	r.chunks[seq] = chunk
	r.flowCtrl.OnBuffered(len(chunk))

	for {
		if nextChunk, ok := r.chunks[r.nextSeq]; ok {
			if r.flowCtrl.ShouldPause() { break }
			r.output <- nextChunk
			delete(r.chunks, r.nextSeq)
			r.nextSeq++
			if flags&0x01 != 0 && uint32(len(r.chunks)) == 0 {
				if !r.isClosed { close(r.output); r.isClosed = true }
				break
			}
		} else { break }
	}
	return nil
}

// Read 实现正确的分片读取逻辑
func (r *Reassembler) Read(p []byte) (int, error) {
	// 1. 如果有上次没读完的数据，优先返回
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		r.flowCtrl.OnConsumed(n)
		return n, nil
	}

	// 2. 从管道取新块
	chunk, ok := <-r.output
	if !ok { return 0, io.EOF }

	n := copy(p, chunk)
	if n < len(chunk) {
		r.pending = chunk[n:] // 暂存剩余部分
	}
	r.flowCtrl.OnConsumed(n)
	return n, nil
}
