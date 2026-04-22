package protocol

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestBuildV1TCP4(t *testing.T) {
	t.Parallel()

	src := &net.TCPAddr{IP: net.IPv4(198, 51, 100, 10), Port: 34567}
	dst := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 5), Port: 25565}

	header, err := BuildV1(src, dst)
	if err != nil {
		t.Fatalf("BuildV1() error = %v", err)
	}

	want := "PROXY TCP4 198.51.100.10 203.0.113.5 34567 25565\r\n"
	if string(header) != want {
		t.Fatalf("BuildV1() = %q, want %q", string(header), want)
	}
}

func TestBuildV2TCP4(t *testing.T) {
	t.Parallel()

	src := &net.TCPAddr{IP: net.IPv4(198, 51, 100, 10), Port: 34567}
	dst := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 5), Port: 25565}

	header, err := BuildV2(src, dst, false)
	if err != nil {
		t.Fatalf("BuildV2() error = %v", err)
	}

	if len(header) != 28 {
		t.Fatalf("len(header) = %d, want 28", len(header))
	}
	if !bytes.Equal(header[:12], v2Signature) {
		t.Fatalf("signature mismatch")
	}
	if header[12] != v2VersionCommandProxy {
		t.Fatalf("version/command = 0x%x, want 0x%x", header[12], v2VersionCommandProxy)
	}
	if header[13] != v2FamProtoTCP4 {
		t.Fatalf("family/proto = 0x%x, want 0x%x", header[13], v2FamProtoTCP4)
	}
	if got := binary.BigEndian.Uint16(header[14:16]); got != 12 {
		t.Fatalf("address length = %d, want 12", got)
	}
	if got := net.IP(header[16:20]).String(); got != "198.51.100.10" {
		t.Fatalf("src ip = %s, want 198.51.100.10", got)
	}
	if got := net.IP(header[20:24]).String(); got != "203.0.113.5" {
		t.Fatalf("dst ip = %s, want 203.0.113.5", got)
	}
	if got := binary.BigEndian.Uint16(header[24:26]); got != 34567 {
		t.Fatalf("src port = %d, want 34567", got)
	}
	if got := binary.BigEndian.Uint16(header[26:28]); got != 25565 {
		t.Fatalf("dst port = %d, want 25565", got)
	}
}

func TestBuildV2UDP6(t *testing.T) {
	t.Parallel()

	src := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 19132}
	dst := &net.UDPAddr{IP: net.ParseIP("2001:db8::2"), Port: 19133}

	header, err := BuildV2(src, dst, true)
	if err != nil {
		t.Fatalf("BuildV2() error = %v", err)
	}

	if len(header) != 52 {
		t.Fatalf("len(header) = %d, want 52", len(header))
	}
	if header[13] != v2FamProtoUDP6 {
		t.Fatalf("family/proto = 0x%x, want 0x%x", header[13], v2FamProtoUDP6)
	}
	if got := binary.BigEndian.Uint16(header[14:16]); got != 36 {
		t.Fatalf("address length = %d, want 36", got)
	}
}

func TestWriteHeaderRejectsUDPV1(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := WriteHeader(
		&out,
		&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000},
		&net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 2000},
		1,
		true,
	)
	if err == nil {
		t.Fatal("WriteHeader() expected error, got nil")
	}
}
