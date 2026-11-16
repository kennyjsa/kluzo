// Package ports declares the interfaces the core and the supervisor use to talk
// to the outside world, and the types that cross those boundaries
// (docs/contracts/interfaces.md). It holds no implementation: the in-memory
// fakes live in ports/fakes and the suite every adapter must pass in
// ports/contracttest.
//
// References (milestone, issue, PR) are the tracker's own numbers written as
// decimal strings; the core treats them as opaque ids.
package ports
