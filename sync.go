package main

import (
	"sync"
)

// LimitedWaitGroup limits the number of concurrent goroutines.
type LimitedWaitGroup struct {
	wg  sync.WaitGroup
	sem chan struct{}
}

// NewLimitedWaitGroup creates a wait group that only allows 'limit' active slots.
func NewLimitedWaitGroup(limit int) *LimitedWaitGroup {
	if limit <= 0 {
		panic("LimitedWaitGroup: limit must be positive")
	}
	return &LimitedWaitGroup{
		sem: make(chan struct{}, limit),
	}
}

// Add acquires slots. It blocks if the limit is reached.
func (lg *LimitedWaitGroup) Add(delta int) {
	if delta <= 0 {
		return // Or handle as an error; standard sync.WaitGroup allows negative delta, but usually for Done.
	}

	// CRITICAL: Call wg.Add() BEFORE acquiring slots to prevent race condition
	// If a goroutine finishes between slot acquisition and wg.Add(), Wait() could return early
	lg.wg.Add(delta)

	for i := 0; i < delta; i++ {
		lg.sem <- struct{}{} // Acquire slot (blocks if full)
	}
}

// Done releases a slot and notifies the WaitGroup.
func (lg *LimitedWaitGroup) Done() {
	<-lg.sem // Release slot
	lg.wg.Done()
}

// Wait blocks until all goroutines have finished.
func (lg *LimitedWaitGroup) Wait() {
	lg.wg.Wait()
}
