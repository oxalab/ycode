package ycode

import (
	"fmt"
	"sync"
)

type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler func(any)) func()
}

type EventBusController interface {
	EventBus
	Clear()
}

type eventBus struct {
	mu       sync.Mutex
	handlers map[string]map[uint64]func(any)
	nextID   uint64
}

func CreateEventBus() EventBusController {
	return &eventBus{
		handlers: make(map[string]map[uint64]func(any)),
	}
}

func (eb *eventBus) Emit(channel string, data any) {
	eb.mu.Lock()
	snapshot := make(map[uint64]func(any), len(eb.handlers[channel]))
	for id, h := range eb.handlers[channel] {
		snapshot[id] = h
	}
	eb.mu.Unlock()

	for _, h := range snapshot {
		func(h func(any)) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("Event handler error (%s): %v\n", channel, r)
				}
			}()
			h(data)
		}(h)
	}
}

func (eb *eventBus) On(channel string, handler func(any)) func() {
	eb.mu.Lock()
	id := eb.nextID
	eb.nextID++
	if eb.handlers[channel] == nil {
		eb.handlers[channel] = make(map[uint64]func(any))
	}
	eb.handlers[channel][id] = handler
	eb.mu.Unlock()

	return func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		delete(eb.handlers[channel], id)
	}
}

func (eb *eventBus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.handlers = make(map[string]map[uint64]func(any))
}
