package httpserver

import (
	"fmt"
	"sync"
)

// inflightGroup coalesces concurrent identical read builds. It is deliberately
// not a cache: the result is discarded as soon as every waiter returns, so
// freshness semantics are unchanged.
type inflightGroup struct {
	mu    sync.Mutex
	calls map[string]*inflightCall
}

type inflightCall struct {
	done chan struct{}
	once sync.Once
	val  any
	err  error
}

func (g *inflightGroup) Do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		<-call.done
		return call.val, call.err
	}
	if g.calls == nil {
		g.calls = make(map[string]*inflightCall)
	}
	call := &inflightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	defer func() {
		if rec := recover(); rec != nil {
			call.err = fmt.Errorf("httpserver: inflight panic: %v", rec)
			g.finish(key, call)
			panic(rec)
		}
	}()
	call.val, call.err = fn()
	g.finish(key, call)
	return call.val, call.err
}

func (g *inflightGroup) finish(key string, call *inflightCall) {
	call.once.Do(func() { close(call.done) })
	g.mu.Lock()
	if g.calls[key] == call {
		delete(g.calls, key)
	}
	g.mu.Unlock()
}

func legacyInflightKey(userID int64, viewRole, query string) string {
	return fmt.Sprintf("%d|%s|%s", userID, viewRole, query)
}
