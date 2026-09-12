// Package applewloc contains a deliberately conservative decoder for the
// undocumented Apple WLoc response envelopes. It never guesses on malformed
// or ambiguous input: callers can forward the original body unchanged.
package applewloc

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var (
	ErrMalformed   = errors.New("malformed protobuf or envelope")
	ErrAmbiguous   = errors.New("ambiguous WLoc envelope")
	ErrUnsupported = errors.New("unsupported WLoc envelope")
)

type Profile struct {
	Latitude, Longitude, Altitude, HorizontalAccuracy, VerticalAccuracy float64
	RandomRadius                                                        float64
	MotionType, MotionConfidence                                        *int64
	InsertMissingLocation                                               bool
}
type Result struct {
	Envelope  string
	Locations int
}
type field struct {
	num        uint32
	wire       byte
	raw, value []byte
}

func (p Profile) Validate() error {
	values := []float64{p.Latitude, p.Longitude, p.Altitude, p.HorizontalAccuracy, p.VerticalAccuracy, p.RandomRadius}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("profile contains non-finite value")
		}
	}
	if p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
		return fmt.Errorf("profile coordinates out of range")
	}
	if p.HorizontalAccuracy < 0 || p.VerticalAccuracy < 0 {
		return fmt.Errorf("profile accuracy must be non-negative")
	}
	if p.RandomRadius < 0 || p.RandomRadius > 100000 {
		return fmt.Errorf("profile random radius is out of range")
	}
	return nil
}

// maxMessage is the protocol ceiling; the daemon may configure a lower
// per-response rewrite budget (the documented default is 1 MiB).
const (
	maxMessage = 4 << 20
	maxFields  = 10000
	maxDepth   = 8
)

func RewriteBody(body []byte, p Profile) ([]byte, Result, error) {
	if err := p.Validate(); err != nil {
		return nil, Result{}, err
	}
	if len(body) > maxMessage {
		return nil, Result{}, fmt.Errorf("body exceeds limit: %w", ErrMalformed)
	}
	p = jitterProfile(p)
	for _, kind := range []string{"fixed-prefix", "arpc", "marker", "bare"} {
		payload, wrap, ok, err := unwrap(body, kind)
		if err != nil {
			if errors.Is(err, ErrAmbiguous) {
				return nil, Result{}, err
			}
			continue
		}
		if !ok {
			continue
		}
		out, n, err := rewriteProto(payload, p, 0)
		if err != nil || n == 0 {
			if err != nil {
				return nil, Result{}, err
			}
			continue
		}
		if _, err = parse(out, 0); err != nil {
			return nil, Result{}, err
		}
		if (kind == "fixed-prefix" || kind == "marker") && len(out) > math.MaxUint16 {
			return nil, Result{}, fmt.Errorf("rewritten envelope exceeds uint16 payload limit: %w", ErrMalformed)
		}
		return wrap(out), Result{Envelope: kind, Locations: n}, nil
	}
	return nil, Result{}, ErrUnsupported
}

func jitterProfile(p Profile) Profile {
	if p.RandomRadius <= 0 {
		return p
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return p
	}
	const denominator = float64(^uint64(0)) + 1
	u1 := float64(binary.BigEndian.Uint64(entropy[:8])) / denominator
	u2 := float64(binary.BigEndian.Uint64(entropy[8:])) / denominator
	distance := math.Sqrt(u1) * p.RandomRadius
	bearing := 2 * math.Pi * u2
	angular := distance / 6378137
	latitude := p.Latitude * math.Pi / 180
	longitude := p.Longitude * math.Pi / 180
	jitteredLatitude := math.Asin(math.Sin(latitude)*math.Cos(angular) + math.Cos(latitude)*math.Sin(angular)*math.Cos(bearing))
	jitteredLongitude := longitude + math.Atan2(math.Sin(bearing)*math.Sin(angular)*math.Cos(latitude), math.Cos(angular)-math.Sin(latitude)*math.Sin(jitteredLatitude))
	p.Latitude = jitteredLatitude * 180 / math.Pi
	p.Longitude = math.Mod(jitteredLongitude*180/math.Pi+540, 360) - 180
	return p
}

func parse(b []byte, depth int) ([]field, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nesting depth: %w", ErrMalformed)
	}
	var fs []field
	for i := 0; i < len(b); {
		start := i
		key, n, err := readVarint(b[i:])
		if err != nil {
			return nil, err
		}
		i += n
		num, wire := uint32(key>>3), byte(key&7)
		if num == 0 || num >= 1<<29 || wire == 3 || wire == 4 {
			return nil, ErrMalformed
		}
		var value []byte
		switch wire {
		case 0:
			_, n, err = readVarint(b[i:])
			if err != nil {
				return nil, err
			}
			value = b[i : i+n]
			i += n
		case 1:
			if len(b)-i < 8 {
				return nil, ErrMalformed
			}
			value = b[i : i+8]
			i += 8
		case 2:
			var l uint64
			l, n, err = readVarint(b[i:])
			if err != nil || l > uint64(len(b)-i-n) {
				return nil, ErrMalformed
			}
			i += n
			value = b[i : i+int(l)]
			i += int(l)
		case 5:
			if len(b)-i < 4 {
				return nil, ErrMalformed
			}
			value = b[i : i+4]
			i += 4
		default:
			return nil, ErrMalformed
		}
		fs = append(fs, field{num: num, wire: wire, raw: b[start:i], value: value})
		if len(fs) > maxFields {
			return nil, ErrMalformed
		}
	}
	return fs, nil
}

func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i, c := range b {
		if i == 10 || (i == 9 && c > 1) {
			return 0, 0, ErrMalformed
		}
		v |= uint64(c&127) << uint(7*i)
		if c < 128 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrMalformed
}
func encVarint(v uint64) []byte   { var b [10]byte; n := binary.PutUvarint(b[:], v); return b[:n] }
func key(n uint32, w byte) []byte { return encVarint(uint64(n)<<3 | uint64(w)) }
func i64(v int64) []byte          { return encVarint(uint64(v)) }

func rewriteProto(b []byte, p Profile, depth int) ([]byte, int, error) {
	fs, err := parse(b, depth)
	if err != nil {
		return nil, 0, err
	}
	out := make([]byte, 0, len(b))
	count := 0
	for _, f := range fs {
		if f.wire == 2 && (f.num == 2 || f.num == 22 || f.num == 24) {
			child, n, e := rewriteRecord(f.value, p, f.num, depth+1)
			if e != nil {
				return nil, 0, e
			}
			if n > 0 {
				out = append(out, key(f.num, 2)...)
				out = append(out, encVarint(uint64(len(child)))...)
				out = append(out, child...)
				count += n
				continue
			}
		}
		out = append(out, f.raw...)
	}
	return out, count, nil
}
func rewriteRecord(b []byte, p Profile, recordType uint32, depth int) ([]byte, int, error) {
	fs, e := parse(b, depth)
	if e != nil {
		return nil, 0, e
	}
	out := make([]byte, 0, len(b))
	n := 0
	for _, f := range fs {
		locationField := uint32(5)
		if recordType == 2 {
			locationField = 2
		}
		if f.wire == 2 && f.num == locationField {
			loc, e := parse(f.value, depth+1)
			if e != nil {
				return nil, 0, e
			}
			if hasLocation(loc) {
				x := location(loc, p)
				out = append(out, key(f.num, 2)...)
				out = append(out, encVarint(uint64(len(x)))...)
				out = append(out, x...)
				n++
				continue
			}
		}
		out = append(out, f.raw...)
	}
	return out, n, nil
}
func hasLocation(fs []field) bool {
	lat, lon := false, false
	for _, f := range fs {
		if f.wire != 0 {
			continue
		}
		if f.num == 1 {
			lat = true
		}
		if f.num == 2 {
			lon = true
		}
	}
	return lat && lon
}
func location(fs []field, p Profile) []byte {
	var out []byte
	replaced := map[uint32]bool{}
	vals := map[uint32]int64{1: int64(math.Round(p.Latitude * 1e8)), 2: int64(math.Round(p.Longitude * 1e8)), 3: int64(math.Round(p.HorizontalAccuracy)), 5: int64(math.Round(p.Altitude)), 6: int64(math.Round(p.VerticalAccuracy))}
	if p.MotionType != nil {
		vals[11] = *p.MotionType
	}
	if p.MotionConfidence != nil {
		vals[12] = *p.MotionConfidence
	}
	for _, f := range fs {
		if f.wire == 0 && vals[f.num] != 0 || f.wire == 0 && (f.num == 1 || f.num == 2 || f.num == 3 || f.num == 5 || f.num == 6 || f.num == 11 || f.num == 12) {
			if v, ok := vals[f.num]; ok {
				out = append(out, key(f.num, 0)...)
				out = append(out, i64(v)...)
				replaced[f.num] = true
				continue
			}
		}
		out = append(out, f.raw...)
	}
	for _, n := range []uint32{1, 2, 3, 5, 6, 11, 12} {
		if v, ok := vals[n]; ok && !replaced[n] && (n < 7 || p.MotionType != nil && n == 11 || p.MotionConfidence != nil && n == 12) {
			out = append(out, key(n, 0)...)
			out = append(out, i64(v)...)
		}
	}
	return out
}

type envelope func([]byte) []byte

func looksLikeFixedPrefix(p []byte) bool {
	return len(p) == 8 && p[0] == 0 && p[1] == 1 && p[2] == 0 && p[3] == 0 && p[4] == 0 && p[6] == 0 && p[7] == 0
}

func unwrap(b []byte, kind string) ([]byte, envelope, bool, error) {
	switch kind {
	case "fixed-prefix":
		if len(b) < 10 || !looksLikeFixedPrefix(b[:8]) {
			return nil, nil, false, nil
		}
		l := int(binary.BigEndian.Uint16(b[8:10]))
		if l > len(b)-10 {
			return nil, nil, false, nil
		}
		p := b[10 : 10+l]
		if _, e := parse(p, 0); e != nil {
			return nil, nil, false, nil
		}
		return p, func(x []byte) []byte {
			return append(append(append([]byte{}, b[:8]...), byte(len(x)>>8), byte(len(x))), append(x, b[10+l:]...)...)
		}, true, nil
	case "arpc":
		i := 2
		for j := 0; j < 3; j++ {
			if len(b)-i < 2 {
				return nil, nil, false, nil
			}
			l := int(binary.BigEndian.Uint16(b[i:]))
			i += 2
			if l > len(b)-i {
				return nil, nil, false, nil
			}
			i += l
		}
		if len(b)-i < 8 {
			return nil, nil, false, nil
		}
		i += 4
		l := int(binary.BigEndian.Uint32(b[i:]))
		i += 4
		if l > len(b)-i {
			return nil, nil, false, nil
		}
		p := b[i : i+l]
		if _, e := parse(p, 0); e != nil {
			return nil, nil, false, nil
		}
		return p, func(x []byte) []byte {
			z := append([]byte{}, b[:i-4]...)
			var q [4]byte
			binary.BigEndian.PutUint32(q[:], uint32(len(x)))
			z = append(z, q[:]...)
			z = append(z, x...)
			z = append(z, b[i+l:]...)
			return z
		}, true, nil
	case "marker":
		m := []byte{0, 0, 0, 1, 0, 0}
		idx := -1
		for i := 0; i+len(m)+2 <= len(b); i++ {
			if string(b[i:i+len(m)]) == string(m) {
				l := int(binary.BigEndian.Uint16(b[i+len(m):]))
				if l <= len(b)-i-len(m)-2 {
					p := b[i+len(m)+2 : i+len(m)+2+l]
					if _, e := parse(p, 0); e == nil {
						if idx >= 0 {
							return nil, nil, false, ErrAmbiguous
						}
						idx = i
					}
				}
			}
		}
		if idx < 0 {
			return nil, nil, false, nil
		}
		l := int(binary.BigEndian.Uint16(b[idx+6:]))
		start := idx + 8
		return b[start : start+l], func(x []byte) []byte {
			z := append([]byte{}, b[:idx+6]...)
			z = append(z, byte(len(x)>>8), byte(len(x)))
			z = append(z, x...)
			z = append(z, b[start+l:]...)
			return z
		}, true, nil
	case "bare":
		if _, e := parse(b, 0); e != nil {
			return nil, nil, false, nil
		}
		return b, func(x []byte) []byte { return x }, true, nil
	}
	return nil, nil, false, nil
}
