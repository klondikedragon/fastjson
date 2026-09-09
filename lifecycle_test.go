package fastjson

import (
	"strings"
	"testing"
)

// The tests below pin what happens to a value the caller keeps past the
// lifetime its allocator documents.
//
// Slot ownership records which value a slot belongs to, so it catches a value
// that names storage belonging to a different value. It cannot catch a pointer
// held past a reset: the slot address does not change, and after reuse the
// slot is owned by its new value exactly as it should be. Distinguishing the
// generations would mean a counter on every slot and a counter compare on
// every borrowed pointer, and it still would not tell a stale pointer from a
// fresh one held by the same caller. That boundary stays where the allocators
// document it: values obtained from an Arena or a Parser are valid until the
// next Reset or Parse call. AuditStorage covers the aliasing half.

func TestLifecycleArenaValueAfterReset(t *testing.T) {
	var a Arena
	root := a.NewObject()
	root.Set("k", a.NewString("payload"))
	borrowed := root.Get("k")

	a.Reset()

	// Reset emptied the slot, so nothing from the previous generation can be
	// read back through the borrowed pointer.
	if got := string(borrowed.MarshalTo(nil)); got != "null" {
		t.Fatalf("borrowed value after reset marshalled as %s; want null", got)
	}
	if borrowed.o.self != borrowed {
		t.Fatal("reset changed slot ownership")
	}
}

func TestLifecycleArenaValueAfterResetAndReuse(t *testing.T) {
	var a Arena
	root := a.NewObject()
	root.Set("k", a.NewString("payload"))
	borrowed := root.Get("k")

	healsBefore := IntegrityHealCount()
	a.Reset()
	churn(&a)

	// The slot is owned by whatever value the arena has since put in it, so
	// the ownership check has nothing to report. The borrowed pointer now
	// reads the new generation: this is the caller's contract, not a
	// condition the library can detect.
	if borrowed.o.self != borrowed {
		t.Fatal("a reused slot must own itself")
	}
	got := string(borrowed.MarshalTo(nil))
	if got == `"payload"` {
		t.Fatal("expected the slot to have been reused")
	}
	if IntegrityHealCount() != healsBefore {
		t.Fatal("reuse of an owned slot must not be contained")
	}

	// Reuse on its own creates no storage sharing, which is the half the
	// audit is responsible for.
	requireNoOverlaps(t, AuditStorage([]*Arena{&a}, nil))
}

func TestLifecycleParserValueAfterReparse(t *testing.T) {
	var p Parser
	v, err := p.Parse(`{"k":"payload"}`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	borrowed := v.Get("k")

	healsBefore := IntegrityHealCount()
	if _, err := p.Parse(bigDocument(500)); err != nil {
		t.Fatalf("cannot parse: %s", err)
	}

	if borrowed.o.self != borrowed {
		t.Fatal("a reused slot must own itself")
	}
	got := string(borrowed.MarshalTo(nil))
	if got == `"payload"` {
		t.Fatal("expected the slot to have been reused")
	}
	if IntegrityHealCount() != healsBefore {
		t.Fatal("reuse of an owned slot must not be contained")
	}
	requireNoOverlaps(t, AuditStorage(nil, []*Parser{&p}))
}

func TestLifecycleParserValueAfterEmptyReparse(t *testing.T) {
	var p Parser
	v, err := p.Parse(`{"k":"payload"}`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	borrowed := v.Get("k")

	// A parse that fails still resets the cache, so the borrowed pointer
	// stops referring to the previous document.
	if _, err := p.Parse(`{`); err == nil {
		t.Fatal("expected a parse error")
	}
	if got := string(borrowed.MarshalTo(nil)); strings.Contains(got, "payload") {
		t.Fatalf("borrowed value still reads the previous document: %s", got)
	}
}

// TestLifecycleBorrowedValueStoredInAnotherAllocator pins the containment
// boundary from the other side: a value borrowed from one arena and stored in
// a graph built by another is a pointer, not shared storage, so nothing is
// aliased and the audit stays quiet. What the second graph reads after the
// first arena is reset is the first arena's business.
func TestLifecycleBorrowedValueStoredInAnotherAllocator(t *testing.T) {
	var lender, holder Arena
	lent := lender.NewString("payload")
	root := holder.NewObject()
	root.Set("borrowed", lent)

	requireNoOverlaps(t, AuditStorage([]*Arena{&lender, &holder}, nil))

	lender.Reset()
	churn(&lender)

	// No ownership violation: the slot belongs to its new value.
	if got := string(root.MarshalTo(nil)); strings.Contains(got, "payload") {
		t.Fatalf("expected the lent slot to have been reused: %s", got)
	}
	requireNoOverlaps(t, AuditStorage([]*Arena{&lender, &holder}, nil))
}
