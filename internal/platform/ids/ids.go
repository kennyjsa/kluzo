// Package ids generates the prefixed ULIDs used as internal identifiers
// (docs/contracts/README.md): "ws_01J…", "nd_01J…", "ss_01J…", "ev_01J…".
//
// The ULID encoding is implemented here instead of importing a library: it is
// small and stable (ADR 0017, criterion 1).
package ids

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Prefix identifies the kind of entity an ID refers to.
type Prefix string

// Known prefixes.
const (
	Project   Prefix = "prj"
	Workspace Prefix = "ws"
	Node      Prefix = "nd"
	Session   Prefix = "ss"
	Event     Prefix = "ev"
	Effect    Prefix = "ef"
	Decision  Prefix = "dc"
	Question  Prefix = "qs"
	Proposal  Prefix = "pp"
	GateRun   Prefix = "gr"
)

// Crockford's base32 alphabet, as specified by ULID.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Generator produces ULIDs that are strictly increasing for a given
// generator, even when several are created within the same millisecond.
type Generator struct {
	mu       sync.Mutex
	entropy  io.Reader
	lastMS   uint64
	lastRand [10]byte
}

// NewGenerator returns a Generator that reads randomness from entropy.
// Pass nil to use crypto/rand.
func NewGenerator(entropy io.Reader) *Generator {
	if entropy == nil {
		entropy = rand.Reader
	}
	return &Generator{entropy: entropy}
}

// New returns a new identifier "<prefix>_<ulid>" for time t.
func (g *Generator) New(p Prefix, t time.Time) (string, error) {
	u, err := g.ulid(t)
	if err != nil {
		return "", err
	}
	return string(p) + "_" + u, nil
}

func (g *Generator) ulid(t time.Time) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := uint64(t.UnixMilli())
	if ms > (1<<48)-1 {
		return "", fmt.Errorf("ids: timestamp %v out of ULID range", t)
	}

	var r [10]byte
	if ms == g.lastMS {
		// Monotonic within the same millisecond: increment the previous randomness.
		r = g.lastRand
		if !increment(&r) {
			return "", fmt.Errorf("ids: ULID randomness overflow within one millisecond")
		}
	} else {
		if _, err := io.ReadFull(g.entropy, r[:]); err != nil {
			return "", fmt.Errorf("ids: read entropy: %w", err)
		}
	}
	g.lastMS, g.lastRand = ms, r

	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	copy(b[6:], r[:])
	return encode(b), nil
}

// increment adds one to the big-endian number in r and reports false on overflow.
func increment(r *[10]byte) bool {
	for i := len(r) - 1; i >= 0; i-- {
		r[i]++
		if r[i] != 0 {
			return true
		}
	}
	return false
}

// encode writes the 128-bit value as 26 Crockford base32 characters.
// 26 × 5 = 130 bits, so the first character carries only the top 3 bits.
func encode(b [16]byte) string {
	var sb strings.Builder
	sb.Grow(26)
	sb.WriteByte(alphabet[b[0]>>5])
	acc := uint32(b[0] & 0x1F)
	bits := 5
	for i := 1; ; i++ {
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(alphabet[(acc>>uint(bits))&0x1F])
			acc &= (1 << uint(bits)) - 1
		}
		if i == len(b) {
			break
		}
		acc = acc<<8 | uint32(b[i])
		bits += 8
	}
	return sb.String()
}
