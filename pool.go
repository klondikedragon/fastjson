package fastjson

import (
	"sync"
)

// ParserPool may be used for pooling Parsers for similarly typed JSONs.
type ParserPool struct {
	pool sync.Pool
}

// Get returns a Parser from pp.
//
// The Parser must be Put to pp after use.
func (pp *ParserPool) Get() *Parser {
	v := pp.pool.Get()
	if v == nil {
		return &Parser{}
	}
	return v.(*Parser)
}

// Put returns p to pp.
//
// Unlike ArenaPool.Put this does not reset p. Parse resets the cache on entry,
// so a parser taken from the pool releases the previous document before it
// builds the next one; resetting here as well would clear every slot twice.
//
// p and objects recursively returned from p cannot be used after p
// is put into pp.
func (pp *ParserPool) Put(p *Parser) {
	pp.pool.Put(p)
}

// ArenaPool may be used for pooling Arenas for similarly typed JSONs.
type ArenaPool struct {
	pool sync.Pool
}

// Get returns an Arena from ap.
//
// The Arena must be Put to ap after use.
func (ap *ArenaPool) Get() *Arena {
	v := ap.pool.Get()
	if v == nil {
		return &Arena{}
	}
	return v.(*Arena)
}

// Put returns a to ap.
//
// Put resets a before pooling it, so the arena buffer and every value
// slot it owns are released before another caller can obtain them.
//
// a and objects created by a cannot be used after a is put into ap.
func (ap *ArenaPool) Put(a *Arena) {
	a.Reset()
	ap.pool.Put(a)
}
