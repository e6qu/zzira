package notifybus

import "testing"

func TestSubscribeCancelIsIdempotent(t *testing.T) {
	bus := New()
	ch, cancel := bus.Subscribe("ws")
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("subscription channel remained open")
	}
}
