package fastjson

import (
	"strconv"
	"strings"
)

// Del deletes the entry with the given key from o.
func (o *Object) Del(key string) {
	if o == nil {
		return
	}
	o.checkIntegrity("Object.Del")
	if !o.keysUnescaped && strings.IndexByte(key, '\\') < 0 {
		// Fast path - try searching for the key without object keys unescaping.
		for i, kv := range o.kvs {
			if kv.k == key {
				o.delMemberAt(i)
				return
			}
		}
	}

	// Slow path - unescape object keys before item search.
	o.unescapeKeys()

	for i, kv := range o.kvs {
		if kv.k == key {
			o.delMemberAt(i)
			return
		}
	}
}

// delMemberAt removes the member at i and clears the entry the tail vacated.
//
// Shortening the slice alone would leave the last member reachable inside the
// retained capacity, where a later reset, which clears only up to len, would
// never reach it.
func (o *Object) delMemberAt(i int) {
	kvs := o.kvs
	copy(kvs[i:], kvs[i+1:])
	kvs[len(kvs)-1] = kv{}
	o.kvs = kvs[:len(kvs)-1]
	o.dropIndex()
}

// DelMany deletes all entries from o who are present in the given keysToDelete set.
// Returns the number of keys that were deleted
func (o *Object) DelMany(keysToDelete map[string]struct{}) int {
	if o == nil {
		return 0
	}
	o.checkIntegrity("Object.DelMany")
	o.unescapeKeys()
	numDeleted := 0
	nextUsedIndex := 0
	for _, kv := range o.kvs {
		if _, exists := keysToDelete[kv.k]; !exists {
			o.kvs[nextUsedIndex] = kv
			nextUsedIndex++
		} else {
			numDeleted++
		}
	}
	// Clear what the surviving members were compacted over, so no deleted
	// member is left reachable inside the retained capacity.
	clear(o.kvs[nextUsedIndex:])
	o.kvs = o.kvs[:nextUsedIndex]
	if numDeleted > 0 {
		o.dropIndex()
	}
	return numDeleted
}

// Del deletes the entry with the given key from array or object v.
func (v *Value) Del(key string) {
	if v == nil {
		return
	}
	v.checkIntegrity("Value.Del")
	if v.t == TypeObject {
		v.o.Del(key)
		return
	}
	if v.t == TypeArray {
		n, err := strconv.Atoi(key)
		if err != nil || n < 0 || n >= len(v.a) {
			return
		}
		// The tail moves down by one; clear the child the move vacated.
		a := v.a
		copy(a[n:], a[n+1:])
		a[len(a)-1] = nil
		v.a = a[:len(a)-1]
	}
}

// Set sets (key, value) entry in the o.
//
// The value must be unchanged during o lifetime.
func (o *Object) Set(key string, value *Value) {
	if o == nil {
		return
	}
	o.checkIntegrity("Object.Set")
	if value == nil {
		value = valueNull
	}
	o.unescapeKeys()

	// Try substituting already existing entry with the given key.
	if i := o.indexOf(key); i >= 0 {
		o.kvs[i].v = value
		return
	}

	// Add new entry.
	kv := o.getKV()
	kv.k = key
	kv.v = value
	if o.index != nil {
		o.index[key] = len(o.kvs) - 1
	}
}

// Set sets (key, value) entry in the array or object v.
//
// The value must be unchanged during v lifetime.
func (v *Value) Set(key string, value *Value) {
	if v == nil {
		return
	}
	v.checkIntegrity("Value.Set")
	if v.t == TypeObject {
		v.o.Set(key, value)
		return
	}
	if v.t == TypeArray {
		idx, err := strconv.Atoi(key)
		if err != nil || idx < 0 {
			return
		}
		v.SetArrayItem(idx, value)
	}
}

// SetArrayItem sets the value in the array v at idx position.
//
// The value must be unchanged during v lifetime.
func (v *Value) SetArrayItem(idx int, value *Value) {
	if v == nil || v.t != TypeArray {
		return
	}
	v.checkIntegrity("Value.SetArrayItem")
	for idx >= len(v.a) {
		v.a = append(v.a, valueNull)
	}
	if value == nil {
		// Match Object.Set: a nil value is stored as JSON null so that
		// readers and MarshalTo never meet a nil element.
		value = valueNull
	}
	v.a[idx] = value
}

// SetArrayLength lengthens or shortens (cuts off the end) of the
// array v to the given length.
func (v *Value) SetArrayLength(length int) {
	if v == nil || v.t != TypeArray {
		return
	}
	v.checkIntegrity("Value.SetArrayLength")
	for length > len(v.a) {
		v.a = append(v.a, valueNull)
	}
	if len(v.a) > length {
		// Clear the children being cut off, so they are not left
		// reachable inside the retained capacity.
		clear(v.a[length:])
		v.a = v.a[:length]
	}
}

// SetStringValue turns v into a JSON string holding s, in place.
//
// This is the supported way to change an existing value into a string: it
// writes only the scalar and the type, leaving v's own member and child
// storage where it is. Assigning a whole Value over another, as in
// *v = *a.NewString(s), instead copies the source's member and child headers
// into v, after which two values name one backing array; the library reports
// that as *IntegrityError.
//
// s is escaped into the buffer of a, so the resulting value is valid until
// Reset is called on a. It does not need to outlive this call.
func (v *Value) SetStringValue(a *Arena, s string) {
	if v == nil {
		return
	}
	v.checkIntegrity("Value.SetStringValue")
	v.s = a.allocateEscapedString(s)
	v.t = typeRawString
}
