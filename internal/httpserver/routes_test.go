package httpserver

import "testing"

// TestRouteTableRegistersWithoutConflict constructs the route table without a
// database or template set. net/http panics on duplicate patterns; this test
// catches cross-module route conflicts before a server starts.
func TestRouteTableRegistersWithoutConflict(t *testing.T) {
	a := &App{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route registration panicked: %v", r)
		}
	}()
	if h := a.Handler(); h == nil {
		t.Fatal("Handler returned nil")
	}
}
