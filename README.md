# fastjson

[![GoDoc Widget]][GoDoc]

[GoDoc]: https://godoc.org/github.com/aperturerobotics/fastjson
[GoDoc Widget]: https://godoc.org/github.com/aperturerobotics/fastjson?status.svg

**fastjson** is an alternative to **encoding/json** which does not use reflection.

**This is a fork of the [upstream project] with some improvements.**

[upstream project]: https://github.com/valyala/fastjson

## Features

  * Up to 15x faster than the standard [encoding/json](https://golang.org/pkg/encoding/json/).
    See [benchmarks](#benchmarks).
  * Parses arbitrary JSON without schema, reflection, struct magic and code generation
    contrary to [easyjson](https://github.com/mailru/easyjson).
  * Provides simple [API](http://godoc.org/github.com/aperturerobotics/fastjson).
  * Outperforms [jsonparser](https://github.com/buger/jsonparser) and [gjson](https://github.com/tidwall/gjson)
    when accessing multiple unrelated fields, since `fastjson` parses the input JSON only once.
  * Validates the parsed JSON unlike [jsonparser](https://github.com/buger/jsonparser)
    and [gjson](https://github.com/tidwall/gjson).
  * May quickly extract a part of the original JSON with `Value.Get(...).MarshalTo` and modify it
    with [Del](https://godoc.org/github.com/aperturerobotics/fastjson#Value.Del)
    and [Set](https://godoc.org/github.com/aperturerobotics/fastjson#Value.Set) functions.
  * May parse array containing values with distinct types (aka non-homogenous types).
    For instance, `fastjson` easily parses the following JSON array `[123, "foo", [456], {"k": "v"}, null]`.
  * `fastjson` preserves the original order of object items when calling
    [Object.Visit](https://godoc.org/github.com/aperturerobotics/fastjson#Object.Visit).


## Known limitations

  * Requies extra care to work with - references to certain objects recursively
    returned by [Parser](https://godoc.org/github.com/aperturerobotics/fastjson#Parser)
    must be released before the next call to [Parse](https://godoc.org/github.com/aperturerobotics/fastjson#Parser.Parse).
    Otherwise the program may work improperly. The same applies to objects returned by [Arena](https://godoc.org/github.com/aperturerobotics/fastjson#Arena).
    Adhere recommendations from [docs](https://godoc.org/github.com/aperturerobotics/fastjson).
  * Cannot parse JSON from `io.Reader`. There is [Scanner](https://godoc.org/github.com/aperturerobotics/fastjson#Scanner)
    for parsing stream of JSON values from a string.


## Usage

One-liner accessing a single field:
```go
	s := []byte(`{"foo": [123, "bar"]}`)
	fmt.Printf("foo.0=%d\n", fastjson.GetInt(s, "foo", "0"))

	// Output:
	// foo.0=123
```

Accessing multiple fields with error handling:
```go
        var p fastjson.Parser
        v, err := p.Parse(`{
                "str": "bar",
                "int": 123,
                "float": 1.23,
                "bool": true,
                "arr": [1, "foo", {}]
        }`)
        if err != nil {
                log.Fatal(err)
        }
        fmt.Printf("foo=%s\n", v.GetStringBytes("str"))
        fmt.Printf("int=%d\n", v.GetInt("int"))
        fmt.Printf("float=%f\n", v.GetFloat64("float"))
        fmt.Printf("bool=%v\n", v.GetBool("bool"))
        fmt.Printf("arr.1=%s\n", v.GetStringBytes("arr", "1"))

        // Output:
        // foo=bar
        // int=123
        // float=1.230000
        // bool=true
        // arr.1=foo
```

See also [examples](https://godoc.org/github.com/aperturerobotics/fastjson#pkg-examples).


## Security

  * `fastjson` shouldn't crash or panic when parsing input strings specially crafted
    by an attacker. It must return error on invalid input JSON.
  * `fastjson` requires up to `sizeof(Value) * len(inputJSON)` bytes of memory
    for parsing `inputJSON` string. Limit the maximum size of the `inputJSON`
    before parsing it in order to limit the maximum memory usage.


## Performance optimization tips

  * Re-use [Parser](https://godoc.org/github.com/aperturerobotics/fastjson#Parser) and [Scanner](https://godoc.org/github.com/aperturerobotics/fastjson#Scanner)
    for parsing many JSONs. This reduces memory allocations overhead.
    [ParserPool](https://godoc.org/github.com/aperturerobotics/fastjson#ParserPool) may be useful in this case.
  * Prefer calling `Value.Get*` on the value returned from [Parser](https://godoc.org/github.com/aperturerobotics/fastjson#Parser)
    instead of calling `Get*` one-liners when multiple fields
    must be obtained from JSON, since each `Get*` one-liner re-parses
    the input JSON again.
  * Prefer calling once [Value.Get](https://godoc.org/github.com/aperturerobotics/fastjson#Value.Get)
    for common prefix paths and then calling `Value.Get*` on the returned value
    for distinct suffix paths.
  * Prefer iterating over array returned from [Value.GetArray](https://godoc.org/github.com/aperturerobotics/fastjson#Object.Visit)
    with a range loop instead of calling `Value.Get*` for each array item.


## Slot ownership checks and their cost

A `Value` is handed out from an allocator slot and must never be copied by
struct assignment.
A copy names the original's member and child storage, so a later write through
either reaches both, and whichever slot is recycled first releases storage the
other still uses.
`Value` and `Object` therefore carry a zero-sized field that makes `go vet`
report any such copy, and every entry point checks who owns the slot it was
reached through: mutators, readers, `MarshalTo` and `Type` panic with
`*IntegrityError`, while allocation and reset drop the foreign headers, count
the drop in `IntegrityHealCount`, and continue.
`SetStrictIntegrity(true)` turns those two sites into panics as well.
Use `Value.SetStringValue` to turn an existing node into a string in place;
that is the supported replacement for assigning a whole value over another.

The checks and the reset they go with are not free.
Measured against the same tree without them, single core, alternating order,
twelve runs per tree on each of two machines; the bands span both:

| benchmark | cost |
|---|---|
| `Parse` small, medium, large, twitter | +15% to +31% |
| `Parse` canada | +37% to +56% |
| `MarshalTo` small, large, twitter | within noise |
| `MarshalTo` canada | +17% to +36% |
| `ArenaTypicalUse` | +12% |
| allocations per operation | unchanged |

Almost none of that is the ownership compare itself, which is one pointer load
and one branch per visit.
It comes from two things: reset now clears every slot the allocator handed out,
which is work proportional to the number of values in the document where it
used to be a single truncation, and `Value` grew from 80 to 88 bytes, so a walk
over a large document touches more memory.
The two large-document rows are where both effects land at once.
This is the price of a document that cannot silently share storage with
another; do not re-litigate it from a benchmark diff alone.
