package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestNewLimitedWaitGroup_ZeroLimit tests that a zero limit causes a panic
func TestNewLimitedWaitGroup_ZeroLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewLimitedWaitGroup(0) expected panic, got nil")
		}
	}()
	NewLimitedWaitGroup(0)
}

// TestNewLimitedWaitGroup_NegativeLimit tests that a negative limit causes a panic
func TestNewLimitedWaitGroup_NegativeLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewLimitedWaitGroup(-1) expected panic, got nil")
		}
	}()
	NewLimitedWaitGroup(-1)
}

// TestLimitedWaitGroup_BasicWait tests basic Add/Done/Wait functionality
func TestLimitedWaitGroup_BasicWait(t *testing.T) {
	lw := NewLimitedWaitGroup(2)
	var counter int32

	for i := 0; i < 5; i++ {
		lw.Add(1)
		go func() {
			defer lw.Done()
			atomic.AddInt32(&counter, 1)
		}()
	}

	lw.Wait()

	if atomic.LoadInt32(&counter) != 5 {
		t.Errorf("counter = %v, want %v", counter, 5)
	}
}

// TestLimitedWaitGroup_ConcurrencyLimit tests that concurrency is actually limited
func TestLimitedWaitGroup_ConcurrencyLimit(t *testing.T) {
	limit := 3
	lw := NewLimitedWaitGroup(limit)

	var (
		activeCount   int32
		maxActive     int32
		totalGoroutines = 10
	)

	for i := 0; i < totalGoroutines; i++ {
		lw.Add(1)
		go func() {
			defer lw.Done()

			// Record active count
			current := atomic.AddInt32(&activeCount, 1)

			// Track maximum concurrent goroutines
			for {
				oldMax := atomic.LoadInt32(&maxActive)
				if current <= oldMax || atomic.CompareAndSwapInt32(&maxActive, oldMax, current) {
					break
				}
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			atomic.AddInt32(&activeCount, -1)
		}()
	}

	lw.Wait()

	maxConcurrent := atomic.LoadInt32(&maxActive)
	if maxConcurrent > int32(limit) {
		t.Errorf("max concurrent goroutines = %v, want <= %v", maxConcurrent, limit)
	}
	if maxConcurrent < int32(limit) && totalGoroutines >= limit {
		// We expect to reach the limit at some point
		t.Logf("warning: max concurrent goroutines (%v) did not reach limit (%v)", maxConcurrent, limit)
	}
}

// TestLimitedWaitGroup_NegativeDelta tests behavior with negative delta (should be ignored)
func TestLimitedWaitGroup_NegativeDelta(t *testing.T) {
	lw := NewLimitedWaitGroup(2)
	var counter int32

	// Add with negative delta should be ignored
	lw.Add(-1)

	// Normal Add should work
	lw.Add(1)
	go func() {
		defer lw.Done()
		atomic.AddInt32(&counter, 1)
	}()

	lw.Wait()

	if atomic.LoadInt32(&counter) != 1 {
		t.Errorf("counter = %v, want %v", counter, 1)
	}
}

// TestLimitedWaitGroup_ZeroDelta tests behavior with zero delta
func TestLimitedWaitGroup_ZeroDelta(t *testing.T) {
	lw := NewLimitedWaitGroup(2)
	var counter int32

	// Add with zero delta should be ignored
	lw.Add(0)

	// Normal Add should work
	lw.Add(1)
	go func() {
		defer lw.Done()
		atomic.AddInt32(&counter, 1)
	}()

	lw.Wait()

	if atomic.LoadInt32(&counter) != 1 {
		t.Errorf("counter = %v, want %v", counter, 1)
	}
}

// TestLimitedWaitGroup_RaceCondition performs race detection with many goroutines
func TestLimitedWaitGroup_RaceCondition(t *testing.T) {
	limit := 5
	lw := NewLimitedWaitGroup(limit)

	var counter int32
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		lw.Add(1)
		go func() {
			defer lw.Done()

			// Simulate work with race-prone operations
			for j := 0; j < 10; j++ {
				atomic.AddInt32(&counter, 1)
				time.Sleep(time.Microsecond)
				atomic.AddInt32(&counter, -1)
			}
		}()
	}

	lw.Wait()

	// Counter should be back to 0 after all goroutines complete
	if atomic.LoadInt32(&counter) != 0 {
		t.Errorf("counter = %v, want %v", counter, 0)
	}
}

// TestLimitedWaitGroup_MultipleAdd tests adding multiple slots at once
func TestLimitedWaitGroup_MultipleAdd(t *testing.T) {
	limit := 2
	lw := NewLimitedWaitGroup(limit)

	var counter int32

	// Add 2 slots at once (should block since limit is 2)
	lw.Add(2)

	go func() {
		defer lw.Done()
		atomic.AddInt32(&counter, 1)
		time.Sleep(10 * time.Millisecond)
	}()

	go func() {
		defer lw.Done()
		atomic.AddInt32(&counter, 1)
		time.Sleep(10 * time.Millisecond)
	}()

	lw.Wait()

	if atomic.LoadInt32(&counter) != 2 {
		t.Errorf("counter = %v, want %v", counter, 2)
	}
}

// TestLimitedWaitGroup_WaitWithoutAdd tests Wait without any Add calls
func TestLimitedWaitGroup_WaitWithoutAdd(t *testing.T) {
	lw := NewLimitedWaitGroup(2)

	// Wait without Add should return immediately
	done := make(chan struct{})
	go func() {
		lw.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - Wait returned immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Wait() without Add should return immediately")
	}
}

// TestLimitedWaitGroup_SlotRelease tests that slots are properly released
func TestLimitedWaitGroup_SlotRelease(t *testing.T) {
	limit := 1
	lw := NewLimitedWaitGroup(limit)

	var firstDone, secondDone int32

	// First goroutine acquires the only slot
	lw.Add(1)
	go func() {
		defer lw.Done()
		time.Sleep(50 * time.Millisecond)
		atomic.StoreInt32(&firstDone, 1)
	}()

	// Give first goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Second goroutine should block until first releases slot
	lw.Add(1)
	go func() {
		defer lw.Done()
		atomic.StoreInt32(&secondDone, 1)
	}()

	lw.Wait()

	// Both should be done after Wait
	if atomic.LoadInt32(&firstDone) != 1 {
		t.Error("first goroutine should complete")
	}
	if atomic.LoadInt32(&secondDone) != 1 {
		t.Error("second goroutine should complete after slot is released")
	}
}
