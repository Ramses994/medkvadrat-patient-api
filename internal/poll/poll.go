package poll

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
)

type MotconsuPoller struct {
	svc    *service.Services
	logger *slog.Logger

	interval time.Duration

	mu             sync.RWMutex
	lastModifyDate time.Time
	seenTalons     map[int]struct{}

	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewMotconsuPoller(svc *service.Services, logger *slog.Logger, interval time.Duration) *MotconsuPoller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &MotconsuPoller{
		svc:        svc,
		logger:     logger,
		interval:   interval,
		seenTalons: make(map[int]struct{}),
		stopCh:     make(chan struct{}),
	}
}

func (p *MotconsuPoller) Start(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	t, err := p.svc.MaxModifyDate(initCtx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.lastModifyDate = t
	p.mu.Unlock()

	p.logger.Info("motconsu polling started", "since", t.Format("2006-01-02 15:04:05"), "interval", p.interval.String())

	go p.loop(ctx)
	return nil
}

func (p *MotconsuPoller) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
}

func (p *MotconsuPoller) loop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *MotconsuPoller) tick(ctx context.Context) {
	p.mu.RLock()
	last := p.lastModifyDate
	p.mu.RUnlock()

	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := p.svc.PollAfter(qCtx, last)
	if err != nil {
		p.logger.Warn("polling failed", "err", err)
		return
	}

	newLast := last
	for _, r := range rows {
		if _, exists := p.seenTalons[r.MotconsuID]; !exists {
			p.logger.Info(
				"motconsu changed",
				"motconsu_id", r.MotconsuID,
				"patient", r.PatientNom+" "+r.PatientPrenom,
				"doctor", r.DoctorNom+" "+r.DoctorPrenom,
				"date", r.DateConsult.Format("02.01.2006 15:04"),
			)
			p.seenTalons[r.MotconsuID] = struct{}{}
		}
		if r.ModifyDate.After(newLast) {
			newLast = r.ModifyDate
		}
	}

	if newLast.After(last) {
		p.mu.Lock()
		p.lastModifyDate = newLast
		p.mu.Unlock()
	}
}
