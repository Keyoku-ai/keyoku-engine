// Package jobs provides background job scheduling for Keyoku Embedded.
// Uses simple in-memory timers instead of database-backed scheduling.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// JobType represents the type of scheduled job.
type JobType string

const (
	JobTypeDecay          JobType = "decay"
	JobTypeConsolidation  JobType = "consolidation"
	JobTypeArchival       JobType = "archival"
	JobTypePurge          JobType = "purge"
)

// JobProcessor processes a specific job type.
type JobProcessor interface {
	Type() JobType
	Process(ctx context.Context) (*JobResult, error)
}

// JobResult contains the result of a job execution.
type JobResult struct {
	ItemsProcessed int
	ItemsAffected  int
	Details        map[string]any
}

// JobSchedule defines when a job should run.
type JobSchedule struct {
	JobType  JobType
	Interval time.Duration
	Enabled  bool
}

// EventEmitter is a callback for emitting events from the scheduler.
type EventEmitter func(eventType string, entityID string, agentID string, data map[string]any)

// Scheduler manages background jobs using in-memory timers.
type Scheduler struct {
	logger     *slog.Logger
	processors map[JobType]JobProcessor
	schedules  []JobSchedule
	timers     []*time.Ticker
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex
	running    map[JobType]bool
	emitter    EventEmitter
}

// DefaultSchedules returns the default job schedules.
func DefaultSchedules() []JobSchedule {
	return []JobSchedule{
		{JobType: JobTypeDecay, Interval: 1 * time.Hour, Enabled: true},
		{JobType: JobTypeConsolidation, Interval: 24 * time.Hour, Enabled: true},
		{JobType: JobTypeArchival, Interval: 24 * time.Hour, Enabled: true},
		{JobType: JobTypePurge, Interval: 24 * time.Hour, Enabled: true},
	}
}

// NewScheduler creates a new in-memory job scheduler.
func NewScheduler(logger *slog.Logger, schedules []JobSchedule) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if schedules == nil {
		schedules = DefaultSchedules()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		logger:     logger.With("component", "scheduler"),
		processors: make(map[JobType]JobProcessor),
		schedules:  schedules,
		ctx:        ctx,
		cancel:     cancel,
		running:    make(map[JobType]bool),
	}
}

// SetEmitter sets the event emitter callback.
func (s *Scheduler) SetEmitter(emitter EventEmitter) { s.emitter = emitter }

// RegisterProcessor registers a processor for a job type.
func (s *Scheduler) RegisterProcessor(processor JobProcessor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processors[processor.Type()] = processor
	s.logger.Info("registered job processor", "type", processor.Type())
}

// Start starts all scheduled job timers.
func (s *Scheduler) Start() {
	s.logger.Info("starting scheduler")

	for _, schedule := range s.schedules {
		if !schedule.Enabled {
			continue
		}

		// Check if we have a processor for this job type
		s.mu.RLock()
		_, ok := s.processors[schedule.JobType]
		s.mu.RUnlock()
		if !ok {
			s.logger.Warn("no processor registered for job type", "type", schedule.JobType)
			continue
		}

		ticker := time.NewTicker(schedule.Interval)
		s.timers = append(s.timers, ticker)

		s.wg.Add(1)
		go s.runTimer(ticker, schedule.JobType)
	}
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")
	s.cancel()

	for _, ticker := range s.timers {
		ticker.Stop()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("scheduler stopped gracefully")
	case <-time.After(60 * time.Second):
		s.logger.Warn("scheduler stop timed out")
	}
}

// RunNow manually triggers a job to run immediately.
func (s *Scheduler) RunNow(ctx context.Context, jobType JobType) error {
	s.mu.RLock()
	processor, ok := s.processors[jobType]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no processor registered for job type: %s", jobType)
	}

	if !s.tryStart(jobType) {
		return fmt.Errorf("job already running: %s", jobType)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.finish(jobType)
		s.executeJob(processor)
	}()

	return nil
}

// runTimer runs jobs on a timer.
func (s *Scheduler) runTimer(ticker *time.Ticker, jobType JobType) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			processor, ok := s.processors[jobType]
			s.mu.RUnlock()

			if !ok {
				continue
			}

			if !s.tryStart(jobType) {
				s.logger.Debug("job already running, skipping", "type", jobType)
				continue
			}

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer s.finish(jobType)
				s.executeJob(processor)
			}()
		}
	}
}

func (s *Scheduler) tryStart(jobType JobType) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[jobType] {
		return false
	}
	s.running[jobType] = true
	return true
}

func (s *Scheduler) finish(jobType JobType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, jobType)
}

func (s *Scheduler) executeJob(processor JobProcessor) {
	ctx := context.Background()
	logger := s.logger.With("type", processor.Type())

	logger.Info("executing job")
	startTime := time.Now()

	result, err := processor.Process(ctx)

	duration := time.Since(startTime)
	if err != nil {
		logger.Error("job failed", "error", err, "duration_ms", duration.Milliseconds())
		if s.emitter != nil {
			s.emitter("job.failed", "", "", map[string]any{
				"job_type":    string(processor.Type()),
				"error":       err.Error(),
				"duration_ms": duration.Milliseconds(),
			})
		}
	} else {
		logger.Info("job completed",
			"duration_ms", duration.Milliseconds(),
			"items_processed", result.ItemsProcessed,
			"items_affected", result.ItemsAffected,
		)
		if s.emitter != nil && result.ItemsAffected > 0 {
			s.emitter("job.completed", "", "", map[string]any{
				"job_type":        string(processor.Type()),
				"items_processed": result.ItemsProcessed,
				"items_affected":  result.ItemsAffected,
				"duration_ms":     duration.Milliseconds(),
				"details":         result.Details,
			})
		}
	}
}
