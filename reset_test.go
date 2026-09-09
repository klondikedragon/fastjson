package fastjson

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// allSlots returns a pointer to every slot in every chunk reachable from c,
// including slots past len that the chunk retains for reuse.
func allSlots(c *cache) []*Value {
	var out []*Value
	forEachSlot(c, true, func(v *Value) { out = append(out, v) })
	return out
}

// liveSlots returns a pointer to every slot handed out so far by c.
func liveSlots(c *cache) []*Value {
	var out []*Value
	forEachSlot(c, false, func(v *Value) { out = append(out, v) })
	return out
}

func chunkCount(c *cache) int {
	n := 0
	for ch := c; ch != nil; ch = ch.nx {
		n++
	}
	return n
}

func describeNonEmpty(v *Value) string {
	if v.t != 0 || v.s != "" || len(v.o.kvs) != 0 || len(v.a) != 0 || v.o.keysUnescaped {
		return fmt.Sprintf("t=%d s=%q members=%d children=%d keysUnescaped=%v",
			v.t, v.s, len(v.o.kvs), len(v.a), v.o.keysUnescaped)
	}
	return ""
}

// bigDocument builds JSON large enough to spill the cache past its first chunk.
func bigDocument(items int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < items; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"k%d":"v%d","n":%d,"a":[1,2,3]}`, i, i, i)
	}
	sb.WriteByte(']')
	return sb.String()
}

func TestParserResetClearsEveryChunk(t *testing.T) {
	var p Parser
	if _, err := p.Parse(bigDocument(2000)); err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	if n := chunkCount(&p.c); n < 2 {
		t.Fatalf("expected the document to spill into multiple chunks; got %d", n)
	}
	handed := liveSlots(&p.c)
	if len(handed) < preAllocatedCacheSize {
		t.Fatalf("expected many slots to be handed out; got %d", len(handed))
	}

	p.c.reset()

	for i, v := range handed {
		if d := describeNonEmpty(v); d != "" {
			t.Fatalf("slot %d is not empty after reset: %s", i, d)
		}
	}

	// Every slot the cache can hand out next must also come back empty,
	// including slots in chunks that are reused rather than allocated.
	for i, v := range allSlots(&p.c) {
		if d := describeNonEmpty(v); d != "" {
			t.Fatalf("retained slot %d is not empty after reset: %s", i, d)
		}
	}
	for i := 0; i < len(handed); i++ {
		v := p.c.getValue()
		if d := describeNonEmpty(v); d != "" {
			t.Fatalf("recycled slot %d is not empty: %s", i, d)
		}
	}
}

func TestArenaResetClearsEveryChunk(t *testing.T) {
	var a Arena
	root := a.NewArray()
	for i := 0; i < 2000; i++ {
		o := a.NewObject()
		o.Set(fmt.Sprintf("k%d", i), a.NewString(fmt.Sprintf("v%d", i)))
		o.Set("n", a.NewNumberInt(i))
		root.SetArrayItem(i, o)
	}
	if n := chunkCount(&a.c); n < 2 {
		t.Fatalf("expected the arena to spill into multiple chunks; got %d", n)
	}
	handed := liveSlots(&a.c)

	a.Reset()

	for i, v := range handed {
		if d := describeNonEmpty(v); d != "" {
			t.Fatalf("slot %d is not empty after reset: %s", i, d)
		}
	}
	for i := 0; i < len(handed); i++ {
		v := a.c.getValue()
		if d := describeNonEmpty(v); d != "" {
			t.Fatalf("recycled slot %d is not empty: %s", i, d)
		}
	}
}

// within reports whether s aliases the byte range of buf.
func within(s string, buf []byte) bool {
	if len(s) == 0 || len(buf) == 0 {
		return false
	}
	sp := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	bp := uintptr(unsafe.Pointer(&buf[0]))
	return sp >= bp && sp < bp+uintptr(len(buf))
}

func TestResetDropsExternalByteSliceReferences(t *testing.T) {
	// A body the caller owns. Values that alias it must not survive a reset,
	// otherwise the cache keeps the whole body reachable.
	body := make([]byte, 1<<20)
	for i := range body {
		body[i] = 'a' + byte(i%26)
	}

	t.Run("arena", func(t *testing.T) {
		var a Arena
		o := a.NewObject()
		for i := 0; i < 500; i++ {
			key := b2s(body[i*64 : i*64+32])
			o.Set(key, a.NewNumberString(b2s(body[i*64+32:i*64+48])))
		}
		handed := liveSlots(&a.c)

		aliasing := 0
		for _, v := range handed {
			if within(v.s, body) {
				aliasing++
			}
			for _, kv := range v.o.kvs {
				if within(kv.k, body) {
					aliasing++
				}
			}
		}
		if aliasing == 0 {
			t.Fatal("expected the arena values to alias the caller body before reset")
		}

		a.Reset()

		for i, v := range handed {
			if within(v.s, body) {
				t.Fatalf("slot %d still aliases the caller body via its scalar", i)
			}
			for _, kv := range v.o.kvs[:cap(v.o.kvs)] {
				if within(kv.k, body) {
					t.Fatalf("slot %d still aliases the caller body via a member key", i)
				}
			}
		}
	})

	t.Run("parser", func(t *testing.T) {
		var p Parser
		doc := bigDocument(2000)
		if _, err := p.Parse(doc); err != nil {
			t.Fatalf("cannot parse: %s", err)
		}
		// Parsed scalars alias the parser's own working copy.
		handed := liveSlots(&p.c)
		aliasing := 0
		for _, v := range handed {
			if within(v.s, p.b) {
				aliasing++
			}
		}
		if aliasing == 0 {
			t.Fatal("expected parsed values to alias the parser working copy")
		}

		p.c.reset()

		for i, v := range handed {
			if within(v.s, p.b) {
				t.Fatalf("slot %d still aliases the parser working copy", i)
			}
			for _, kv := range v.o.kvs[:cap(v.o.kvs)] {
				if within(kv.k, p.b) {
					t.Fatalf("slot %d still aliases the parser working copy via a member key", i)
				}
			}
		}
	})
}

// assertNoRetainedReferences checks that no slot in any chunk of c holds a
// member or a child anywhere inside its capacity. After a reset every slice is
// empty, so anything still reachable there was left behind by a site that
// shortened a slice without clearing what it dropped.
func assertNoRetainedReferences(t *testing.T, c *cache) {
	t.Helper()
	var zero kv
	for i, v := range allSlots(c) {
		for j, m := range v.o.kvs[:cap(v.o.kvs)] {
			if m != zero {
				t.Fatalf("slot %d retains member %d after reset: key %q value %p", i, j, m.k, m.v)
			}
		}
		for j, child := range v.a[:cap(v.a)] {
			if child != nil {
				t.Fatalf("slot %d retains child %d after reset: %p", i, j, child)
			}
		}
	}
}

func TestResetAfterDeletionsRetainsNothing(t *testing.T) {
	// A value from another allocator, so a reference left inside retained
	// capacity would hold a whole second allocator reachable.
	var other Arena
	lent := other.NewString("lent")

	t.Run("arena", func(t *testing.T) {
		var a Arena
		o := a.NewObject()
		for i := 0; i < 32; i++ {
			o.Set(fmt.Sprintf("k%d", i), a.NewNumberInt(i))
		}
		o.Set("lent", lent)
		o.Del("lent")
		o.Del("k0")
		o.GetObject().DelMany(map[string]struct{}{"k1": {}, "k2": {}, "k3": {}})

		arr := a.NewArray()
		for i := 0; i < 32; i++ {
			arr.SetArrayItem(i, a.NewNumberInt(i))
		}
		arr.SetArrayItem(8, lent)
		arr.Del("8")
		arr.SetArrayLength(4)

		a.Reset()
		assertNoRetainedReferences(t, &a.c)
	})

	t.Run("parser", func(t *testing.T) {
		var p Parser
		v, err := p.Parse(bigDocument(200))
		if err != nil {
			t.Fatalf("cannot parse: %s", err)
		}
		for i := 0; i < 20; i++ {
			item := v.Get(strconv.Itoa(i))
			if item == nil {
				t.Fatalf("missing item %d", i)
			}
			item.Set("lent", lent)
			item.Del("lent")
			item.Del("n")
			item.GetObject().DelMany(map[string]struct{}{"a": {}})
			arr := v.Get(strconv.Itoa(i+20), "a")
			if arr != nil {
				arr.SetArrayItem(1, lent)
				arr.Del("1")
				arr.SetArrayLength(1)
			}
		}
		v.SetArrayLength(10)

		p.c.reset()
		assertNoRetainedReferences(t, &p.c)
	})
}
