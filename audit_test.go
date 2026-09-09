package fastjson

import (
	"testing"
)

func overlapsFor(t *testing.T, arenas []*Arena, parsers []*Parser, scanners ...*Scanner) []StorageOverlap {
	t.Helper()
	return AuditStorage(arenas, parsers, scanners)
}

func requireNoOverlaps(t *testing.T, found []StorageOverlap) {
	t.Helper()
	if len(found) != 0 {
		for _, o := range found {
			t.Errorf("unexpected overlap: %s", o)
		}
		t.FailNow()
	}
}

func requireOverlap(t *testing.T, found []StorageOverlap, a, b *Value, field string) {
	t.Helper()
	for _, o := range found {
		if o.A.Field != field || o.B.Field != field {
			continue
		}
		if (o.A.Value == a && o.B.Value == b) || (o.A.Value == b && o.B.Value == a) {
			return
		}
	}
	t.Fatalf("no %s overlap reported between %p and %p; found %d overlaps: %v", field, a, b, len(found), found)
}

// TestAuditNoFindingsOnHealthyDocuments is the known-negative: a parsed
// document and an arena-built document that references one child under two
// keys must produce nothing.
func TestAuditNoFindingsOnHealthyDocuments(t *testing.T) {
	var p Parser
	if _, err := p.Parse(bigDocument(500)); err != nil {
		t.Fatalf("cannot parse: %s", err)
	}

	var a Arena
	root := a.NewObject()
	shared := a.NewObject()
	shared.Set("s", a.NewString("payload"))
	// One child reachable under two keys is a permitted shape.
	root.Set("first", shared)
	root.Set("second", shared)
	arr := a.NewArray()
	for i := 0; i < 50; i++ {
		arr.SetArrayItem(i, shared)
	}
	root.Set("arr", arr)

	requireNoOverlaps(t, overlapsFor(t, []*Arena{&a}, []*Parser{&p}))
}

func TestAuditDormantHeaderOnScalarSlot(t *testing.T) {
	var a Arena
	scalar := a.NewObject()
	scalar.Set("a", a.NewNumberInt(1))
	scalar.Set("b", a.NewNumberInt(2))
	// The node becomes a string but keeps the member storage it built.
	scalar.SetStringValue(&a, "now a string")

	other := a.NewObject()
	other.o.kvs = scalar.o.kvs

	found := overlapsFor(t, []*Arena{&a}, nil)
	requireOverlap(t, found, scalar, other, StorageMembers)
	if scalar.Type() != TypeString {
		t.Fatal("expected the dormant header to sit on a string typed slot")
	}
}

func TestAuditSharedStorageAcrossLiveRoots(t *testing.T) {
	var first, second Arena

	rootA := first.NewArray()
	childA := first.NewArray()
	childA.SetArrayItem(0, first.NewNumberInt(1))
	childA.SetArrayItem(1, first.NewNumberInt(2))
	rootA.SetArrayItem(0, childA)

	rootB := second.NewArray()
	childB := second.NewArray()
	childB.SetArrayItem(0, second.NewNumberInt(3))
	rootB.SetArrayItem(0, childB)

	childB.a = childA.a

	found := overlapsFor(t, []*Arena{&first, &second}, nil)
	requireOverlap(t, found, childA, childB, StorageChildren)
}

func TestAuditZeroLengthSliceWithRetainedCapacity(t *testing.T) {
	var a Arena
	owner := a.NewArray()
	for i := 0; i < 8; i++ {
		owner.SetArrayItem(i, a.NewNumberInt(i))
	}

	empty := a.NewArray()
	// No elements in use, but the capacity still names the owner's array.
	empty.a = owner.a[:0]
	if len(empty.a) != 0 || cap(empty.a) == 0 {
		t.Fatalf("expected an empty slice with retained capacity: len %d cap %d", len(empty.a), cap(empty.a))
	}

	found := overlapsFor(t, []*Arena{&a}, nil)
	requireOverlap(t, found, owner, empty, StorageChildren)
}

func TestAuditOverlappingSlicesWithDifferentStarts(t *testing.T) {
	var a Arena
	owner := a.NewArray()
	for i := 0; i < 8; i++ {
		owner.SetArrayItem(i, a.NewNumberInt(i))
	}

	tail := a.NewArray()
	tail.a = owner.a[3:]
	if &tail.a[0] == &owner.a[0] {
		t.Fatal("expected the two slices to start at different addresses")
	}

	found := overlapsFor(t, []*Arena{&a}, nil)
	requireOverlap(t, found, owner, tail, StorageChildren)
}

func TestAuditTwoObjectsSharingOneMemberArray(t *testing.T) {
	var a Arena
	first := a.NewObject()
	first.Set("k1", a.NewNumberInt(1))
	first.Set("k2", a.NewNumberInt(2))

	second := a.NewObject()
	second.o.kvs = first.o.kvs

	found := overlapsFor(t, []*Arena{&a}, nil)
	requireOverlap(t, found, first, second, StorageMembers)
}

// TestAuditFindsTheStructCopyShape covers the shape the ownership check is
// built for: a whole value assigned over another leaves both naming one array.
func TestAuditFindsTheStructCopyShape(t *testing.T) {
	var donor, recipient Arena
	d := donor.NewObject()
	d.Set("k1", donor.NewNumberInt(1))
	d.Set("k2", donor.NewNumberInt(2))

	r := recipient.NewObject()
	r.Set("other", recipient.NewNumberInt(3))
	copyValueStruct(r, d)

	found := overlapsFor(t, []*Arena{&donor, &recipient}, nil)
	requireOverlap(t, found, d, r, StorageMembers)

	// Containment removes the finding.
	recipient.Reset()
	requireNoOverlaps(t, overlapsFor(t, []*Arena{&donor, &recipient}, nil))
}

// TestAuditCoversScannerSlots checks the third cache: a Scanner owns slots of
// its own and is the source side of DeepCopyValue, so a shape that shares
// storage between a scanned value and an arena value has to be visible.
func TestAuditCoversScannerSlots(t *testing.T) {
	var sc Scanner
	sc.Init(`{"a":[1,2,3]} {"b":[4,5]}`)
	if !sc.Next() {
		t.Fatalf("cannot scan: %s", sc.Error())
	}
	scanned := sc.Value().Get("a")
	if scanned == nil {
		t.Fatal("missing scanned array")
	}

	var a Arena
	copied := a.DeepCopyValue(sc.Value())
	if got := string(copied.MarshalTo(nil)); got != `{"a":[1,2,3]}` {
		t.Fatalf("unexpected deep copy: %s", got)
	}
	// A deep copy allocates its own storage, so nothing is shared.
	requireNoOverlaps(t, overlapsFor(t, []*Arena{&a}, nil, &sc))

	// A shallow reuse of the scanner's child array is.
	borrowed := a.NewArray()
	borrowed.a = scanned.a
	found := overlapsFor(t, []*Arena{&a}, nil, &sc)
	requireOverlap(t, found, scanned, borrowed, StorageChildren)

	// Without the scanner in the walk the same shape is invisible, which is
	// what the parameter buys.
	if len(overlapsFor(t, []*Arena{&a}, nil)) != 0 {
		t.Fatal("expected the scanner side to be unreachable when it is not passed")
	}
}
