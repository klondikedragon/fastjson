package fastjson

import (
	"fmt"
	"sync/atomic"
)

// IntegrityError reports that a Value was reached through an allocator slot
// that the Value does not own.
//
// It is raised as a panic value rather than returned, because every affected
// entry point has a signature with no error result. A caller that wants to
// survive the condition can recover and type-assert for *IntegrityError to
// tell it apart from any other panic.
type IntegrityError struct {
	// Site names the library entry point that observed the violation.
	Site string
}

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("fastjson: %s reached a value that does not own its allocator slot; "+
		"a Value must be obtained from an Arena or Parser and never copied by struct assignment", e.Site)
}

var (
	strictIntegrity atomic.Bool
	integrityHeals  atomic.Uint64
)

// SetStrictIntegrity selects what an allocator does when it finds a slot whose
// value does not own it.
//
// With strict false, the default, allocation and reset contain the condition:
// the slot's member and child headers are dropped without being written
// through, IntegrityHealCount is incremented, and the caller continues. Every
// other entry point still panics with *IntegrityError, so the condition is
// never silent for the code that reads or writes the affected value.
//
// With strict true, allocation and reset panic with *IntegrityError as well,
// so the offending struct assignment is reported where it is first observed
// instead of being contained. Use it in tests and staging.
//
// The setting is global and may be changed at any time.
func SetStrictIntegrity(strict bool) {
	strictIntegrity.Store(strict)
}

// IntegrityHealCount returns how many times an allocator has dropped member or
// child headers found on a slot whose value did not own it, instead of writing
// through them.
//
// A non-zero count means some caller copied a Value by struct assignment: two
// values then name one backing array, and a write through either reaches both.
// Build values through Arena and the Set calls instead.
func IntegrityHealCount() uint64 {
	return integrityHeals.Load()
}

// checkIntegrity panics unless v owns the slot it occupies.
//
// Ownership is recorded in one word, the self pointer of the embedded object,
// so a struct assignment of a whole Value and a struct assignment of an Object
// alone both show up here.
//
// A nil self marks a value that no allocator handed out, such as a
// package-level singleton or a Value the caller declared, and is allowed
// everywhere.
func (v *Value) checkIntegrity(site string) {
	if v.o.self != nil && v.o.self != v {
		panic(&IntegrityError{Site: site})
	}
}

// checkIntegrity panics unless o is the object embedded in the value that
// claims it.
func (o *Object) checkIntegrity(site string) {
	if o.self != nil && &o.self.o != o {
		panic(&IntegrityError{Site: site})
	}
}

// dropForeignHeaders releases the member and child storage that v names but
// does not own.
//
// The storage belongs to another value and may still be live there, so it is
// released by dropping the headers, never by writing through them. The slot is
// left unowned; the next allocation claims it.
func (v *Value) dropForeignHeaders() {
	v.o.kvs = nil
	v.o.keysUnescaped = false
	v.o.dropIndex()
	v.o.self = nil
	v.a = nil
	integrityHeals.Add(1)
}

// containForeignSlot reports whether v named a slot it does not own and, in
// that case, contains the condition. It panics in strict mode.
func (v *Value) containForeignSlot(site string) bool {
	if v.o.self == nil || v.o.self == v {
		return false
	}
	if strictIntegrity.Load() {
		panic(&IntegrityError{Site: site})
	}
	v.dropForeignHeaders()
	return true
}
