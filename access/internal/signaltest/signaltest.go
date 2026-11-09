// Package signaltest is an in-memory signal channel with the shape of the
// application's one: Signal publishes a kind and Subscribe follows one. Tests
// drive the engine's ChangeSignal adapter over it without an emulator. A
// Signal wakes every subscriber of its kind before it returns, so a test that
// has seen Signal return has seen the subscribers run.
package signaltest

import (
	"context"
	"slices"
	"sync"
)

// Channel is the fake. The zero value is ready to use.
type Channel struct {
	mu          sync.Mutex
	subscribers map[string]map[int]func()
	next        int
	signals     []string
}

// Signal records kind and wakes every subscriber of kind, on the calling
// goroutine, before returning.
func (c *Channel) Signal(_ context.Context, kind string) error {
	c.mu.Lock()
	c.signals = append(c.signals, kind)
	wake := make([]func(), 0, len(c.subscribers[kind]))
	for _, onSignal := range c.subscribers[kind] {
		wake = append(wake, onSignal)
	}
	c.mu.Unlock()

	for _, onSignal := range wake {
		onSignal()
	}

	return nil
}

// Subscribe registers onSignal for kind and returns the function that ends
// the subscription.
func (c *Channel) Subscribe(kind string, onSignal func()) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscribers == nil {
		c.subscribers = make(map[string]map[int]func())
	}
	if c.subscribers[kind] == nil {
		c.subscribers[kind] = make(map[int]func())
	}
	id := c.next
	c.next++
	c.subscribers[kind][id] = onSignal

	stop := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.subscribers[kind], id)
	}

	return stop, nil
}

// Subscribers reports how many subscriptions of kind are open.
func (c *Channel) Subscribers(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.subscribers[kind])
}

// Signals returns the kinds signaled so far, in order.
func (c *Channel) Signals() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.signals)
}
