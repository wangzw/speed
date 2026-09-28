package event

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// NewID returns a fresh event identifier in the form Publish assigns one: an
// RFC 9562 version 7 UUID in its canonical text form.
//
// A publisher calls it when it has to know an event's identifier before
// publishing — to record it next to the write the event announces, or to
// publish the same event again after a failure it cannot read, so that a
// consumer that already acted on the first attempt recognises the second.
//
// Version 7 leads with the time in milliseconds, so an identifier minted in a
// later millisecond sorts after one minted in an earlier one, each millisecond
// read from the clock of the process that minted it, and a consumer keeping the
// identifiers it has seen in an index appends to it rather than inserting all
// over it. The remaining 74 bits are random, which keeps apart two identifiers
// minted in the same millisecond, on one replica or on two, and leaves their
// order to chance: two events published within one millisecond, even one after
// the other on one goroutine, sort either way. The identifier is therefore no
// record of the order events were published in. It uses the standard library
// alone because every publisher imports this package.
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
