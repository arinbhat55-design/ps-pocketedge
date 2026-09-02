// Package schedule runs recurring and one-time container start/stop
// schedules: a ticker that periodically asks the store which schedules are
// due and dispatches a ContainerActionCommand for each, over the same
// Dispatcher/OpWaiter a REST-triggered action uses.
package schedule

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// tickInterval is how often the scheduler checks for due schedules — fine
// grained enough that a schedule fires within half a minute of its
// intended time, without hammering the database.
const tickInterval = 30 * time.Second

// fireResultTimeout bounds how long the scheduler waits for a fired
// schedule's ContainerOpResult before recording it as failed — shorter
// than the REST API's containerOpTimeout since a scheduled fire has no
// interactive caller waiting on it and firing many schedules on the same
// tick shouldn't back up behind a single unresponsive agent.
const fireResultTimeout = 20 * time.Second

// Scheduler ticks container_schedules, firing any schedule whose
// next_run_at has arrived.
type Scheduler struct {
	log        *slog.Logger
	store      *store.Store
	dispatcher *deploy.Dispatcher
	opWaiter   *deploy.OpWaiter
}

func New(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) *Scheduler {
	return &Scheduler{log: log, store: st, dispatcher: dispatcher, opWaiter: opWaiter}
}

// Run blocks, ticking every tickInterval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	due, err := s.store.DueSchedules(ctx, time.Now())
	if err != nil {
		s.log.Error("failed to load due schedules", "error", err)
		return
	}

	for _, sched := range due {
		go s.fire(ctx, sched)
	}
}

// fire dispatches sched's action to its server, waits (briefly) for the
// outcome to log/record it, and reschedules: a recurring schedule gets its
// next cron-computed run time, a one-time schedule is disabled (see
// store.RecordRun's doc comment). A server that's offline at fire time
// just records a failure — no retry queue in this MVP slice, consistent
// with how deploy.ErrAgentNotConnected is handled elsewhere.
func (s *Scheduler) fire(ctx context.Context, sched store.Schedule) {
	action := agentv1.ContainerAction_CONTAINER_ACTION_STOP
	if sched.Action == "start" {
		action = agentv1.ContainerAction_CONTAINER_ACTION_START
	}

	status := "completed"
	if err := s.dispatchAndWait(ctx, sched.ServerID, sched.ContainerID, action); err != nil {
		status = "failed: " + err.Error()
		s.log.Warn("scheduled container action failed", "schedule_id", sched.ID, "server_id", sched.ServerID, "container_id", sched.ContainerID, "action", sched.Action, "error", err)
	} else {
		s.log.Info("scheduled container action fired", "schedule_id", sched.ID, "server_id", sched.ServerID, "container_id", sched.ContainerID, "action", sched.Action)
	}

	var nextRunAt *time.Time
	if sched.ScheduleType == "recurring" && sched.CronExpr != nil {
		if parsed, err := cron.ParseStandard(*sched.CronExpr); err == nil {
			next := parsed.Next(time.Now())
			nextRunAt = &next
		} else {
			s.log.Error("failed to parse cron expression for reschedule", "schedule_id", sched.ID, "cron_expr", *sched.CronExpr, "error", err)
		}
	}

	if err := s.store.RecordRun(ctx, sched.ID, status, nextRunAt); err != nil {
		s.log.Error("failed to record schedule run", "schedule_id", sched.ID, "error", err)
	}
}

func (s *Scheduler) dispatchAndWait(ctx context.Context, serverID, containerID string, action agentv1.ContainerAction) error {
	requestID, err := auth.RandomToken()
	if err != nil {
		return err
	}

	ch, cleanup := s.opWaiter.Await(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ContainerAction{
			ContainerAction: &agentv1.ContainerActionCommand{
				RequestId:   requestID,
				ServerId:    serverID,
				ContainerId: containerID,
				Action:      action,
			},
		},
	}
	if err := s.dispatcher.Send(serverID, cmd); err != nil {
		return err
	}

	select {
	case result := <-ch:
		if !result.GetSuccess() {
			return errors.New(result.GetErrorMessage())
		}
		return nil
	case <-time.After(fireResultTimeout):
		return errors.New("timed out waiting for agent response")
	case <-ctx.Done():
		return ctx.Err()
	}
}
