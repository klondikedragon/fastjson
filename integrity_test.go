package fastjson

import (
	"errors"
	"testing"
	"unsafe"
)

// copyValueStruct performs the struct assignment that callers must not make,
// so the containment paths can be exercised. It goes through unsafe so that
// the copylocks analysis does not reject the test itself.
func copyValueStruct(dst, src *Value) {
	*(*[unsafe.Sizeof(Value{})]byte)(unsafe.Pointer(dst)) =
		*(*[unsafe.Sizeof(Value{})]byte)(unsafe.Pointer(src))
}

// recoverIntegrity runs f and returns the *IntegrityError it panicked with, or
// nil if it returned normally. Any other panic is re-raised.
func recoverIntegrity(t *testing.T, f func()) (err *IntegrityError) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		ie, ok := r.(*IntegrityError)
		if !ok {
			panic(r)
		}
		err = ie
	}()
	f()
	return nil
}

func withStrictIntegrity(t *testing.T, strict bool) {
	t.Helper()
	SetStrictIntegrity(strict)
	t.Cleanup(func() { SetStrictIntegrity(false) })
}

func TestIntegritySlotsAreSelfOwned(t *testing.T) {
	var p Parser
	if _, err := p.Parse(bigDocument(2000)); err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	if n := chunkCount(&p.c); n < 2 {
		t.Fatalf("expected multiple chunks; got %d", n)
	}
	for i, v := range liveSlots(&p.c) {
		if v.self != v {
			t.Fatalf("parser slot %d does not own itself: self=%p slot=%p", i, v.self, v)
		}
		if v.o.self != v {
			t.Fatalf("parser slot %d has an object owned by %p, want %p", i, v.o.self, v)
		}
	}

	var a Arena
	root := a.NewArray()
	for i := 0; i < 2000; i++ {
		o := a.NewObject()
		o.Set("k", a.NewNumberInt(i))
		root.SetArrayItem(i, o)
	}
	if n := chunkCount(&a.c); n < 2 {
		t.Fatalf("expected multiple chunks; got %d", n)
	}
	for i, v := range liveSlots(&a.c) {
		if v.self != v {
			t.Fatalf("arena slot %d does not own itself: self=%p slot=%p", i, v.self, v)
		}
		if v.o.self != v {
			t.Fatalf("arena slot %d has an object owned by %p, want %p", i, v.o.self, v)
		}
	}
}

func TestIntegrityCopyThenMutatePanics(t *testing.T) {
	var a Arena
	donor := a.NewObject()
	donor.Set("k", a.NewString("v"))
	target := a.NewObject()
	copyValueStruct(target, donor)

	cases := []struct {
		name string
		f    func()
	}{
		{"Value.Set", func() { target.Set("x", a.NewNumberInt(1)) }},
		{"Value.Del", func() { target.Del("k") }},
		{"Object.Set", func() { target.o.Set("x", a.NewNumberInt(1)) }},
		{"Object.Del", func() { target.o.Del("k") }},
		{"Object.DelMany", func() { target.o.DelMany(map[string]struct{}{"k": {}}) }},
		{"Object.Reset", func() { target.o.Reset() }},
		{"Value.Get", func() { target.Get("k") }},
		{"Value.GetObject", func() { target.GetObject() }},
		{"Object.Get", func() { target.o.Get("k") }},
		{"Object.Visit", func() { target.o.Visit(func([]byte, *Value) {}) }},
		{"Object.ReverseVisit", func() { target.o.ReverseVisit(func([]byte, *Value) bool { return true }) }},
		{"Value.MarshalTo", func() { target.MarshalTo(nil) }},
		{"Object.MarshalTo", func() { target.o.MarshalTo(nil) }},
		{"Value.Type", func() { target.Type() }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := recoverIntegrity(t, c.f)
			if err == nil {
				t.Fatalf("%s did not report an integrity violation", c.name)
			}
			var ie *IntegrityError
			if !errors.As(error(err), &ie) {
				t.Fatalf("panic value is not *IntegrityError: %#v", err)
			}
			if ie.Site == "" {
				t.Fatal("integrity error carries no site")
			}
		})
	}
}

func TestIntegrityCopiedArrayMutatorsPanic(t *testing.T) {
	var a Arena
	donor := a.NewArray()
	donor.SetArrayItem(0, a.NewNumberInt(1))
	target := a.NewArray()
	copyValueStruct(target, donor)

	for _, c := range []struct {
		name string
		f    func()
	}{
		{"Value.SetArrayItem", func() { target.SetArrayItem(1, a.NewNumberInt(2)) }},
		{"Value.SetArrayLength", func() { target.SetArrayLength(0) }},
		{"Value.GetArray", func() { target.GetArray() }},
		{"Value.Array", func() { _, _ = target.Array() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := recoverIntegrity(t, c.f); err == nil {
				t.Fatalf("%s did not report an integrity violation", c.name)
			}
		})
	}
}

func TestIntegrityCopyThenRecycleDefaultMode(t *testing.T) {
	var donor, recipient Arena
	d := donor.NewObject()
	d.Set("k", donor.NewString("v"))
	r := recipient.NewObject()
	copyValueStruct(r, d)

	before := IntegrityHealCount()
	recipient.Reset()
	if got := IntegrityHealCount(); got <= before {
		t.Fatalf("reset did not record a contained slot: count %d -> %d", before, got)
	}
	if r.o.kvs != nil || r.a != nil {
		t.Fatal("reset kept the foreign headers instead of dropping them")
	}
	if r.self != nil {
		t.Fatal("reset left the contained slot owned")
	}

	// The donor graph is untouched by the containment.
	if got := d.String(); got != `{"k":"v"}` {
		t.Fatalf("donor graph changed: %s", got)
	}

	// The slot comes back with fresh headers.
	v := recipient.NewObject()
	if v != r {
		t.Skip("allocator did not hand back the same slot")
	}
	if cap(v.o.kvs) != 0 || cap(v.a) != 0 {
		t.Fatalf("recycled slot kept storage: members cap %d, children cap %d", cap(v.o.kvs), cap(v.a))
	}
	if v.self != v || v.o.self != v {
		t.Fatal("recycled slot is not self owned")
	}
}

func TestIntegrityCopyThenRecycleStrictMode(t *testing.T) {
	withStrictIntegrity(t, true)

	var donor, recipient Arena
	d := donor.NewObject()
	d.Set("k", donor.NewString("v"))
	r := recipient.NewObject()
	copyValueStruct(r, d)

	err := recoverIntegrity(t, func() { recipient.Reset() })
	if err == nil {
		t.Fatal("strict mode did not report the contained slot at reset")
	}
	if err.Site != "Value.reset" {
		t.Fatalf("unexpected site: %s", err.Site)
	}
}

// buildDonorRecipient builds one graph per arena and copies a node of the
// donor graph over a node of the recipient graph by struct assignment, the way
// a caller must not. It returns the root of each graph and the copied node.
func buildDonorRecipient(donor, recipient *Arena) (donorRoot, recipientRoot, copied *Value) {
	donorRoot = donor.NewObject()
	inner := donor.NewObject()
	inner.Set("donor", donor.NewString("payload"))
	donorRoot.Set("inner", inner)

	recipientRoot = buildRecipientGraph(recipient)
	copied = recipientRoot.Get("inner")
	copyValueStruct(copied, inner)
	return donorRoot, recipientRoot, copied
}

func buildRecipientGraph(a *Arena) *Value {
	root := a.NewObject()
	inner := a.NewObject()
	inner.Set("recipient", a.NewString("payload"))
	root.Set("inner", inner)
	return root
}

// churn forces the arena to hand out and recycle many slots.
func churn(a *Arena) {
	root := a.NewArray()
	for i := 0; i < 1000; i++ {
		o := a.NewObject()
		o.Set("k", a.NewNumberInt(i))
		root.SetArrayItem(i, o)
	}
}

func TestIntegrityCrossAllocatorResetOrders(t *testing.T) {
	t.Run("donor reset first", func(t *testing.T) {
		var donor, recipient Arena
		_, recipientRoot, _ := buildDonorRecipient(&donor, &recipient)

		donor.Reset()
		churn(&donor)

		// The surviving graph holds a node that does not own its slot, so
		// marshalling it reports rather than emitting storage the donor
		// arena has since reused.
		err := recoverIntegrity(t, func() { _ = recipientRoot.MarshalTo(nil) })
		if err == nil {
			t.Fatal("marshalling the surviving graph did not report the copied node")
		}
		if err.Site != "Value.MarshalTo" {
			t.Fatalf("unexpected site: %s", err.Site)
		}
	})

	t.Run("recipient reset first", func(t *testing.T) {
		var donor, recipient Arena
		donorRoot, _, _ := buildDonorRecipient(&donor, &recipient)

		before := IntegrityHealCount()
		recipient.Reset()
		churn(&recipient)
		if IntegrityHealCount() <= before {
			t.Fatal("resetting the recipient did not contain the copied node")
		}

		// The recipient dropped the shared headers instead of clearing
		// through them, so the surviving donor graph is intact and its
		// bytes are its own.
		got := string(donorRoot.MarshalTo(nil))
		want := `{"inner":{"donor":"payload"}}`
		if got != want {
			t.Fatalf("surviving graph marshalled as %s; want %s", got, want)
		}
	})
}

func TestSetStringValue(t *testing.T) {
	var a Arena
	root := a.NewObject()
	v := a.NewString("first")
	root.Set("k", v)

	v.SetStringValue(&a, `second "quoted"`)
	if got := string(root.MarshalTo(nil)); got != `{"k":"second \"quoted\""}` {
		t.Fatalf("unexpected json: %s", got)
	}
	if v.Type() != TypeString {
		t.Fatalf("unexpected type: %s", v.Type())
	}
}

func TestSetStringValueOnFormerObject(t *testing.T) {
	var a Arena
	v := a.NewObject()
	v.Set("a", a.NewNumberInt(1))
	v.Set("b", a.NewNumberInt(2))
	members := v.o.kvs

	v.SetStringValue(&a, "now a string")

	if got := string(v.MarshalTo(nil)); got != `"now a string"` {
		t.Fatalf("unexpected json: %s", got)
	}
	// The node keeps its own member storage: nothing was handed to, or taken
	// from, another value.
	if &v.o.kvs[0] != &members[0] || len(v.o.kvs) != len(members) {
		t.Fatal("member storage changed identity")
	}
	if v.o.self != v || v.self != v {
		t.Fatal("node stopped owning its own storage")
	}

	// Turning it back into an object reuses that same storage.
	v.o.Reset()
	v.t = TypeObject
	v.Set("c", a.NewNumberInt(3))
	if got := string(v.MarshalTo(nil)); got != `{"c":3}` {
		t.Fatalf("unexpected json after reuse: %s", got)
	}
	if &v.o.kvs[0] != &members[0] {
		t.Fatal("member storage was reallocated")
	}
}

func TestSetStringValueChecksOwnership(t *testing.T) {
	var a Arena
	donor := a.NewString("donor")
	target := a.NewObject()
	copyValueStruct(target, donor)

	err := recoverIntegrity(t, func() { target.SetStringValue(&a, "x") })
	if err == nil {
		t.Fatal("SetStringValue did not report the copied node")
	}
	if err.Site != "Value.SetStringValue" {
		t.Fatalf("unexpected site: %s", err.Site)
	}
}

func TestNoCopyIsZeroSized(t *testing.T) {
	if got := unsafe.Sizeof(noCopy{}); got != 0 {
		t.Fatalf("noCopy occupies %d bytes; the copy guard must be free", got)
	}
	// Placed first in Value, so it adds no padding either.
	if unsafe.Offsetof(Value{}.self) != 0 {
		t.Fatal("noCopy shifted the first real field of Value")
	}
}
