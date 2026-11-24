package lease

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRenewalStopsAndJoins(t *testing.T) {
	called := make(chan struct{}, 1)
	renewal := StartRenewal(context.Background(), time.Millisecond, func(context.Context) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("renewal callback did not run")
	}
	renewal.Stop(context.Canceled)
	select {
	case <-renewal.Done():
	default:
		t.Fatal("Stop returned before renewal goroutine exited")
	}
	if !errors.Is(context.Cause(renewal.Context()), context.Canceled) {
		t.Fatalf("renewal context cause = %v, want context canceled", context.Cause(renewal.Context()))
	}
	if err := renewal.Err(); err != nil {
		t.Fatalf("stopped renewal error = %v, want nil", err)
	}
}

func TestRenewalReportsLossAndStops(t *testing.T) {
	want := errors.New("lease lost")
	renewal := StartRenewal(context.Background(), time.Millisecond, func(context.Context) error {
		return want
	})
	select {
	case <-renewal.Lost():
	case <-time.After(time.Second):
		t.Fatal("lease loss was not reported")
	}
	if !errors.Is(renewal.Err(), want) {
		t.Fatalf("renewal context cause = %v, want %v", renewal.Err(), want)
	}
	select {
	case <-renewal.Done():
	case <-time.After(time.Second):
		t.Fatal("renewal goroutine did not stop after lease loss")
	}
}

func TestRenewalRetainsFailureWhenCanceledInFlight(t *testing.T) {
	want := errors.New("catalog renewal failed")
	started, release := make(chan struct{}), make(chan struct{})
	renewal := StartRenewal(context.Background(), time.Millisecond, func(context.Context) error {
		close(started)
		<-release
		return want
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renewal callback did not start")
	}
	renewal.Cancel(nil)
	close(release)
	select {
	case <-renewal.Done():
	case <-time.After(time.Second):
		t.Fatal("renewal goroutine did not stop")
	}
	if !errors.Is(renewal.Err(), want) {
		t.Fatalf("renewal error = %v, want %v", renewal.Err(), want)
	}
}

func TestRenewalStopWaitsForInFlightRenewal(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	renewal := StartRenewal(context.Background(), time.Millisecond, func(context.Context) error {
		close(started)
		<-finish
		return nil
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renewal callback did not start")
	}
	stopped := make(chan struct{})
	go func() {
		renewal.Stop(nil)
		close(stopped)
	}()
	select {
	case <-renewal.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the renewal context")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while the renewal callback was still running")
	default:
	}
	close(finish)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the renewal callback finished")
	}
}
