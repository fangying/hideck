package volte

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/yibaiba/hideck/pkg/logger"
)

const routeSocket = "/run/hideck-qdc507-audio/control.sock"

// The root-owned marker is an explicit deployment opt-in. A broker outage
// must fail closed, not silently revert to the boot-time resident route.
func (c *Controller) managedRoute(deviceID string) bool {
	h, ok := c.host.(atVoiceStatusHost)
	if !ok || !h.UseATVoiceStatus(deviceID) {
		return false
	}
	_, err := os.Stat("/run/hideck-qdc507-audio/managed")
	return err == nil
}

func (c *Controller) activateManagedOutbound(deviceID, callID string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		call, ok := c.lookup(deviceID, callID)
		if !ok || stateRank(call.State) == rankTerminal {
			return
		}
		if call.State == "connected" {
			pcm, err := c.managedCallPCM(context.Background(), deviceID, callID)
			if err == nil {
				if media := c.media.get(callID); media != nil {
					err = media.activatePCM(pcm)
				} else {
					_ = pcm.Close()
					err = errors.New("outbound media endpoint disappeared")
				}
			}
			if err == nil {
				return
			}
			logger.Warn("QDC507 outbound audio failed", "device", deviceID, "err", err)
			break
		}
		select {
		case <-ticker.C:
			continue
		case <-timer.C:
		}
		break
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.HangupCall(ctx, deviceID, callID); err != nil {
		logger.Warn("QDC507 outbound cleanup failed", "device", deviceID, "err", err)
	}
}

// The open Unix connection is the lease. Process death releases it without
// credentials, log scraping, a shell command API, or a network listener.
func acquireRoute(ctx context.Context, path string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(25 * time.Second))
	line, err := bufio.NewReaderSize(conn, 128).ReadString('\n')
	if err != nil || line != "READY\n" {
		_ = conn.Close()
		return nil, fmt.Errorf("QDC507 route unavailable (%q): %w", line, errors.Join(errors.New("lease rejected"), err, ctx.Err()))
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

type leasedPCM struct {
	PCMPort
	lease net.Conn
	once  sync.Once
	err   error
}

func (p *leasedPCM) Close() error {
	p.once.Do(func() {
		// Host PCM must close BEFORE the broker dismantles module D4/UAC.
		p.err = errors.Join(p.PCMPort.Close(), p.lease.Close())
	})
	return p.err
}

func (c *Controller) managedCallPCM(ctx context.Context, deviceID, callID string) (PCMPort, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	// Hangup during route preparation must release the pending lease too.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				call, ok := c.lookup(deviceID, callID)
				if !ok || stateRank(call.State) == rankTerminal {
					cancel()
					return
				}
			}
		}
	}()
	lease, err := acquireRoute(ctx, routeSocket)
	if err != nil {
		return nil, err
	}
	// A new, verified runtime lease permits retrying an ALSA failure from a
	// previous call; do not permanently cache an unplug/startup transient.
	c.mu.Lock()
	c.ensureLocked(deviceID).alsaUnavailable = false
	c.mu.Unlock()
	pcm := c.callPCM(deviceID)
	if _, silent := pcm.(nullPCM); silent {
		_ = lease.Close()
		return nil, errors.New("QDC507 route ready but real ALSA PCM could not be opened")
	}
	if ctx.Err() != nil {
		_ = pcm.Close()
		_ = lease.Close()
		return nil, ctx.Err()
	}
	port := &leasedPCM{PCMPort: pcm, lease: lease}
	go func() {
		var b [1]byte
		// Broker exit/lost readiness must close the host stream too.
		_, _ = lease.Read(b[:])
		_ = port.Close()
	}()
	return port, nil
}
