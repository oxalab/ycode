package ycode

// agent_session_retry_test.go covers the retry-chain primitives (audit #14):
// waitForRetry liveness, chain-spanning retryChan, and abort delivery — no
// LLM involved.

import (
	"testing"
	"time"
)

func newRetryTestSession() *AgentSession {
	return &AgentSession{}
}

func TestWaitForRetryNilChanReturnsImmediately(t *testing.T) {
	s := newRetryTestSession()
	done := make(chan struct{})
	go func() {
		s.waitForRetry()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForRetry blocked with no retry pending")
	}
}

func TestWaitForRetryReturnsAfterResolve(t *testing.T) {
	s := newRetryTestSession()
	s.retryMu.Lock()
	s.retryChan = make(chan struct{}, 1)
	s.retryMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.waitForRetry()
		close(done)
	}()

	// Let the waiter block, then settle the chain.
	time.Sleep(20 * time.Millisecond)
	s.resolveRetry()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForRetry did not return after resolveRetry")
	}

	// The chain must be cleared after resolution (TS: _retryPromise = undefined).
	s.retryMu.Lock()
	cleared := s.retryChan == nil
	s.retryMu.Unlock()
	if !cleared {
		t.Error("retryChan not cleared by resolveRetry")
	}
}

func TestWaitForRetryReturnsAfterAbort(t *testing.T) {
	s := newRetryTestSession()
	s.retryMu.Lock()
	s.retryChan = make(chan struct{}, 1)
	s.backoffAbort = make(chan struct{}, 1)
	s.retryMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.waitForRetry()
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	s.AbortRetry()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForRetry did not return after AbortRetry")
	}
}

func TestRetryChainSpansBackoffCycle(t *testing.T) {
	// Simulate one backoff cycle ending (backoffAbort cleared, as the real
	// goroutine does): the chain (retryChan) must STAY non-nil until
	// resolveRetry — a retried turn's error must see the chain in flight
	// (TS: same _retryPromise across cycles).
	s := newRetryTestSession()
	s.retryMu.Lock()
	s.retryChan = make(chan struct{}, 1)
	s.backoffAbort = make(chan struct{}, 1)
	s.retryMu.Unlock()

	// Cycle ends (mirror of the goroutine's timer branch).
	s.retryMu.Lock()
	s.backoffAbort = nil
	s.retryMu.Unlock()

	s.retryMu.Lock()
	stillPending := s.retryChan != nil
	s.retryMu.Unlock()
	if !stillPending {
		t.Fatal("retryChan was cleared at backoff end — chain must span the retried turn")
	}

	// Settle.
	s.resolveRetry()
	s.retryMu.Lock()
	cleared := s.retryChan == nil
	s.retryMu.Unlock()
	if !cleared {
		t.Error("retryChan not cleared after resolve")
	}
}
