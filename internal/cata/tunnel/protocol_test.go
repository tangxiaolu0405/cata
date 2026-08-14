package tunnel

import "testing"

func TestEncodeDecodeData(t *testing.T) {
	cases := [][]byte{
		[]byte("hello\nworld"),
		[]byte{},
		[]byte{0x00, 0x01, 0xfe, 0xff, '\n'},
		[]byte("中文 payload with spaces and \t tabs"),
	}
	for _, in := range cases {
		out, err := DecodeData(EncodeData(in))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if string(out) != string(in) {
			t.Fatalf("roundtrip mismatch: %q != %q", out, in)
		}
	}
}

func TestFrameJSONShape(t *testing.T) {
	f := Frame{
		Type:     FrameLine,
		AgentID:  "ws-1",
		Stream:   7,
		Data:     EncodeData([]byte("line\n")),
		Protocol: ProtocolName,
		Version:  Version,
	}
	if f.Type != FrameLine || f.Stream != 7 {
		t.Fatal("frame fields not set")
	}
	if f.Version != 1 || f.Protocol != "cata-tunnel.v1" {
		t.Fatalf("protocol constants: version=%d protocol=%q", f.Version, f.Protocol)
	}
	if MaxFrameBytes != 8<<20 {
		t.Fatalf("MaxFrameBytes=%d", MaxFrameBytes)
	}
}
