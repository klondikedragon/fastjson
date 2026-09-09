package fastjson

// AuditStorage is a test and diagnostic helper that lives in the exported API
// because it must read the chunk lists inside an Arena, a Parser and a
// Scanner, which are package private. An export_test.go hook was the other
// candidate home; it would not reach the tests of packages that build on this
// one, which is where the shapes it looks for actually appear.

import (
	"fmt"
	"sort"
	"unsafe"
)

// Field names reported by StorageRange.
const (
	// StorageMembers names the backing array behind an object's members.
	StorageMembers = "members"

	// StorageChildren names the backing array behind an array's children.
	StorageChildren = "children"
)

// StorageRange describes the backing memory that one slot's member or child
// slice names, from the start of the array to the end of its capacity.
type StorageRange struct {
	// Value is the slot that names the storage.
	Value *Value

	// Field is StorageMembers or StorageChildren.
	Field string

	// Start and End bound the array, End exclusive. The range covers the
	// whole capacity, not just the elements in use.
	Start uintptr
	End   uintptr

	// Len and Cap are the slice header of the slot at the time of the walk.
	Len int
	Cap int
}

func (r StorageRange) String() string {
	return fmt.Sprintf("%s of %p [%#x,%#x) len %d cap %d", r.Field, r.Value, r.Start, r.End, r.Len, r.Cap)
}

// StorageOverlap reports two distinct slots whose backing storage intersects.
//
// Two slots sharing one array is never correct: a write through either reaches
// both, and whichever slot is recycled first releases storage the other still
// uses.
type StorageOverlap struct {
	A StorageRange
	B StorageRange
}

func (o StorageOverlap) String() string {
	return fmt.Sprintf("%s overlaps %s", o.A, o.B)
}

// AuditStorage walks every value slot held by the given arenas, parsers and
// scanners and reports every pair of distinct slots whose member or child
// storage shares backing memory.
//
// The walk covers every chunk of every allocator, every slot a chunk retains
// beyond the ones currently handed out, and both the member and the child
// field of each slot regardless of its type, so a header left dormant on a
// scalar value is inspected like any other. Ranges cover the whole capacity of
// each array, so storage retained past the end of a slice is compared too.
// Each slot is visited once, so a value referenced from several places in a
// document produces one entry and never reports against itself.
//
// This is a test and diagnostic helper. It allocates, it is quadratic in the
// number of overlapping ranges, and it must not be called on a hot path.
func AuditStorage(arenas []*Arena, parsers []*Parser, scanners []*Scanner) []StorageOverlap {
	seen := make(map[*Value]struct{})
	var ranges []StorageRange

	visit := func(v *Value) {
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		if c := cap(v.o.kvs); c > 0 {
			kvs := v.o.kvs[:c]
			start := uintptr(unsafe.Pointer(&kvs[0]))
			ranges = append(ranges, StorageRange{
				Value: v,
				Field: StorageMembers,
				Start: start,
				End:   start + uintptr(c)*unsafe.Sizeof(kv{}),
				Len:   len(v.o.kvs),
				Cap:   c,
			})
		}
		if c := cap(v.a); c > 0 {
			a := v.a[:c]
			start := uintptr(unsafe.Pointer(&a[0]))
			ranges = append(ranges, StorageRange{
				Value: v,
				Field: StorageChildren,
				Start: start,
				End:   start + uintptr(c)*unsafe.Sizeof((*Value)(nil)),
				Len:   len(v.a),
				Cap:   c,
			})
		}
	}

	for _, a := range arenas {
		if a != nil {
			forEachSlot(&a.c, true, visit)
		}
	}
	for _, p := range parsers {
		if p != nil {
			forEachSlot(&p.c, true, visit)
		}
	}
	for _, sc := range scanners {
		if sc != nil {
			forEachSlot(&sc.c, true, visit)
		}
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})

	var out []StorageOverlap
	for i := range ranges {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[j].Start >= ranges[i].End {
				// Ranges are ordered by start, so nothing further can
				// reach back into this one.
				break
			}
			if ranges[i].Value == ranges[j].Value {
				// One slot naming its own storage twice is not a
				// sharing defect.
				continue
			}
			out = append(out, StorageOverlap{A: ranges[i], B: ranges[j]})
		}
	}
	return out
}
