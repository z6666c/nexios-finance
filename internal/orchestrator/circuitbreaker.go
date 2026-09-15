package orchestrator

import (
	"sync"
	"time"
)

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

type CircuitBreaker struct {
	mu               sync.Mutex
	failureThreshold int
	openDuration     time.Duration
	latencyThreshold time.Duration

	failures  map[string]int
	state     map[string]breakerState
	openSince map[string]time.Time
}

func NewCircuitBreaker(failureThreshold int, openDuration, latencyThreshold time.Duration) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if openDuration == 0 {
		openDuration = 5 * time.Second
	}
	if latencyThreshold == 0 {
		latencyThreshold = 150 * time.Millisecond
	}
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		openDuration:     openDuration,
		latencyThreshold: latencyThreshold,
		failures:         make(map[string]int),
		state:            make(map[string]breakerState),
		openSince:        make(map[string]time.Time),
	}
}

func (cb *CircuitBreaker) Allow(provider string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state[provider] {
	case stateOpen:
		if time.Since(cb.openSince[provider]) >= cb.openDuration {
			cb.state[provider] = stateHalfOpen
			return true
		}
		return false
	default:
		return true
	}
}

func (cb *CircuitBreaker) RecordResult(provider string, err error, latency time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	slow := latency > cb.latencyThreshold
	if err != nil || slow {
		cb.failures[provider]++
		if cb.state[provider] == stateHalfOpen || cb.failures[provider] >= cb.failureThreshold {
			cb.state[provider] = stateOpen
			cb.openSince[provider] = time.Now()
		}
		return
	}

	cb.failures[provider] = 0
	cb.state[provider] = stateClosed
}
