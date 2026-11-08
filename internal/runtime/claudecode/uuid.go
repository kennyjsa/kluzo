package claudecode

import (
	"crypto/sha1" //nolint:gosec // UUIDv5 is defined with SHA-1; it is an id, not a security control
	"fmt"
	"strconv"
)

// namespace is the fixed UUIDv5 namespace of Kluzo's Claude Code sessions.
var namespace = [16]byte{0x6b, 0x6c, 0x75, 0x7a, 0x6f, 0x2d, 0x63, 0x63, 0x2d, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e}

// harnessUUID is the id the harness session gets: the UUIDv5 of the Kluzo
// session id and the counter of harness sessions (ADR 0024, item 9). The same
// pair always gives the same id, so a resume finds its transcript.
func harnessUUID(sessionID string, counter int) string {
	h := sha1.New() //nolint:gosec // see above
	h.Write(namespace[:])
	h.Write([]byte(sessionID + "/" + strconv.Itoa(counter)))
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = u[6]&0x0f | 0x50 // version 5
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
