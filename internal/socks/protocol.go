package socks

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	socksVersion5 = 0x05

	authNoAuth       = 0x00
	authNoAcceptable = 0xff

	cmdConnect = 0x01

	atypIPv4 = 0x01
	atypFQDN = 0x03
	atypIPv6 = 0x04

	replySuccess            = 0x00
	replyGeneralFailure     = 0x01
	replyCommandNotSupp     = 0x07
	replyAddrTypeNotSupp    = 0x08
	replyHostUnreachable    = 0x04
	defaultFailureReplyAtyp = atypIPv4
)

type request struct {
	atyp byte
	host string
	port uint16
}

func (r request) RouteKey() string {
	return normalizeHost(r.host)
}

func handleHandshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("read greeting header: %w", err)
	}
	if header[0] != socksVersion5 {
		return fmt.Errorf("unsupported socks version: %d", header[0])
	}

	nMethods := int(header[1])
	if nMethods == 0 {
		return fmt.Errorf("empty auth methods list")
	}

	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return fmt.Errorf("read auth methods: %w", err)
	}

	selected := byte(authNoAcceptable)
	for _, method := range methods {
		if method == authNoAuth {
			selected = authNoAuth
			break
		}
	}

	if _, err := conn.Write([]byte{socksVersion5, selected}); err != nil {
		return fmt.Errorf("write auth select: %w", err)
	}
	if selected != authNoAuth {
		return fmt.Errorf("no supported authentication method")
	}
	return nil
}

func readRequest(conn net.Conn) (*request, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, fmt.Errorf("read request header: %w", err)
	}

	if header[0] != socksVersion5 {
		return nil, fmt.Errorf("unsupported request version: %d", header[0])
	}
	if header[1] != cmdConnect {
		_ = writeFailureReply(conn, replyCommandNotSupp)
		return nil, fmt.Errorf("unsupported command: %d", header[1])
	}
	if header[2] != 0x00 {
		_ = writeFailureReply(conn, replyGeneralFailure)
		return nil, fmt.Errorf("invalid reserved byte: %d", header[2])
	}

	req := &request{atyp: header[3]}
	switch req.atyp {
	case atypIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return nil, fmt.Errorf("read ipv4 address: %w", err)
		}
		req.host = net.IP(addr).String()
	case atypFQDN:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return nil, fmt.Errorf("read fqdn length: %w", err)
		}
		if size[0] == 0 {
			_ = writeFailureReply(conn, replyAddrTypeNotSupp)
			return nil, fmt.Errorf("empty fqdn")
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return nil, fmt.Errorf("read fqdn: %w", err)
		}
		req.host = string(name)
	case atypIPv6:
		_ = writeFailureReply(conn, replyAddrTypeNotSupp)
		return nil, fmt.Errorf("ipv6 is not supported")
	default:
		_ = writeFailureReply(conn, replyAddrTypeNotSupp)
		return nil, fmt.Errorf("unsupported atyp: %d", req.atyp)
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return nil, fmt.Errorf("read destination port: %w", err)
	}
	req.port = binary.BigEndian.Uint16(portBytes)
	if req.port == 0 {
		_ = writeFailureReply(conn, replyGeneralFailure)
		return nil, fmt.Errorf("invalid destination port 0")
	}
	return req, nil
}

func writeFailureReply(conn net.Conn, code byte) error {
	reply := []byte{socksVersion5, code, 0x00, defaultFailureReplyAtyp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	_, err := conn.Write(reply)
	return err
}

func writeSuccessReply(conn net.Conn, bindAddr net.Addr) error {
	tcpAddr, ok := bindAddr.(*net.TCPAddr)
	if !ok {
		return writeFailureReply(conn, replyGeneralFailure)
	}

	ip4 := tcpAddr.IP.To4()
	if ip4 == nil {
		ip4 = net.IPv4zero.To4()
	}

	reply := make([]byte, 10)
	reply[0] = socksVersion5
	reply[1] = replySuccess
	reply[2] = 0x00
	reply[3] = atypIPv4
	copy(reply[4:8], ip4)
	binary.BigEndian.PutUint16(reply[8:10], uint16(tcpAddr.Port))
	_, err := conn.Write(reply)
	return err
}
