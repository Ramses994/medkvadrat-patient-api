package service

import (
	"context"
	"sync"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
)

const (
	otpMailQueueSize = 100
	otpMailWorkers   = 2
	otpMailTimeout   = 30 * time.Second
)

type otpMailJob struct {
	requestID string
	email     string
	code      string
}

// otpMailQueue sends login codes after the response, so the answer does not depend on SMTP.
type otpMailQueue struct {
	mu      sync.Mutex
	closed  bool
	jobs    chan otpMailJob
	workers sync.WaitGroup
	pending sync.WaitGroup
}

func (s *Services) otpMail() *otpMailQueue {
	s.mailOnce.Do(func() {
		q := &otpMailQueue{jobs: make(chan otpMailJob, otpMailQueueSize)}
		for i := 0; i < otpMailWorkers; i++ {
			q.workers.Add(1)
			go func() {
				defer q.workers.Done()
				for job := range q.jobs {
					s.sendQueuedOTPMail(job)
					q.pending.Done()
				}
			}()
		}
		s.mailQ = q
	})
	return s.mailQ
}

func (s *Services) enqueueOTPMail(job otpMailJob) {
	q := s.otpMail()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		s.Logger.Error("otp email not queued", "stage", "queue", "code", "stopped", "request_id", job.requestID)
		return
	}
	q.pending.Add(1)
	select {
	case q.jobs <- job:
	default:
		q.pending.Done()
		s.Logger.Error("otp email not queued", "stage", "queue", "code", "queue_full", "request_id", job.requestID)
	}
}

func (s *Services) sendQueuedOTPMail(job otpMailJob) {
	ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
	defer cancel()
	if err := sendOTPMail(ctx, s, job.email, job.code); err != nil {
		s.Logger.Error("otp email send failed", "stage", "smtp_send", "code", otp.SMTPErrorCode(err), "request_id", job.requestID)
	}
}

// StopOTPMail stops taking letters and waits for the queued ones until ctx ends.
func (s *Services) StopOTPMail(ctx context.Context) error {
	q := s.otpMail()
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.jobs)
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() {
		q.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
