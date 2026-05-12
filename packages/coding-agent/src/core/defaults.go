package ycode

import (
	"fmt"
	"sync"
)

type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler func(data any)) func()
}

type EventBusController interface {
	EventBus
	Clear()
}

type eventBus struct {
	mu sync.RWMutex
	listeners map[string][]func(any)
}

func NewEventBus() EventBusController {
	return &eventBus{
		listeners: make(map[string][]func(any)),
	}
}

func (eb *eventBus) Emit(channel string, data any) {
	eb.mu.RLock()
	handlers := make([]func(any), len(eb.listeners[channel]))
	copy(handlers, eb.listeners[channel])
	eb.mu.RUnlock()

	for _, handler := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("Event handler error (%s): %v\n", channel, r)
				}
			} ()
			handler(data)
		}()
	}
}

func (eb *eventBus) On(channel string, handler func(data any)) func() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.listeners[channel] = append(eb.listeners[channel], handler)
	return func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		handlers := eb.listeners[channel]
		for i, h := range handlers {
			if &h == &handler {
				eb.listeners[channel] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}
}

func (eb *eventBus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.listeners = make(map[string][]func(any))
}