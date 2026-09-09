package fastjson

import (
	"unsafe"
)

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func s2b(s string) (b []byte) {
	// Alias the string's backing array as a byte slice without allocating.
	// Writing through the result is only correct where the caller knows the
	// string points into a buffer it owns, which is how key and value
	// unescaping rewrites the parser working copy in place. For any string
	// that came from outside the package the result is read only.
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

const maxStartEndStringLen = 80

func startEndString(s string) string {
	if len(s) <= maxStartEndStringLen {
		return s
	}
	start := s[:40]
	end := s[len(s)-40:]
	return start + "..." + end
}
