package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

var v2Signature = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}

const (
	v2VersionCommandProxy = 0x21
	v2FamProtoTCP4        = 0x11
	v2FamProtoTCP6        = 0x21
	v2FamProtoUDP4        = 0x12
	v2FamProtoUDP6        = 0x22
)

func BuildV1(src, dst net.Addr) ([]byte, error) {
	srcAddr, dstAddr, err := parseTCPAddrs(src, dst)
	if err != nil {
		return nil, err
	}

	family := "TCP6"
	if srcAddr.IP.To4() != nil {
		family = "TCP4"
	}

	line := fmt.Sprintf(
		"PROXY %s %s %s %d %d\r\n",
		family,
		srcAddr.IP.String(),
		dstAddr.IP.String(),
		srcAddr.Port,
		dstAddr.Port,
	)

	return []byte(line), nil
}

func BuildV2(src, dst net.Addr, isUDP bool) ([]byte, error) {
	parsed, err := parseAddrPair(src, dst, isUDP)
	if err != nil {
		return nil, err
	}

	length := 12
	if parsed.isIPv6 {
		length = 36
	}

	buf := make([]byte, 16+length)
	copy(buf[:12], v2Signature)
	buf[12] = v2VersionCommandProxy
	buf[13] = parsed.familyProto
	binary.BigEndian.PutUint16(buf[14:16], uint16(length))

	offset := 16
	if parsed.isIPv6 {
		copy(buf[offset:offset+16], parsed.srcIP.To16())
		offset += 16
		copy(buf[offset:offset+16], parsed.dstIP.To16())
		offset += 16
	} else {
		copy(buf[offset:offset+4], parsed.srcIP.To4())
		offset += 4
		copy(buf[offset:offset+4], parsed.dstIP.To4())
		offset += 4
	}

	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(parsed.srcPort))
	offset += 2
	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(parsed.dstPort))

	return buf, nil
}

func WriteHeader(w io.Writer, src, dst net.Addr, version int, isUDP bool) error {
	var (
		header []byte
		err    error
	)

	switch version {
	case 1:
		if isUDP {
			return fmt.Errorf("PROXY protocol v1 does not support UDP")
		}
		header, err = BuildV1(src, dst)
	case 2:
		header, err = BuildV2(src, dst, isUDP)
	default:
		return fmt.Errorf("unsupported PROXY protocol version %d", version)
	}
	if err != nil {
		return err
	}

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write PROXY header failed: %w", err)
	}
	return nil
}

type parsedAddrs struct {
	srcIP       net.IP
	dstIP       net.IP
	srcPort     int
	dstPort     int
	isIPv6      bool
	familyProto byte
}

func parseAddrPair(src, dst net.Addr, isUDP bool) (*parsedAddrs, error) {
	if isUDP {
		srcAddr, ok := src.(*net.UDPAddr)
		if !ok {
			return nil, fmt.Errorf("source address %T is not UDP", src)
		}
		dstAddr, ok := dst.(*net.UDPAddr)
		if !ok {
			return nil, fmt.Errorf("destination address %T is not UDP", dst)
		}
		return buildParsed(srcAddr.IP, dstAddr.IP, srcAddr.Port, dstAddr.Port, true)
	}

	srcAddr, dstAddr, err := parseTCPAddrs(src, dst)
	if err != nil {
		return nil, err
	}
	return buildParsed(srcAddr.IP, dstAddr.IP, srcAddr.Port, dstAddr.Port, false)
}

func parseTCPAddrs(src, dst net.Addr) (*net.TCPAddr, *net.TCPAddr, error) {
	srcAddr, ok := src.(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("source address %T is not TCP", src)
	}
	dstAddr, ok := dst.(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("destination address %T is not TCP", dst)
	}

	if srcAddr.IP == nil || dstAddr.IP == nil {
		return nil, nil, fmt.Errorf("source/destination IP must be set")
	}

	return srcAddr, dstAddr, nil
}

func buildParsed(srcIP, dstIP net.IP, srcPort, dstPort int, isUDP bool) (*parsedAddrs, error) {
	if srcIP == nil || dstIP == nil {
		return nil, fmt.Errorf("source/destination IP must be set")
	}

	src4 := srcIP.To4()
	dst4 := dstIP.To4()
	if (src4 == nil) != (dst4 == nil) {
		return nil, fmt.Errorf("mixed IP family is not supported")
	}

	isIPv6 := src4 == nil
	familyProto := byte(v2FamProtoTCP4)
	if isUDP {
		if isIPv6 {
			familyProto = byte(v2FamProtoUDP6)
		} else {
			familyProto = byte(v2FamProtoUDP4)
		}
	} else if isIPv6 {
		familyProto = byte(v2FamProtoTCP6)
	}

	return &parsedAddrs{
		srcIP:       srcIP,
		dstIP:       dstIP,
		srcPort:     srcPort,
		dstPort:     dstPort,
		isIPv6:      isIPv6,
		familyProto: familyProto,
	}, nil
}
