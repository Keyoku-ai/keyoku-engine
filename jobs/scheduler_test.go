// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package jobs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockProcessor is a simple JobProcessor for scheduler tests.
type mockProcessor struct {
	mu        sync.Mutex
	jobType   JobType
	callCount int
	result    *JobResult
	err       error
	delay     time.Duration
}

func (p *mockProcessor) Type() JobType { return p.jobType }

func (p *mockProcessor) Process(_ context.Context) (*JobResult, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if p.err != nil {
		return nil, p.err
	}
	if p.result != nil {
		return p.result, nil
	}
	return &JobResult{ItemsProcessed: 1, ItemsAffected: 0}, nil
}

func (p *mockProcessor) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

func TestNewScheduler(t *testing.T) {
	t.Run("nil logger and schedules", func(t *testing.T) {
		s := NewScheduler(nil, nil)
		if s == nil {
			t.Fatal("expected non-nil scheduler")
		}
		if len(s.schedules) != 4 {
			t.Errorf("default schedules = %d, want 4", len(s.schedules))
		}
		s.Stop()
	})

	t.Run("custom schedules", func(t *testing.T) {
		schedules := []JobSchedule{
			{JobType: JobTypeDecay, Interval: 5 * time.Second, Enabled: true},
		}
		s := NewScheduler(nil, schedules)
		if len(s.schedules) != 1 {
			t.Errorf("schedules = %d, want 1", len(s.schedules))
		}
		s.Stop()
	})
}

func TestDefaultSchedules(t *testing.T) {
	schedules := DefaultSchedules()
	if len(schedules) != 4 {
		t.Fatalf("DefaultSchedules() = %d, want 4", len(schedules))
	}

	types := map[JobType]bool{}
	for _, s := range schedules {
		types[s.JobType] = true
		if !s.Enabled {
			t.Errorf("schedule %s should be enabled", s.JobType)
		}
		if s.Interval <= 0 {
			t.Errorf("schedule %s interval = %v, want > 0", s.JobType, s.Interval)
		}
	}
	for _, jt := range []JobType{JobTypeDecay, JobTypeConsolidation, JobTypeArchival, JobTypePurge} {
		if !types[jt] {
			t.Errorf("missing default schedule for %s", jt)
		}
	}
}

func TestRegisterProcessor(t *testing.T) {
	s := NewScheduler(nil, nil)
	defer s.Stop()

	p := &mockProcessor{jobType: JobTypeDecay}
	s.RegisterProcessor(p)

	s.mu.RLock()
	_, ok := s.processors[JobTypeDecay]
	s.mu.RUnlock()
	if !ok {
		t.Error("expected processor to be registered")
	}
}

func TestScheduler_StartStop(t *testing.T) {
	schedules := []JobSchedule{
		{JobType: JobTypeDecay, Interval: 50 * time.Millisecond, Enabled: true},
	}
	s := NewScheduler(nil, schedules)

	p := &mockProcessor{jobType: JobTypeDecay}
	s.RegisterProcessor(p)

	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Stop()

	if p.calls() < 1 {
		t.Error("expected processor to be called at least once")
	}
}

func TestScheduler_StartSkipsDisabled(t *testing.T) {
	schedules := []JobSchedule{
		{JobType: JobTypeDecay, Interval: 50 * time.Millisecond, Enabled: false},
	}
	s := NewScheduler(nil, schedules)

	p := &mockProcessor{jobType: JobTypeDecay}
	s.RegisterProcessor(p)

	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Stop()

	if p.calls() != 0 {
		t.Errorf("disabled schedule should not trigger processor, got %d calls", p.calls())
	}
}

func TestScheduler_StartSkipsUnregistered(t *testing.T) {
	schedules := []JobSchedule{
		{JobType: JobTypeDecay, Interval: 50 * time.Millisecond, Enabled: true},
	}
	s := NewScheduler(nil, schedules)
	// Do not register any processor
	s.Start()
	time.Sleep(100 * time.Millisecond)
	s.Stop()
	// Should not panic
}

func TestRunNow_HappyPath(t *testing.T) {
	s := NewScheduler(nil, nil)
	defer s.Stop()

	p := &mockProcessor{
		jobType: JobTypeDecay,
		result:  &JobResult{ItemsProcessed: 5, ItemsAffected: 2},
	}
	s.RegisterProcessor(p)

	err := s.RunNow(context.Background(), JobTypeDecay)
	if err != nil {
		t.Fatalf("RunNow error = %v", err)
	}

	// Wait for async execution
	time.Sleep(50 * time.Millisecond)

	if p.calls() != 1 {
		t.Errorf("calls = %d, want 1", p.calls())
	}
}

func TestRunNow_NoProcessor(t *testing.T) {
	s := NewScheduler(nil, nil)
	defer s.Stop()

	err := s.RunNow(context.Background(), JobTypeDecay)
	if err == nil {
		t.Error("expected error for unregistered processor")
	}
}

func TestRunNow_AlreadyRunning(t *testing.T) {
	s := NewScheduler(nil, nil)
	defer s.Stop()

	p := &mockProcessor{
		jobType: JobTypeDecay,
		delay:   200 * time.Millisecond,
	}
	s.RegisterProcessor(p)

	// Start first run
	err := s.RunNow(context.Background(), JobTypeDecay)
	if err != nil {
		t.Fatalf("first RunNow error = %v", err)
	}

	// Try to run again immediately — should be blocked
	time.Sleep(10 * time.Millisecond)
	err = s.RunNow(context.Background(), JobTypeDecay)
	if err == nil {
		t.Error("expected error for already running job")
	}

	// Wait for completion
	time.Sleep(300 * time.Millisecond)

	// Should work again now
	err = s.RunNow(context.Background(), JobTypeDecay)
	if err != nil {
		t.Fatalf("third RunNow error = %v", err)
	}
}

func TestRunNow_ProcessorError(t *testing.T) {
	s := NewScheduler(nil, nil)
	defer s.Stop()

	p := &mockProcessor{
		jobType: JobTypeDecay,
		err:     fmt.Errorf("processing failed"),
	}
	s.RegisterProcessor(p)

	// RunNow itself doesn't return the processor error (it runs async)
	err := s.RunNow(context.Background(), JobTypeDecay)
	if err != nil {
		t.Fatalf("RunNow error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if p.calls() != 1 {
		t.Errorf("calls = %d, want 1", p.calls())
	}
}

func TestJobTypes(t *testing.T) {
	if JobTypeDecay != "decay" {
		t.Errorf("JobTypeDecay = %q", JobTypeDecay)
	}
	if JobTypeConsolidation != "consolidation" {
		t.Errorf("JobTypeConsolidation = %q", JobTypeConsolidation)
	}
	if JobTypeArchival != "archival" {
		t.Errorf("JobTypeArchival = %q", JobTypeArchival)
	}
	if JobTypePurge != "purge" {
		t.Errorf("JobTypePurge = %q", JobTypePurge)
	}
}
