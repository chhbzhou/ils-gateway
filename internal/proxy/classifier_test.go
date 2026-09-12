package proxy

import (
	"encoding/binary"
	"net"
	"testing"
)

func hello(name string) []byte {
	sn := []byte(name)
	serverName := make([]byte, 7+len(sn))
	binary.BigEndian.PutUint16(serverName[0:2], 0)
	binary.BigEndian.PutUint16(serverName[2:4], uint16(3+len(sn)))
	serverName[4] = 0
	binary.BigEndian.PutUint16(serverName[5:7], uint16(len(sn)))
	copy(serverName[7:], sn)
	extensions := make([]byte, 4+len(serverName))
	binary.BigEndian.PutUint16(extensions[0:2], 0)
	binary.BigEndian.PutUint16(extensions[2:4], uint16(len(serverName)))
	copy(extensions[4:], serverName)
	body := make([]byte, 0, 2+32+1+2+2+1+2+len(extensions))
	body = append(body, 3, 3)
	body = append(body, make([]byte, 32)...)
	body = append(body, 0)
	body = append(body, 0, 2, 0, 0x2f)
	body = append(body, 1, 0)
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)
	h := make([]byte, 4+len(body))
	h[0] = 1
	h[1] = byte(len(body) >> 16)
	h[2] = byte(len(body) >> 8)
	h[3] = byte(len(body))
	copy(h[4:], body)
	r := make([]byte, 5+len(h))
	r[0] = 22
	r[1] = 3
	r[2] = 3
	binary.BigEndian.PutUint16(r[3:5], uint16(len(h)))
	copy(r[5:], h)
	return r
}
func TestClassify(t *testing.T) {
	r := Classify(hello("WLoc.Apple.com"), net.ParseIP("1.2.3.4"), 443, Policy{AllowSNI: map[string]bool{"wloc.apple.com": true}, AllowDestination: map[string]bool{"1.2.3.4": true}})
	if r.Decision != MITM || r.SNI != "wloc.apple.com" {
		t.Fatal(r)
	}
}
func TestPassthrough(t *testing.T) {
	r := Classify([]byte{22, 3, 3, 0, 1, 0}, net.ParseIP("1.2.3.4"), 443, Policy{})
	if r.Decision != Passthrough {
		t.Fatal(r)
	}
}

func FuzzParseSNI(f *testing.F) {
	f.Add(hello("gsp-ssl.ls.apple.com"))
	f.Add([]byte{22, 3, 3, 0, 1, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseSNI(b)
		_ = Classify(b, net.ParseIP("1.2.3.4"), 443, Policy{MaxHello: 64 << 10, AllowSNI: map[string]bool{"gsp-ssl.ls.apple.com": true}, AllowDestination: map[string]bool{"*": true}})
	})
}
