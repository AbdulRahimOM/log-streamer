package logstream

import (
	"sync"
	"time"
)

// Filter is shared by historical and live event delivery.
type Filter struct {
	Source string
	Level  string
	Origin string
}

func (f Filter) matches(event Event) bool {
	return (f.Source == "" || event.Source == f.Source) &&
		(f.Level == "" || event.Level == f.Level) &&
		(f.Origin == "" || event.Origin == f.Origin)
}

// Delivery pairs a process-local sequence number with an event.
type Delivery struct {
	ID    uint64
	Event Event
}

type Subscriber struct {
	C      <-chan Delivery
	id     uint64
	filter Filter
	queue  chan Delivery
}

// Publisher owns bounded recent history and fan-out queues. Publish never
// waits for a subscriber; a subscriber that cannot keep up is disconnected.
type Publisher struct {
	mu           sync.Mutex
	history      []Delivery
	capacity     int
	historyStart int
	historyCount int
	queueSize    int
	sequence     uint64
	subscriberID uint64
	subscribers  map[uint64]*Subscriber
}

func NewPublisher(historyCapacity, queueCapacity int) *Publisher {
	if historyCapacity < 1 || queueCapacity < 1 {
		panic("publisher capacities must be positive")
	}
	return &Publisher{
		history: make([]Delivery, historyCapacity), capacity: historyCapacity,
		queueSize: queueCapacity, subscribers: make(map[uint64]*Subscriber),
	}
}

func (p *Publisher) Publish(event Event) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sequence++
	delivery := Delivery{ID: p.sequence, Event: event}
	if p.historyCount < p.capacity {
		index := (p.historyStart + p.historyCount) % p.capacity
		p.history[index] = delivery
		p.historyCount++
	} else {
		p.history[p.historyStart] = delivery
		p.historyStart = (p.historyStart + 1) % p.capacity
	}
	for id, subscriber := range p.subscribers {
		if !subscriber.filter.matches(event) {
			continue
		}
		select {
		case subscriber.queue <- delivery:
		default:
			close(subscriber.queue)
			delete(p.subscribers, id)
		}
	}
	return delivery.ID
}

// Subscribe registers the subscriber and captures history under one lock.
// The returned snapshot is chronological; live records with IDs at or below
// its high-water mark should be suppressed by the consumer.
func (p *Publisher) Subscribe(filter Filter) (*Subscriber, []Delivery, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscriberID++
	queue := make(chan Delivery, p.queueSize)
	subscriber := &Subscriber{id: p.subscriberID, filter: filter, queue: queue, C: queue}
	p.subscribers[subscriber.id] = subscriber
	snapshot := make([]Delivery, 0, p.historyCount)
	for i := 0; i < p.historyCount; i++ {
		delivery := p.history[(p.historyStart+i)%p.capacity]
		if filter.matches(delivery.Event) {
			snapshot = append(snapshot, delivery)
		}
	}
	return subscriber, snapshot, p.sequence
}

func (p *Publisher) Unsubscribe(subscriber *Subscriber) {
	if subscriber == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.subscribers[subscriber.id]; ok && current == subscriber {
		delete(p.subscribers, subscriber.id)
		close(subscriber.queue)
	}
}

func NewEvent(source, origin, raw string) Event {
	return Parse(raw, source, origin, time.Now())
}
