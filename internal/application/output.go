package application

import (
	"sync"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

const (
	outputRecordLimit = 256
	outputByteLimit   = 1 << 20
)

type OutputEvent struct {
	Sequence uint64
	Record   ports.StreamRecord
}

type outputSubscriber struct {
	events  chan OutputEvent
	dropped uint64
}

type OutputBuffer struct {
	mu          sync.Mutex
	records     []OutputEvent
	bytes       int
	dropped     uint64
	next        uint64
	nextSub     uint64
	subscribers map[uint64]*outputSubscriber
}

type OutputAttachment struct {
	buffer *OutputBuffer
	id     uint64
	Events <-chan OutputEvent
}

func NewOutputBuffer() *OutputBuffer {
	return &OutputBuffer{subscribers: make(map[uint64]*outputSubscriber)}
}

func (b *OutputBuffer) Publish(record ports.StreamRecord) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	event := OutputEvent{Sequence: b.next}
	if len(record.Data) > outputByteLimit {
		b.dropped++
		for _, sub := range b.subscribers {
			sub.dropped++
		}
		return event.Sequence
	}
	record.Data = append([]byte(nil), record.Data...)
	event.Record = record
	for len(b.records) > 0 && (len(b.records) >= outputRecordLimit || b.bytes+len(record.Data) > outputByteLimit) {
		b.bytes -= len(b.records[0].Record.Data)
		b.records = b.records[1:]
		b.dropped++
	}
	b.records = append(b.records, event)
	b.bytes += len(record.Data)
	for _, sub := range b.subscribers {
		copyEvent := event
		copyEvent.Record.Data = append([]byte(nil), event.Record.Data...)
		select {
		case sub.events <- copyEvent:
		default:
			sub.dropped++
		}
	}
	return event.Sequence
}

func (b *OutputBuffer) Snapshot(after uint64) ([]OutputEvent, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.records) == 0 {
		return nil, b.dropped
	}
	oldest := b.records[0].Sequence
	var dropped uint64
	if after < oldest && oldest-after > 1 {
		dropped = oldest - after - 1
	}
	var records []OutputEvent
	for _, event := range b.records {
		if event.Sequence > after {
			copyEvent := event
			copyEvent.Record.Data = append([]byte(nil), event.Record.Data...)
			records = append(records, copyEvent)
		}
	}
	return records, dropped
}

func (b *OutputBuffer) Attach(buffer int) *OutputAttachment {
	if buffer < 1 {
		buffer = 1
	}
	if buffer > outputRecordLimit {
		buffer = outputRecordLimit
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSub++
	sub := &outputSubscriber{events: make(chan OutputEvent, buffer)}
	b.subscribers[b.nextSub] = sub
	return &OutputAttachment{buffer: b, id: b.nextSub, Events: sub.events}
}

func (a *OutputAttachment) Dropped() uint64 {
	a.buffer.mu.Lock()
	defer a.buffer.mu.Unlock()
	if sub := a.buffer.subscribers[a.id]; sub != nil {
		return sub.dropped
	}
	return 0
}

func (a *OutputAttachment) Close() {
	a.buffer.mu.Lock()
	defer a.buffer.mu.Unlock()
	if sub := a.buffer.subscribers[a.id]; sub != nil {
		delete(a.buffer.subscribers, a.id)
		close(sub.events)
	}
}
