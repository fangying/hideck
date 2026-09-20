package volte

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	pcmuPayloadType   = 0
	pcmuClockRate     = 8000
	pcmuFrameSamples  = 160
	pcmuFrameDuration = 20 * time.Millisecond
	pcmJitterDepth    = 5
)

type PCMPort interface {
	ReadFrame() ([]int16, error)
	WriteFrame([]int16) error
	Close() error
}

type PCMBridge struct {
	conn        net.PacketConn
	remoteMu    sync.Mutex
	remote      net.Addr
	pcm         PCMPort
	listenOnly  bool
	closed      chan struct{}
	closeOnce   sync.Once
	workers     sync.WaitGroup
	seq         uint16
	ts          uint32
	toPCM       atomic.Uint64
	fromPCM     atomic.Uint64
	readErrors  atomic.Uint64
	writeErrors atomic.Uint64
	silent      atomic.Uint64
	lost        atomic.Uint64
	overflow    atomic.Uint64
}

func NewPCMBridge(conn net.PacketConn, remote net.Addr, pcm PCMPort, listenOnly bool) *PCMBridge {
	b := &PCMBridge{conn: conn, remote: remote, pcm: pcm, listenOnly: listenOnly, closed: make(chan struct{})}
	b.workers.Add(2)
	go b.downlink()
	go b.uplink()
	return b
}

func (b *PCMBridge) setRemote(addr net.Addr) {
	if b == nil || addr == nil {
		return
	}
	b.remoteMu.Lock()
	if b.remote == nil {
		b.remote = addr
	}
	b.remoteMu.Unlock()
}

func (b *PCMBridge) getRemote() net.Addr {
	if b == nil {
		return nil
	}
	b.remoteMu.Lock()
	defer b.remoteMu.Unlock()
	return b.remote
}

func (b *PCMBridge) Stats() (toPCM, fromPCM, silent, lost, overflow uint64) {
	return b.toPCM.Load(), b.fromPCM.Load(), b.silent.Load(), b.lost.Load(), b.overflow.Load()
}

func (b *PCMBridge) Close() error {
	var err error
	b.closeOnce.Do(func() {
		close(b.closed)
		if b.conn != nil {
			err = b.conn.Close()
		}
		if b.pcm != nil {
			_ = b.pcm.Close()
		}
		b.workers.Wait()
	})
	return err
}

func (b *PCMBridge) downlink() {
	defer b.workers.Done()
	buf := make([]byte, 2048)
	for {
		select {
		case <-b.closed:
			return
		default:
		}
		if b.conn == nil {
			return
		}
		_ = b.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := b.conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		if addr != nil {
			b.setRemote(addr)
		}
		payload, ok := rtpPCMUPayload(buf[:n])
		if !ok {
			b.lost.Add(1)
			continue
		}
		pcm := phone.DecodePCMU(payload)
		if b.pcm == nil {
			continue
		}
		if err := b.pcm.WriteFrame(pcm); err != nil {
			count := b.writeErrors.Add(1)
			if count <= 5 || count%100 == 0 {
				logger.Warn("VoLTE PCM 下行 ALSA 写入失败", "errors", count, "err", err)
			}
			b.overflow.Add(1)
			continue
		}
		count := b.toPCM.Add(1)
		if count%100 == 0 {
			logger.Info("VoLTE PCM 下行样本诊断", "frames", count, "nonzero", pcmNonZero(pcm), "peak", pcmPeak(pcm))
		}
	}
}

func (b *PCMBridge) uplink() {
	defer b.workers.Done()
	ticker := time.NewTicker(pcmuFrameDuration)
	defer ticker.Stop()
	for {
		select {
		case <-b.closed:
			return
		case <-ticker.C:
			remote := b.getRemote()
			if b.conn == nil || remote == nil {
				continue
			}
			samples := make([]int16, pcmuFrameSamples)
			if b.listenOnly || b.pcm == nil {
				b.silent.Add(1)
			} else {
				frame, err := b.pcm.ReadFrame()
				if err != nil {
					count := b.readErrors.Add(1)
					if count <= 5 || count%100 == 0 {
						logger.Warn("VoLTE PCM 上行 ALSA 读取失败", "errors", count, "err", err)
					}
					b.lost.Add(1)
				} else {
					copy(samples, frame)
					count := b.fromPCM.Add(1)
					if count%100 == 0 {
						logger.Info("VoLTE PCM 上行样本诊断", "frames", count, "nonzero", pcmNonZero(frame), "peak", pcmPeak(frame))
					}
				}
			}
			payload := phone.EncodePCMU(samples)
			pkt := encodePCMURTP(b.seq, b.ts, payload)
			b.seq++
			b.ts += pcmuFrameSamples
			if _, err := b.conn.WriteTo(pkt, remote); err != nil {
				return
			}
		}
	}
}

func pcmNonZero(samples []int16) int {
	n := 0
	for _, sample := range samples {
		if sample != 0 {
			n++
		}
	}
	return n
}

func pcmPeak(samples []int16) int {
	peak := 0
	for _, sample := range samples {
		value := int(sample)
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	return peak
}

func rtpPCMUPayload(pkt []byte) ([]byte, bool) {
	var packet rtp.Packet
	if err := packet.Unmarshal(pkt); err != nil || packet.Version != 2 || packet.PayloadType != pcmuPayloadType {
		return nil, false
	}
	return packet.Payload, true
}

func encodePCMURTP(seq uint16, ts uint32, payload []byte) []byte {
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80
	pkt[1] = pcmuPayloadType
	binary.BigEndian.PutUint16(pkt[2:], seq)
	binary.BigEndian.PutUint32(pkt[4:], ts)
	binary.BigEndian.PutUint32(pkt[8:], 0x48444543)
	copy(pkt[12:], payload)
	return pkt
}
