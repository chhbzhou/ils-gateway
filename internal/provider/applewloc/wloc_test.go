package applewloc

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestRandomRadiusKeepsCoordinateInsideCircle(t *testing.T) {
	p := Profile{Latitude: 12.5, Longitude: 34.5, RandomRadius: 50}
	for i := 0; i < 100; i++ {
		jittered := jitterProfile(p)
		dLat := (jittered.Latitude - p.Latitude) * math.Pi / 180
		dLon := (jittered.Longitude - p.Longitude) * math.Pi / 180
		a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(p.Latitude*math.Pi/180)*math.Cos(jittered.Latitude*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
		distance := 2 * 6378137 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
		if distance > p.RandomRadius+0.01 {
			t.Fatalf("jitter exceeded radius: %.3f", distance)
		}
	}
}

func msg(fields ...[]byte) []byte {
	var out []byte
	for _, field := range fields {
		out = append(out, field...)
	}
	return out
}

func varField(number uint32, value int64) []byte {
	return append(key(number, 0), i64(value)...)
}

func bytesField(number uint32, value []byte) []byte {
	out := append([]byte{}, key(number, 2)...)
	out = append(out, encVarint(uint64(len(value)))...)
	return append(out, value...)
}

func locationRecord() []byte {
	location := msg(varField(1, 1), varField(2, 2))
	return bytesField(2, location)
}

func rootPayload() []byte {
	return bytesField(2, locationRecord())
}

func TestRewriteBarePreservesUnknown(t *testing.T) {
	root := msg(rootPayload(), varField(40, 123))
	out, result, err := RewriteBody(root, Profile{Latitude: 35.5, Longitude: -139.7, HorizontalAccuracy: 10, VerticalAccuracy: 20})
	if err != nil || result.Locations != 1 {
		t.Fatalf("rewrite failed: %v %+v", err, result)
	}
	fields, err := parse(out, 0)
	if err != nil || len(fields) != 2 {
		t.Fatalf("output parse failed: %v", err)
	}
	original, _ := parse(root, 0)
	if !bytes.Equal(fields[1].raw, original[1].raw) {
		t.Fatal("unknown field not preserved")
	}
}

func TestFixedPrefixAndSuffix(t *testing.T) {
	payload := rootPayload()
	body := append([]byte{0, 1, 0, 0, 0, 1, 0, 0, byte(len(payload) >> 8), byte(len(payload))}, payload...)
	body = append(body, 9, 8)
	out, result, err := RewriteBody(body, Profile{Latitude: 1, Longitude: 2})
	if err != nil || result.Envelope != "fixed-prefix" || out[len(out)-2] != 9 {
		t.Fatalf("fixed-prefix failed: %v %+v", err, result)
	}
}

func TestAmbiguousMarker(t *testing.T) {
	payload := rootPayload()
	marker := []byte{0, 0, 0, 1, 0, 0, byte(len(payload) >> 8), byte(len(payload))}
	body := append(append(append([]byte{}, marker...), payload...), marker...)
	body = append(body, payload...)
	if _, _, err := RewriteBody(body, Profile{}); err != ErrAmbiguous {
		t.Fatalf("expected ErrAmbiguous, got %v", err)
	}
}

func TestMalformedLength(t *testing.T) {
	if _, _, err := RewriteBody([]byte{0x12, 0x05, 1}, Profile{}); err == nil {
		t.Fatal("expected malformed input error")
	}
}

func TestARPC(t *testing.T) {
	payload := rootPayload()
	body := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(body[12:], uint32(len(payload)))
	body = append(body, payload...)
	if _, result, err := RewriteBody(body, Profile{Latitude: 3, Longitude: 4}); err != nil || result.Envelope != "arpc" {
		t.Fatalf("ARPC failed: %v %+v", err, result)
	}
}

func TestZeroCoordinatesAreWritten(t *testing.T) {
	root := msg(bytesField(2, bytesField(2, msg(varField(1, 9), varField(2, 8)))))
	out, _, err := RewriteBody(root, Profile{Latitude: 0, Longitude: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte{0x08, 0x00}) || !bytes.Contains(out, []byte{0x10, 0x00}) {
		t.Fatal("zero coordinates were not encoded")
	}
}

func TestBareBodyIsNotMisclassifiedAsFixedPrefix(t *testing.T) {
	root := rootPayload()
	_, result, err := RewriteBody(root, Profile{Latitude: 3, Longitude: 4})
	if err != nil || result.Envelope != "bare" {
		t.Fatalf("unexpected envelope: %v %+v", err, result)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = parse(body, 0)
	})
}
