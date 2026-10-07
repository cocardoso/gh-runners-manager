package events

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func ev(msg string) store.Event { return store.Event{Seq: 1, Kind: "k", Level: "info", Message: msg} }

func recv(t *testing.T, c <-chan store.Event) store.Event {
	t.Helper()
	select {
	case e := <-c:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return store.Event{}
	}
}

func TestBusFanOut(t *testing.T) {
	b := NewBus()
	s1, s2 := b.Subscribe(4), b.Subscribe(4)
	defer s1.Close()
	defer s2.Close()
	b.Publish(ev("hello"))
	if recv(t, s1.C).Message != "hello" || recv(t, s2.C).Message != "hello" {
		t.Fatal("both subscribers must receive the event")
	}
}

func TestSlowSubscriberIsMarkedLaggedNotBlocking(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(1)
	defer s.Close()
	done := make(chan struct{})
	go func() {
		for range 3 {
			b.Publish(ev("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	if !s.Lagged() {
		t.Fatal("subscriber must be marked lagged")
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(4)
	s.Close()
	b.Publish(ev("after close"))
	if _, ok := <-s.C; ok {
		t.Fatal("closed subscription must not receive events")
	}
}

func TestRecorderPersistsBeforePublish(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := NewBus()
	sub := b.Subscribe(4)
	defer sub.Close()
	r := NewRecorder(st, b, time.Now)
	got, err := r.Info(context.Background(), "environment.created", "created", Refs{EnvironmentID: "e1"}, map[string]any{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	pub := recv(t, sub.C)
	if pub.Seq != got.Seq || pub.Seq == 0 {
		t.Fatalf("published seq %d, recorded %d", pub.Seq, got.Seq)
	}
	stored, _ := st.ListEvents(context.Background(), store.EventFilter{EnvironmentID: "e1"})
	if len(stored) != 1 || stored[0].Seq != pub.Seq || stored[0].Level != "info" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestConcurrentRecordsArePublishedInSequenceOrder(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := NewBus()
	sub := b.Subscribe(4096)
	defer sub.Close()
	r := NewRecorder(st, b, time.Now)
	// Delay the publication of the first event so a later one could overtake it.
	testHookAfterAppend = func(seq int64) {
		if seq == 1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	defer func() { testHookAfterAppend = nil }()
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			for range 50 {
				_, _ = r.Info(context.Background(), "k", "m", Refs{}, nil)
			}
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	var last int64
	for range 400 {
		e := recv(t, sub.C)
		if e.Seq != last+1 {
			t.Fatalf("published seq %d after %d: out of order", e.Seq, last)
		}
		last = e.Seq
	}
}
