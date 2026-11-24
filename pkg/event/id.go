package event

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// NewID returns a fresh event identifier in the form Publish assigns one: an
// RFC 9562 version 7 UUID in canonical text. Later milliseconds sort later, but
// the identifier records no publication order within one.
func NewID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error and always fills the buffer.
	_, _ = rand.Read(b[6:])
	// The leading 48 bits are the low 48 bits of the Unix time in
	// milliseconds, most significant first.
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(time.Now().UnixMilli()))
	copy(b[0:6], ms[2:8])
	b[6] = b[6]&0x0f | 0x70 // version 7
	b[8] = b[8]&0x3f | 0x80 // the RFC 9562 variant

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
