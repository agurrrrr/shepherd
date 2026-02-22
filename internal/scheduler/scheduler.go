package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/schedule"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/robfig/cron/v3"
)

// Scheduler checks enabled schedules and creates tasks when due.
type Scheduler struct {
	interval time.Duration
	stopCh   chan struct{}
	running  bool
	mu       sync.Mutex

	// Callback: when a schedule triggers a new task
	OnScheduleTriggered func(scheduleID int, scheduleName, projectName, prompt string, taskID int)
}

// New creates a new Scheduler that checks schedules at the given interval.
func New(interval time.Duration) *Scheduler {
	return &Scheduler{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the background schedule checking loop.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// Initialize next_run for all enabled schedules that don't have one
	s.initNextRuns()

	go s.processLoop()
}

// Stop halts the background schedule checking loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
}

// IsRunning returns whether the scheduler is running.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scheduler) processLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkSchedules()
		case <-s.stopCh:
			return
		}
	}
}

// checkSchedules finds due schedules and creates tasks.
func (s *Scheduler) checkSchedules() {
	ctx := context.Background()
	client := db.Client()

	now := time.Now()

	// Query enabled schedules where next_run <= now
	dueSchedules, err := client.Schedule.Query().
		Where(
			schedule.EnabledEQ(true),
			schedule.NextRunNotNil(),
			schedule.NextRunLTE(now),
		).
		WithProject(func(q *ent.ProjectQuery) {
			q.WithSheep()
		}).
		All(ctx)
	if err != nil {
		log.Printf("[scheduler] failed to query due schedules: %v", err)
		return
	}

	for _, sched := range dueSchedules {
		s.triggerSchedule(ctx, client, sched, now)
	}
}

// triggerSchedule creates a task from a schedule and updates next_run.
func (s *Scheduler) triggerSchedule(ctx context.Context, client *ent.Client, sched *ent.Schedule, now time.Time) {
	proj := sched.Edges.Project
	if proj == nil {
		log.Printf("[scheduler] schedule #%d has no project, skipping", sched.ID)
		return
	}

	// Find the sheep assigned to this project
	sheepEntity := proj.Edges.Sheep
	if sheepEntity == nil {
		log.Printf("[scheduler] project '%s' has no sheep assigned, skipping schedule '%s'", proj.Name, sched.Name)
		return
	}

	// Create task
	prompt := "[스케줄: " + sched.Name + "] " + sched.Prompt
	task, err := queue.CreateTask(prompt, sheepEntity.ID, proj.ID)
	if err != nil {
		log.Printf("[scheduler] failed to create task for schedule '%s': %v", sched.Name, err)
		return
	}

	// Update last_run and next_run
	nextRun := s.calcNext(sched, now)
	update := client.Schedule.UpdateOneID(sched.ID).
		SetLastRun(now)
	if nextRun != nil {
		update = update.SetNextRun(*nextRun)
	} else {
		update = update.ClearNextRun()
	}
	if _, err := update.Save(ctx); err != nil {
		log.Printf("[scheduler] failed to update schedule '%s' next_run: %v", sched.Name, err)
	}

	// Callback
	if s.OnScheduleTriggered != nil {
		s.OnScheduleTriggered(sched.ID, sched.Name, proj.Name, sched.Prompt, task.ID)
	}

	log.Printf("[scheduler] triggered schedule '%s' → task #%d for project '%s'", sched.Name, task.ID, proj.Name)
}

// calcNext returns the next run time after now for the given schedule.
func (s *Scheduler) calcNext(sched *ent.Schedule, after time.Time) *time.Time {
	switch sched.ScheduleType {
	case schedule.ScheduleTypeCron:
		if sched.CronExpr == "" {
			return nil
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		cronSched, err := parser.Parse(sched.CronExpr)
		if err != nil {
			return nil
		}
		next := cronSched.Next(after)
		return &next
	case schedule.ScheduleTypeInterval:
		if sched.IntervalSeconds == 0 {
			return nil
		}
		next := after.Add(time.Duration(sched.IntervalSeconds) * time.Second)
		return &next
	}
	return nil
}

// initNextRuns sets next_run for enabled schedules that don't have one.
func (s *Scheduler) initNextRuns() {
	ctx := context.Background()
	client := db.Client()

	schedules, err := client.Schedule.Query().
		Where(
			schedule.EnabledEQ(true),
			schedule.NextRunIsNil(),
		).
		All(ctx)
	if err != nil {
		log.Printf("[scheduler] failed to query schedules for init: %v", err)
		return
	}

	now := time.Now()
	for _, sched := range schedules {
		entSched := &ent.Schedule{
			ScheduleType:    sched.ScheduleType,
			CronExpr:        sched.CronExpr,
			IntervalSeconds: sched.IntervalSeconds,
		}
		next := s.calcNext(entSched, now)
		if next != nil {
			if _, err := client.Schedule.UpdateOneID(sched.ID).
				SetNextRun(*next).
				Save(ctx); err != nil {
				log.Printf("[scheduler] failed to init next_run for schedule #%d: %v", sched.ID, err)
			}
		}
	}
}

// RunNow immediately creates a task from a schedule (manual trigger).
func RunNow(scheduleID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	sched, err := client.Schedule.Query().
		Where(schedule.ID(scheduleID)).
		WithProject(func(q *ent.ProjectQuery) {
			q.WithSheep()
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, err
		}
		return nil, err
	}

	proj := sched.Edges.Project
	if proj == nil {
		return nil, err
	}

	sheepEntity := proj.Edges.Sheep
	if sheepEntity == nil || sheepEntity.Status != sheep.StatusIdle {
		if sheepEntity == nil {
			return nil, nil
		}
	}

	prompt := "[스케줄: " + sched.Name + "] " + sched.Prompt
	task, err := queue.CreateTask(prompt, sheepEntity.ID, proj.ID)
	if err != nil {
		return nil, err
	}

	// Update last_run
	now := time.Now()
	_, _ = client.Schedule.UpdateOneID(sched.ID).
		SetLastRun(now).
		Save(ctx)

	return task, nil
}
