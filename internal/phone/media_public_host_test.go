package phone

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestSetWebRTCPublicHostReportsResolutionFailure(t *testing.T) {
	settings := &webrtc.SettingEngine{}
	wantErr := errors.New("lookup failed")
	err := setWebRTCPublicHost(settings, "hideck.example.com", func(context.Context, string) ([]net.IPAddr, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("setWebRTCPublicHost() error = %v", err)
	}
}

func TestResolveWebRTCPublicIPs(t *testing.T) {
	lookupCalls := 0
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookupCalls++
		if host != "hideck.example.com" {
			t.Fatalf("lookup host = %q", host)
		}
		return []net.IPAddr{
			{IP: net.ParseIP("203.0.113.10")},
			{IP: net.ParseIP("203.0.113.10")},
			{IP: net.ParseIP("2001:db8::10")},
		}, nil
	}

	got, err := resolveWebRTCPublicIPs("hideck.example.com", lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.10", "2001:db8::10"}
	if !reflect.DeepEqual(got, want) || lookupCalls != 1 {
		t.Fatalf("resolveWebRTCPublicIPs() = %v, calls = %d", got, lookupCalls)
	}

	got, err = resolveWebRTCPublicIPs("203.0.113.20", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"203.0.113.20"}) || lookupCalls != 1 {
		t.Fatalf("IP literal result = %v, calls = %d", got, lookupCalls)
	}
}

func TestFilterIPv4Candidates(t *testing.T) {
	sdp := "v=0\r\n" +
		"a=candidate:1 1 UDP 2130706431 192.0.2.10 7580 typ host\r\n" +
		"a=candidate:2 1 UDP 2130706430 2001:db8::189 7580 typ host\r\n" +
		"a=end-of-candidates\r\n"
	got := filterIPv4Candidates(sdp)
	if strings.Contains(got, "2001:db8::189") {
		t.Fatalf("IPv6 candidate was not filtered: %q", got)
	}
	if !strings.Contains(got, "192.0.2.10") {
		t.Fatalf("IPv4 candidate was filtered: %q", got)
	}
}
