package volte

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActiveCLCCMustMatchSingleVoiceCall(t *testing.T) {
	response := "+CLCC: 2,1,0,1,0\n+CLCC: 4,1,0,0,0"
	if !clccHasActiveVoiceID(response, 4) {
		t.Fatal("matching active voice missing")
	}
	if clccHasActiveVoiceID(response, 2) {
		t.Fatal("data ID accepted as voice")
	}
	if clccHasActiveVoiceID(response+"\n+CLCC: 5,1,0,0,0", 4) {
		t.Fatal("multiple voice calls accepted")
	}
}

func leaseListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "qdc-lease-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, path
}

func TestRouteLeaseReadyAndRelease(t *testing.T) {
	l, path := leaseListener(t)
	released := make(chan struct{})
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("READY\n"))
		var b [1]byte
		_, _ = conn.Read(b[:])
		close(released)
	}()
	lease, err := acquireRoute(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	pcm := &memPCM{}
	p := &leasedPCM{PCMPort: pcm, lease: lease}
	_ = p.Close()
	_ = p.Close()
	if !pcm.isClosed() {
		t.Fatal("host PCM was not closed")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("lease not released")
	}
}

func TestRouteLeaseRejectsBusy(t *testing.T) {
	l, path := leaseListener(t)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("BUSY\n"))
	}()
	if conn, err := acquireRoute(context.Background(), path); err == nil {
		conn.Close()
		t.Fatal("busy route accepted")
	}
}

func TestRouteLeaseCancellationClosesPendingConnection(t *testing.T) {
	l, path := leaseListener(t)
	done := make(chan struct{})
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var b [1]byte
		_, _ = conn.Read(b[:])
		close(done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if conn, err := acquireRoute(ctx, path); err == nil {
		conn.Close()
		t.Fatal("cancelled route accepted")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pending lease leaked")
	}
}
