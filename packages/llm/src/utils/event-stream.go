package utils

import( 
	"sync"
	"ycode/packages/llm/src"
)

// EventStream is an async event stream with final result extraction.
// Consumers iterate via the channel returned by Events(); the stream
//  auto-closes when "complete" event is published

type EventStream[T any, R any] struct {
	ch 				chan T
	isComplete 		func(T) bool
	extractResult 	func(T) R
	resultCh  		chan R
	mu 				sync.Mutex
	ended 			bool		
}

func NewEventStream[T any, R any](isComplete func(T) bool, extractResult func(T) R) *EventStream[T, R] {
	return &EventStream[T, R]{
		ch: 			make(chan T, 256),
		isComplete: 	isComplete,
		extractResult: 	extractResult,
		resultCh: 		make(chan R, 1),
	}
}

// Push: Appends an event. If the event satisfies isComplete the stream is closed and 
// the final result becomes available via Result().
func(s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	if s.isComplete(event) {
		s.ended = true
		s.ch <- event
		close(s.ch)
		s.resultCh <- s.extractResult(event)
		return
	}
	s.ch <- event
}

// End: Signals that no more events will arrive and closes the stream.
// If the result is non-nill it becomes the final result; otherwise Result()
// returns the zero value of R immediately.
func (s *EventStream[T, R]) End(result *R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	close(s.ch)
	if result != nil {
		s.resultCh <- *result
	} else {
		close(s.resultCh)
	}
}

// Events: Returns the read-only channel consumers range over
func (s *EventStream[T, R]) Events() <-chan T {
	return s.ch
}

// Result: Blocks until the stream completes and then returns the final result
func (s *EventStream[T, R]) Result() <-chan R {
	return s.resultCh
}

// AssistantMessageEventStream -> Concrete stream used by all LLM providers
type AssistantMessageEventStream = EventStream[llm.AssistantMessageEvent, llm.AssistantMessage]

// NewAssistantMessageEventStream creates a stream that completes on "done" or "error" events.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return NewEventStream(
		func(event llm.AssistantMessageEvent) bool {
			return event.Type == "done" || event.Type == "error"
		},
		func(event llm.AssistantMessageEvent) llm.AssistantMessage {
			switch event.Type {
			case "done":
				if event.Message != nil {
					return *event.Message
				}
				return llm.AssistantMessage{}
			case "error":
				if event.Error != nil {
					return *event.Error
				}
				return llm.AssistantMessage{}
			default:
				panic("unexpected event type for final result")
			}
		},
	)
}