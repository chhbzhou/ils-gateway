package proxy

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
)

type Decision string

const (
	Passthrough Decision = "passthrough"
	MITM        Decision = "mitm"
)

type Policy struct {
	MaxHello         int
	AllowSNI         map[string]bool
	AllowDestination map[string]bool
}
type Result struct {
	Decision    Decision
	SNI         string
	Destination string
	Reason      string
}

func NormalizeSNI(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// Classify inspects a TLS record containing ClientHello. It deliberately does
// not attempt decryption or proxying and returns passthrough on any malformed
// or oversized input.
func Classify(hello []byte, original net.IP, port int, p Policy) Result {
	r := Result{Decision: Passthrough, Destination: net.JoinHostPort(original.String(), "0")}
	if port > 0 {
		r.Destination = net.JoinHostPort(original.String(), strconv.Itoa(port))
	}
	if port != 443 {
		r.Reason = "non_tls_port"
		return r
	}
	if len(p.AllowDestination) == 0 {
		r.Reason = "destination_allowlist_empty"
		return r
	}
	max := p.MaxHello
	if max <= 0 {
		max = 64 << 10
	}
	if len(hello) > max {
		r.Reason = "hello_too_large"
		return r
	}
	sni, ok := parseSNI(hello)
	if !ok {
		r.Reason = "no_sni"
		return r
	}
	r.SNI = NormalizeSNI(sni)
	if r.SNI == "" || !p.AllowSNI[r.SNI] {
		r.Reason = "sni_not_allowed"
		return r
	}
	if !p.AllowDestination[original.String()] && !p.AllowDestination["*"] {
		r.Reason = "destination_not_allowed"
		return r
	}
	r.Decision, r.Reason = MITM, "allowlist_match"
	return r
}

func parseSNI(b []byte) (string, bool) {
	if len(b) < 5 || b[0] != 22 {
		return "", false
	}
	recordLen := int(binary.BigEndian.Uint16(b[3:5]))
	if recordLen < 4 || recordLen+5 > len(b) {
		return "", false
	}
	handshake := b[5 : 5+recordLen]
	if len(handshake) < 4 || handshake[0] != 1 {
		return "", false
	}
	helloLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
	if helloLen != len(handshake)-4 {
		return "", false
	}
	body := handshake[4:]
	p := 0
	if p+2+32+1 > len(body) {
		return "", false
	}
	p += 2 + 32
	sessionLen := int(body[p])
	p++
	if p+sessionLen+2 > len(body) {
		return "", false
	}
	p += sessionLen
	cipherLen := int(binary.BigEndian.Uint16(body[p : p+2]))
	p += 2
	if p+cipherLen+1 > len(body) {
		return "", false
	}
	p += cipherLen
	compressionLen := int(body[p])
	p++
	if p+compressionLen+2 > len(body) {
		return "", false
	}
	p += compressionLen
	extLen := int(binary.BigEndian.Uint16(body[p : p+2]))
	p += 2
	end := p + extLen
	if end > len(body) {
		return "", false
	}
	for p+4 <= end {
		typ := binary.BigEndian.Uint16(body[p : p+2])
		length := int(binary.BigEndian.Uint16(body[p+2 : p+4]))
		p += 4
		if p+length > end {
			return "", false
		}
		if typ == 0 && length >= 5 {
			q := p
			outerLen := int(binary.BigEndian.Uint16(body[q : q+2]))
			q += 2
			listLen := outerLen
			// Accept the historical corpus encoding with an additional
			// two-byte prefix, while preferring the RFC 6066 layout.
			if outerLen != length-2 && q+2 <= p+length {
				listLen = int(binary.BigEndian.Uint16(body[q : q+2]))
				q += 2
			}
			if listLen > length-2 || listLen < 3 || q+listLen > p+length {
				return "", false
			}
			if body[q] != 0 || q+3 > p+length {
				return "", false
			}
			nameLen := int(binary.BigEndian.Uint16(body[q+1 : q+3]))
			q += 3
			if nameLen == 0 || nameLen > listLen-3 || q+nameLen > p+length {
				return "", false
			}
			return string(body[q : q+nameLen]), true
		}
		p += length
	}
	return "", false
}
