package application

import (
	"context"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

const maxSchedulerWorkers = 4

type Scheduler struct {
	store       ports.SessionStore
	coordinator *Coordinator
	clock       ports.Clock
	jitter      ports.Jitter
	limit       int
}

func NewScheduler(store ports.SessionStore, coordinator *Coordinator, clock ports.Clock, jitter ports.Jitter, limit int) *Scheduler {
	if limit <= 0 || limit > maxSchedulerWorkers {
		limit = maxSchedulerWorkers
	}
	coordinator.setJitter(jitter)
	return &Scheduler{store: store, coordinator: coordinator, clock: clock, jitter: jitter, limit: limit}
}

func (s *Scheduler) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := make(chan struct{}, s.limit)
	wake := make(chan struct{}, 1)

	for {
		if runCtx.Err() != nil {
			for len(workers) > 0 {
				<-workers
			}
			return runCtx.Err()
		}
		available := s.limit - len(workers)
		if available > 0 {
			mode := domain.ModeManaged
			candidates, err := s.store.List(runCtx, ports.SessionFilter{Mode: &mode, DueOnly: true, Now: s.clock.Now(), Limit: available})
			if err != nil {
				cancel()
				for len(workers) > 0 {
					<-workers
				}
				if runCtx.Err() != nil {
					return runCtx.Err()
				}
				return err
			}
			for _, candidate := range candidates {
				if runCtx.Err() != nil {
					break
				}
				workers <- struct{}{}
				go func(sessionID string) {
					defer func() { <-workers }()
					if err := s.coordinator.Resume(runCtx, sessionID, false); err == nil {
						select {
						case wake <- struct{}{}:
						default:
						}
					}
				}(candidate.ID)
			}
		}

		timer := s.clock.NewTimer(s.pollInterval())
		select {
		case <-runCtx.Done():
			timer.Stop()
			for len(workers) > 0 {
				<-workers
			}
			return runCtx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C():
		}
	}
}

func (s *Scheduler) pollInterval() time.Duration {
	d := 30 * time.Second
	if s.jitter != nil {
		v := s.jitter.Float64()
		if v < 0 {
			v = 0
		} else if v > 1 {
			v = 1
		}
		d -= time.Duration(v * float64(5*time.Second))
	}
	return d
}
