package fastjson

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

// linearObject is the object search as it was before the index: first member whose key matches,
// replaced in place by Set, appended otherwise. The differential test holds the indexed Object
// to it.
type linearObject struct {
	keys []string
	vals []string
}

func (l *linearObject) get(key string) (string, bool) {
	for i, k := range l.keys {
		if k == key {
			return l.vals[i], true
		}
	}
	return "", false
}

func (l *linearObject) set(key, val string) {
	for i, k := range l.keys {
		if k == key {
			l.vals[i] = val
			return
		}
	}
	l.keys = append(l.keys, key)
	l.vals = append(l.vals, val)
}

func (l *linearObject) del(key string) {
	for i, k := range l.keys {
		if k == key {
			l.keys = append(l.keys[:i], l.keys[i+1:]...)
			l.vals = append(l.vals[:i], l.vals[i+1:]...)
			return
		}
	}
}

func (l *linearObject) marshal() string {
	var a Arena
	v := a.NewObject()
	o := v.GetObject()
	for i := range l.keys {
		// Appended directly: a model with duplicate keys (from a parsed object) must keep them.
		kv := o.getKV()
		kv.k, kv.v = l.keys[i], a.NewString(l.vals[i])
	}
	o.keysUnescaped = true
	return string(o.MarshalTo(nil))
}

// Random Get, Set, Del, DelMany and Reset sequences over objects large enough to build the index,
// starting from parsed objects that carry duplicate and escaped keys, give the same answers and
// the same marshalled bytes, member order included, as the linear search.
func TestObjectIndexMatchesLinearSearch(t *testing.T) {
	// A low minimum makes the index appear at the sizes this test reaches; the logic does not
	// depend on the value.
	defer func(saved int) { objectIndexMinLen = saved }(objectIndexMinLen)
	objectIndexMinLen = 32
	indexed := 0
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var a Arena
		keyCount := 20 + rng.Intn(200)
		key := func() string {
			switch rng.Intn(10) {
			case 0:
				return "K" + strconv.Itoa(rng.Intn(keyCount)) // differs from the k keys only by case
			case 1:
				return "dup"
			default:
				return "k" + strconv.Itoa(rng.Intn(keyCount))
			}
		}
		// A parsed start with a duplicate key and an escaped one.
		src := `{"dup":"first","ab":"escaped","dup":"second","ab":"plain"}`
		v, err := ParseBytes([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		o := v.GetObject()
		o.unescapeKeys()
		model := &linearObject{keys: []string{"dup", "ab", "dup", "ab"}, vals: []string{"first", "escaped", "second", "plain"}}
		for step := 0; step < 3000; step++ {
			k := key()
			switch op := rng.Intn(20); {
			case op < 10:
				val := fmt.Sprintf("v%d", step)
				o.Set(k, a.NewString(val))
				model.set(k, val)
			case op < 16:
				got := o.Get(k)
				want, ok := model.get(k)
				if (got != nil) != ok || (ok && string(got.GetStringBytes()) != want) {
					t.Fatalf("seed %d step %d: Get(%q) = %v, want %q (present %v)", seed, step, k, got, want, ok)
				}
			case op < 19:
				o.Del(k)
				model.del(k)
			default:
				if rng.Intn(10) == 0 {
					drop := map[string]struct{}{k: {}, key(): {}}
					o.DelMany(drop)
					for d := range drop {
						for {
							if _, ok := model.get(d); !ok {
								break
							}
							model.del(d)
						}
					}
				}
			}
			if o.index != nil {
				indexed++
			}
			if got, want := string(o.MarshalTo(nil)), model.marshal(); got != want {
				t.Fatalf("seed %d step %d: marshalled\n got  %s\n want %s", seed, step, got, want)
			}
		}
	}
	if indexed == 0 {
		t.Fatal("no step ran with an index: the test compares nothing new")
	}
	t.Logf("%d of %d steps ran with an index", indexed, 40*3000)
}

// An object built one distinct key at a time, each Set preceded by a Get, does linear work: past
// objectIndexScans lengths of scanning it answers from its index, and its scan count stays
// within that bound.
func TestObjectIndexBoundsBuildUp(t *testing.T) {
	var a Arena
	o := a.NewObject().GetObject()
	const n = 20000
	for i := 0; i < n; i++ {
		k := "k" + strconv.Itoa(i)
		if o.Get(k) != nil {
			t.Fatalf("%s present before Set", k)
		}
		o.Set(k, a.NewNumberInt(i))
	}
	if o.index == nil || len(o.index) != n {
		t.Fatalf("index has %d keys, want %d", len(o.index), n)
	}
	if limit := uint64(objectIndexScans) * n; uint64(o.scanned) > limit {
		t.Fatalf("scanned %d members, over %d", o.scanned, limit)
	}
	// A parsed object searched only a few times never builds one.
	src := "{"
	for i := 0; i < 4*objectIndexMinLen; i++ {
		src += `"k` + strconv.Itoa(i) + `":1,`
	}
	parsed := MustParse(src[:len(src)-1] + "}").GetObject()
	for i := 0; i < objectIndexScans-1; i++ {
		_ = parsed.Get("absent")
	}
	if parsed.index != nil {
		t.Fatal("an object searched fewer than objectIndexScans times its length must not build an index")
	}
	_ = parsed.Get("absent")
	_ = parsed.Get("absent")
	if parsed.index == nil {
		t.Fatal("past objectIndexScans lengths of scanning the object must build its index")
	}
}

// SetObjectIndexMinLen moves the size from which objects index: above an object's size, the
// same build-up that indexes at the default never does.
func TestSetObjectIndexMinLen(t *testing.T) {
	defer SetObjectIndexMinLen(objectIndexMinLen)
	SetObjectIndexMinLen(1 << 20)
	var a Arena
	o := a.NewObject().GetObject()
	for i := 0; i < 2000; i++ {
		k := "k" + strconv.Itoa(i)
		_ = o.Get(k)
		o.Set(k, a.NewNull())
	}
	if o.index != nil {
		t.Fatal("an object below the minimum length must not build an index")
	}
}
