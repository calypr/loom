package lease

import (
	"context"
	"time"
)

type Renewal struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
	lost   chan struct{}
	err    error
}

func StartRenewal(parent context.Context, interval time.Duration, renew func(context.Context) error) *Renewal {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	r := &Renewal{ctx: ctx, cancel: cancel, done: make(chan struct{}), lost: make(chan struct{})}
	go r.run(interval, renew)
	return r
}

func (r *Renewal) run(interval time.Duration, renew func(context.Context) error) {
	defer close(r.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if err := renew(r.ctx); err != nil {
				r.err = err
				r.cancel(err)
				close(r.lost)
				return
			}
		}
	}
}

func (r *Renewal) Context() context.Context {
	return r.ctx
}

func (r *Renewal) Lost() <-chan struct{} {
	return r.lost
}

func (r *Renewal) Err() error {
	select {
	case <-r.lost:
		return r.err
	default:
		return nil
	}
}

func (r *Renewal) Cancel(cause error) {
	r.cancel(cause)
}

func (r *Renewal) Done() <-chan struct{} {
	return r.done
}

func (r *Renewal) Stop(cause error) {
	r.Cancel(cause)
	<-r.done
}
