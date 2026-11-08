package contracts

// EdgeKind is the kind of relation between two nodes (ADR 0002). The core, the
// ports and the plan file all use this one type.
type EdgeKind string

// Edge kinds.
const (
	EdgeBlocks   EdgeKind = "blocks"   // To needs From's code integrated
	EdgeContract EdgeKind = "contract" // To needs only From's published interface
	EdgeConflict EdgeKind = "conflict" // From and To touch the same area; never run together
)
