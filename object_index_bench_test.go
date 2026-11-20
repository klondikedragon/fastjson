package fastjson

import (
	"strconv"
	"testing"
)

// The lookup patterns itl-services uses on objects, at each size, with the index forced off
// (never built) and forced on (built on the first search):
//   - lookups: a parsed object read a handful of times (8 Gets, half of them misses), as the
//     decoder reads a parsed event's standard fields;
//   - build: an object built from empty one distinct key at a time with a Get then a Set, as
//     AutoExtract merges extracted fields;
//   - mixed: a parsed object of n keys that then gains n/2 more the same way, with the 8 reads.
var benchObjectIndexSizes = []int{8, 16, 32, 64, 128, 256, 512, 1024, 4096}

func benchObjectIndexKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "field_" + strconv.Itoa(i)
	}
	return keys
}

func benchParsedObject(b *testing.B, keys []string) string {
	b.Helper()
	src := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			src = append(src, ',')
		}
		src = append(src, '"')
		src = append(src, k...)
		src = append(src, `":1`...)
	}
	return string(append(src, '}'))
}

func withObjectIndex(on bool, run func()) {
	savedLen, savedScans := objectIndexMinLen, objectIndexScans
	if on {
		objectIndexMinLen, objectIndexScans = 0, 0
	} else {
		objectIndexMinLen = 1 << 30
	}
	defer func() { objectIndexMinLen, objectIndexScans = savedLen, savedScans }()
	run()
}

func benchReads(o *Object, keys []string) {
	for i := 0; i < 4; i++ {
		_ = o.Get(keys[(i*7919)%len(keys)])
		_ = o.Get("absent_" + strconv.Itoa(i))
	}
}

func BenchmarkObjectIndex(b *testing.B) {
	for _, n := range benchObjectIndexSizes {
		keys := benchObjectIndexKeys(n)
		extra := benchObjectIndexKeys(n + n/2)[n:]
		src := benchParsedObject(b, keys)
		for _, on := range []bool{false, true} {
			mode := "off"
			if on {
				mode = "on"
			}
			b.Run("lookups/"+strconv.Itoa(n)+"/"+mode, func(b *testing.B) {
				withObjectIndex(on, func() {
					var p Parser
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						v, _ := p.Parse(src)
						o := v.GetObject()
						o.unescapeKeys()
						benchReads(o, keys)
					}
				})
			})
			b.Run("build/"+strconv.Itoa(n)+"/"+mode, func(b *testing.B) {
				withObjectIndex(on, func() {
					var a Arena
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						a.Reset()
						o := a.NewObject().GetObject()
						for j, k := range keys {
							if o.Get(k) == nil {
								o.Set(k, a.NewNumberInt(j))
							}
						}
					}
				})
			})
			b.Run("mixed/"+strconv.Itoa(n)+"/"+mode, func(b *testing.B) {
				withObjectIndex(on, func() {
					var p Parser
					var a Arena
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						a.Reset()
						v, _ := p.Parse(src)
						o := v.GetObject()
						benchReads(o, keys)
						for j, k := range extra {
							if o.Get(k) == nil {
								o.Set(k, a.NewNumberInt(j))
							}
						}
						benchReads(o, keys)
					}
				})
			})
		}
	}
}
