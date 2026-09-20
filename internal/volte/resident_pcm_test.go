package volte

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestWaitAudioRoute(t *testing.T) {
	if err := waitAudioRoute(context.Background(), time.Second, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := waitAudioRoute(context.Background(), time.Millisecond, func() bool { return false }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitAudioRoute(ctx, time.Second, func() bool { return true }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

type residentPCMHost struct {
	*FakeModem
	ready bool
}

func (h *residentPCMHost) USBResidentAudioReady(string) bool { return h.ready }

func TestResidentPCMBypassesQPCMV(t *testing.T) {
	h := &residentPCMHost{FakeModem: newFakeModem(), ready: true}
	h.Audio = "hw:2,0"
	h.Fail = map[string]error{"AT+QPCMV=1,2": errors.New("ERROR")}
	c := NewControllerWithBackup(h, t.TempDir())
	c.ensureVoicePCM("wwan1")
	if c.alsaUnavailable("wwan1") || c.Status("wwan1").QPCMVFailed {
		t.Fatal("ready resident route must permit ALSA without QPCMV")
	}
	for _, cmd := range h.Commands {
		if cmd == "AT+QPCMV=1,2" {
			t.Fatal("resident route must not use QPCMV")
		}
	}
}

func TestResidentPCMRecoversCachedQPCMVFailure(t *testing.T) {
	h := &residentPCMHost{FakeModem: newFakeModem()}
	h.Audio = "hw:2,0"
	h.Fail = map[string]error{"AT+QPCMV=1,2": errors.New("ERROR")}
	c := NewControllerWithBackup(h, t.TempDir())
	c.ensureVoicePCM("wwan1")
	if !c.alsaUnavailable("wwan1") || !c.Status("wwan1").QPCMVFailed {
		t.Fatal("unready runtime must not bypass QPCMV failure")
	}
	h.ready = true
	c.ensureVoicePCM("wwan1")
	if c.alsaUnavailable("wwan1") || c.Status("wwan1").QPCMVFailed {
		t.Fatal("resident recovery must clear cached QPCMV failure")
	}
	// Do not clear unrelated ALSA-open failures on every readiness check.
	c.markALSAUnavailable("wwan1")
	c.ensureVoicePCM("wwan1")
	if !c.alsaUnavailable("wwan1") {
		t.Fatal("resident readiness erased an unrelated ALSA failure")
	}
}

func TestPCMUPayloadExcludesExtensionsAndPadding(t *testing.T) {
	payload := bytes.Repeat([]byte{0xff}, pcmuFrameSamples)
	p := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, CSRC: []uint32{7}}, Payload: payload}
	if err := p.SetExtension(1, []byte{0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Add valid RTP padding in addition to the header extension and CSRC.
	raw[0] |= 0x20
	raw = append(raw, 0, 0, 0, 4)
	got, ok := rtpPCMUPayload(raw)
	if !ok || !bytes.Equal(got, payload) {
		t.Fatalf("payload contains non-audio bytes: ok=%v len=%d", ok, len(got))
	}
	raw[0] = (raw[0] & 0x3f) | 0x40
	if _, ok := rtpPCMUPayload(raw); ok {
		t.Fatal("invalid RTP version accepted")
	}
}
