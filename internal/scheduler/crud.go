package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/schedule"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/robfig/cron/v3"
)

// CreateSchedule creates a new schedule for the given project.
func CreateSchedule(projectID int, name, prompt, schedType, cronExpr string, intervalSecs int) (*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	builder := client.Schedule.Create().
		SetName(name).
		SetPrompt(prompt).
		SetScheduleType(schedule.ScheduleType(schedType)).
		SetEnabled(true).
		SetProjectID(projectID)

	if schedType == "cron" {
		if cronExpr == "" {
			return nil, fmt.Errorf("cron expression is required for cron schedule")
		}
		// Validate cron expression
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(cronExpr)
		if err != nil {
			return nil, fmt.Errorf("invalid cron expression: %w", err)
		}
		builder = builder.SetCronExpr(cronExpr)
		nextRun := sched.Next(time.Now())
		builder = builder.SetNextRun(nextRun)
	} else if schedType == "interval" {
		if intervalSecs <= 0 {
			return nil, fmt.Errorf("interval_seconds must be positive")
		}
		builder = builder.SetIntervalSeconds(intervalSecs)
		nextRun := time.Now().Add(time.Duration(intervalSecs) * time.Second)
		builder = builder.SetNextRun(nextRun)
	}

	s, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create schedule: %w", err)
	}

	// Reload with project edge
	return GetSchedule(s.ID)
}

// GetSchedule returns a schedule by ID with edges.
func GetSchedule(id int) (*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	s, err := client.Schedule.Query().
		Where(schedule.ID(id)).
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("schedule #%d not found", id)
		}
		return nil, fmt.Errorf("failed to query schedule: %w", err)
	}

	return s, nil
}

// UpdateSchedule updates a schedule.
func UpdateSchedule(id int, name, prompt, schedType, cronExpr string, intervalSecs int, enabled bool) (*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	builder := client.Schedule.UpdateOneID(id).
		SetName(name).
		SetPrompt(prompt).
		SetScheduleType(schedule.ScheduleType(schedType)).
		SetEnabled(enabled)

	if schedType == "cron" {
		if cronExpr == "" {
			return nil, fmt.Errorf("cron expression is required for cron schedule")
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(cronExpr)
		if err != nil {
			return nil, fmt.Errorf("invalid cron expression: %w", err)
		}
		builder = builder.SetCronExpr(cronExpr).ClearIntervalSeconds()
		if enabled {
			nextRun := sched.Next(time.Now())
			builder = builder.SetNextRun(nextRun)
		}
	} else if schedType == "interval" {
		if intervalSecs <= 0 {
			return nil, fmt.Errorf("interval_seconds must be positive")
		}
		builder = builder.SetIntervalSeconds(intervalSecs).ClearCronExpr()
		if enabled {
			nextRun := time.Now().Add(time.Duration(intervalSecs) * time.Second)
			builder = builder.SetNextRun(nextRun)
		}
	}

	if !enabled {
		builder = builder.ClearNextRun()
	}

	s, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("schedule #%d not found", id)
		}
		return nil, fmt.Errorf("failed to update schedule: %w", err)
	}

	_ = s
	return GetSchedule(id)
}

// DeleteSchedule deletes a schedule by ID.
func DeleteSchedule(id int) error {
	ctx := context.Background()
	client := db.Client()

	err := client.Schedule.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("schedule #%d not found", id)
		}
		return fmt.Errorf("failed to delete schedule: %w", err)
	}

	return nil
}

// ListAll returns all schedules with project edges.
func ListAll() ([]*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	schedules, err := client.Schedule.Query().
		WithProject().
		Order(ent.Desc(schedule.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list schedules: %w", err)
	}

	return schedules, nil
}

// ListByProject returns schedules for the given project.
func ListByProject(projectName string) ([]*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	schedules, err := client.Schedule.Query().
		Where(schedule.HasProjectWith(project.Name(projectName))).
		WithProject().
		Order(ent.Desc(schedule.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list schedules: %w", err)
	}

	return schedules, nil
}

// ToggleEnabled toggles the enabled state of a schedule.
func ToggleEnabled(id int, enabled bool) (*ent.Schedule, error) {
	ctx := context.Background()
	client := db.Client()

	builder := client.Schedule.UpdateOneID(id).
		SetEnabled(enabled)

	if enabled {
		// Recalculate next_run
		s, err := GetSchedule(id)
		if err != nil {
			return nil, err
		}
		nextRun := calcNextRun(s)
		if nextRun != nil {
			builder = builder.SetNextRun(*nextRun)
		}
	} else {
		builder = builder.ClearNextRun()
	}

	_, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("schedule #%d not found", id)
		}
		return nil, fmt.Errorf("failed to toggle schedule: %w", err)
	}

	return GetSchedule(id)
}

// calcNextRun calculates the next run time for a schedule.
func calcNextRun(s *ent.Schedule) *time.Time {
	now := time.Now()
	switch s.ScheduleType {
	case schedule.ScheduleTypeCron:
		if s.CronExpr == "" {
			return nil
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(s.CronExpr)
		if err != nil {
			return nil
		}
		next := sched.Next(now)
		return &next
	case schedule.ScheduleTypeInterval:
		if s.IntervalSeconds == 0 {
			return nil
		}
		next := now.Add(time.Duration(s.IntervalSeconds) * time.Second)
		return &next
	}
	return nil
}

// CalcNextRunPreview calculates the next N run times for a cron expression (for UI preview).
func CalcNextRunPreview(cronExpr string, count int) ([]time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}

	times := make([]time.Time, 0, count)
	t := time.Now()
	for i := 0; i < count; i++ {
		t = sched.Next(t)
		times = append(times, t)
	}

	return times, nil
}
