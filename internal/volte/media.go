package volte

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

type callMedia struct {
	bridge *PCMBridge
	conn   net.PacketConn
	sdp    string
	pcm    PCMPort

	// deferred is used for mobile-terminated calls. The WebRTC endpoint is
	// created while the phone is ringing, but the modem PCM device must not be
	// opened until ATA has completed.
	deferred *switchablePCM
}

// switchablePCM starts as a silent source/sink and atomically switches to the
// modem PCM port after an incoming call is answered. Holding the read lock for
// a frame operation prevents Replace or Close from racing with bridge I/O.
type switchablePCM struct {
	mu      sync.RWMutex
	current PCMPort
	closed  bool
}

func (p *switchablePCM) ReadFrame() ([]int16, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.current == nil {
		return make([]int16, pcmuFrameSamples), nil
	}
	return p.current.ReadFrame()
}

func (p *switchablePCM) WriteFrame(samples []int16) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.current == nil {
		return nil
	}
	return p.current.WriteFrame(samples)
}

func (p *switchablePCM) Replace(next PCMPort) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		if next != nil {
			return next.Close()
		}
		return nil
	}
	previous := p.current
	p.current = next
	p.mu.Unlock()
	if previous != nil {
		return previous.Close()
	}
	return nil
}

func (p *switchablePCM) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	current := p.current
	p.current = nil
	p.mu.Unlock()
	if current != nil {
		return current.Close()
	}
	return nil
}

func (m *callMedia) activatePCM(pcm PCMPort) error {
	if m == nil || m.deferred == nil {
		if pcm != nil {
			return pcm.Close()
		}
		return nil
	}
	return m.deferred.Replace(pcm)
}

func pcmuOfferSDP(port int) string {
	return fmt.Sprintf(
		"v=0\r\no=hideck 0 0 IN IP4 127.0.0.1\r\ns=HiDeck VoLTE\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0 101\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:101 telephone-event/8000\r\na=fmtp:101 0-15\r\na=ptime:20\r\na=sendrecv\r\n",
		port,
	)
}

func rtpPortFromSDP(sdp string) (int, error) {
	for _, raw := range strings.Split(strings.ReplaceAll(sdp, "\r\n", "\n"), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) >= 2 && strings.HasPrefix(raw, "m=audio") {
			port, err := strconv.Atoi(fields[1])
			if err != nil || port <= 0 {
				return 0, fmt.Errorf("volte: SDP audio port %q", fields[1])
			}
			return port, nil
		}
	}
	return 0, fmt.Errorf("volte: SDP has no m=audio")
}

func sdpHasRecvOnly(sdp string) bool {
	return strings.Contains(sdp, "a=recvonly")
}

type nullPCM struct{}

func (nullPCM) ReadFrame() ([]int16, error) {
	return make([]int16, pcmuFrameSamples), nil
}
func (nullPCM) WriteFrame([]int16) error { return nil }
func (nullPCM) Close() error             { return nil }

func startCallMedia(browserSDP string, pcm PCMPort, listenOnly bool) (*callMedia, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("volte: listen PCMU: %w", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	var remote net.Addr
	if browserPort, err := rtpPortFromSDP(browserSDP); err == nil && browserPort > 0 {
		remote = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: browserPort}
	}
	if pcm == nil {
		pcm = nullPCM{}
	}
	if sdpHasRecvOnly(browserSDP) {
		listenOnly = true
	}
	bridge := NewPCMBridge(conn, remote, pcm, listenOnly)
	return &callMedia{bridge: bridge, conn: conn, sdp: pcmuOfferSDP(port), pcm: pcm}, nil
}

func startDeferredCallMedia(browserSDP string, listenOnly bool) (*callMedia, error) {
	pcm := &switchablePCM{}
	media, err := startCallMedia(browserSDP, pcm, listenOnly)
	if err != nil {
		_ = pcm.Close()
		return nil, err
	}
	media.deferred = pcm
	return media, nil
}

func (m *callMedia) Close() error {
	if m == nil {
		return nil
	}
	var err error
	if m.bridge != nil {
		err = m.bridge.Close()
		m.bridge = nil
	}
	return err
}

type mediaTable struct {
	mu    sync.Mutex
	items map[string]*callMedia
}

func (t *mediaTable) put(id string, media *callMedia) {
	t.mu.Lock()
	if t.items == nil {
		t.items = map[string]*callMedia{}
	}
	if old := t.items[id]; old != nil {
		_ = old.Close()
	}
	t.items[id] = media
	t.mu.Unlock()
}

func (t *mediaTable) take(id string) *callMedia {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.items[id]
	delete(t.items, id)
	return m
}

func (t *mediaTable) get(id string) *callMedia {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.items[id]
}
