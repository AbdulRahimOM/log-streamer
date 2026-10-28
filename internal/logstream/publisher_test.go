package logstream

import (
	"sync"
	"testing"
	"time"
)

func TestRingBufferWraparoundAndChronologicalSnapshot(t *testing.T) {
	p := NewPublisher(3, 4)
	for i := 1; i <= 5; i++ {
		p.Publish(Event{Message: string(rune('0' + i)), Source: "api", Level: "INFO"})
	}
	_, snapshot, high := p.Subscribe(Filter{})
	if high != 5 || len(snapshot) != 3 || snapshot[0].ID != 3 || snapshot[2].ID != 5 {
		t.Fatalf("unexpected snapshot: high=%d entries=%#v", high, snapshot)
	}
}

func TestFilterAppliesToHistoryAndLive(t *testing.T) {
	p := NewPublisher(10, 4)
	p.Publish(Event{Source: "api", Level: "INFO", Origin: "stdout"})
	p.Publish(Event{Source: "worker", Level: "ERROR", Origin: "stderr"})
	sub, snapshot, high := p.Subscribe(Filter{Source: "api", Level: "INFO", Origin: "stdout"})
	defer p.Unsubscribe(sub)
	if len(snapshot) != 1 || snapshot[0].Event.Source != "api" {
		t.Fatalf("unexpected filtered snapshot: %#v", snapshot)
	}
	p.Publish(Event{Source: "worker", Level: "INFO", Origin: "stdout"})
	p.Publish(Event{Source: "api", Level: "INFO", Origin: "stdout"})
	got := <-sub.C
	if got.ID <= high || got.Event.Source != "api" {
		t.Fatalf("unexpected live record: %#v", got)
	}
	select {
	case extra := <-sub.C:
		t.Fatalf("unexpected filtered record: %#v", extra)
	default:
	}
}

func TestMultipleSubscribersReceiveIndependently(t *testing.T) {
	p := NewPublisher(10, 2)
	first, _, _ := p.Subscribe(Filter{Source: "api"})
	second, _, _ := p.Subscribe(Filter{Source: "worker"})
	defer p.Unsubscribe(first)
	defer p.Unsubscribe(second)
	p.Publish(Event{Source: "api", Message: "api record"})
	p.Publish(Event{Source: "worker", Message: "worker record"})
	if got := <-first.C; got.Event.Message != "api record" {
		t.Fatalf("first subscriber got %#v", got)
	}
	if got := <-second.C; got.Event.Message != "worker record" {
		t.Fatalf("second subscriber got %#v", got)
	}
}

func TestFullSubscriberDisconnectsWithoutBlockingPublishers(t *testing.T) {
	p := NewPublisher(10, 1)
	sub, _, _ := p.Subscribe(Filter{})
	p.Publish(Event{Message: "first"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			p.Publish(Event{Message: "next"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher blocked on a full subscriber queue")
	}
	if _, ok := <-sub.C; !ok {
		t.Fatal("queued record was not retained before close")
	}
	if _, ok := <-sub.C; ok {
		t.Fatal("slow subscriber was not disconnected")
	}
	p.Unsubscribe(sub)
}

func TestConcurrentPublishAndAtomicHistoryLiveHandoff(t *testing.T) {
	p := NewPublisher(5000, 5000)
	const total = 200
	for i := 1; i <= total/2; i++ {
		p.Publish(Event{Message: "before"})
	}
	paused := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(paused)
		<-resume
		for i := total/2 + 1; i <= total; i++ {
			p.Publish(Event{Message: "after"})
		}
		close(done)
	}()
	<-paused
	sub, snapshot, high := p.Subscribe(Filter{})
	defer p.Unsubscribe(sub)
	if len(snapshot) != total/2 || high != total/2 {
		t.Fatalf("history boundary mismatch: snapshot=%d high=%d", len(snapshot), high)
	}
	close(resume)
	<-done
	lastID := high
	for i := total/2 + 1; i <= total; i++ {
		got := <-sub.C
		if got.ID != uint64(i) || got.ID != lastID+1 {
			t.Fatalf("handoff gap or duplicate: got ID %d after %d; wanted %d", got.ID, lastID, i)
		}
		lastID = got.ID
	}
}

func TestConcurrentPublishOrdering(t *testing.T) {
	p := NewPublisher(5000, 5000)
	const publishers, perPublisher = 5, 100
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPublisher; j++ {
				p.Publish(Event{Level: "INFO"})
			}
		}()
	}
	wg.Wait()
	sub, snapshot, high := p.Subscribe(Filter{})
	defer p.Unsubscribe(sub)
	if len(snapshot) != publishers*perPublisher || high != publishers*perPublisher {
		t.Fatalf("history lost events: snapshot=%d high=%d", len(snapshot), high)
	}
}
