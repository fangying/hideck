package device

import "testing"

func TestOutboundCLCCID(t *testing.T) {
	for _, tt := range []struct {
		name, response string
		id             uint8
		ok             bool
	}{
		{"voice not data", "+CLCC: 1,0,0,1,0,\"\",128\n+CLCC: 4,0,3,0,0,\"10086\",129", 4, true},
		{"incoming excluded", "+CLCC: 4,1,0,0,0,\"10086\",129", 0, false},
		{"wrong peer", "+CLCC: 4,0,3,0,0,\"10010\",129", 0, false},
		{"single anonymous", "+CLCC: 4,0,2,0,0,\"\",129", 4, true},
		{"ambiguous", "+CLCC: 4,0,2,0,0\n+CLCC: 5,0,2,0,0", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := outboundCLCCID(tt.response, "10086")
			if id != tt.id || ok != tt.ok {
				t.Fatalf("got %d,%v", id, ok)
			}
		})
	}
}
