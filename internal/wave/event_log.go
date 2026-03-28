package wave

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// EventLog writes events as JSONL to a file and notifies subscribers.
type EventLog struct {
	path        string
	file        *os.File
	mu          sync.Mutex
	subscribers []chan Event
}

// NewEventLog opens (or creates) the file at path for append-only writes.
func NewEventLog(path string) (*EventLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &EventLog{path: path, file: f}, nil
}

// Emit marshals event as a JSON line, writes it to the file, and notifies
// all subscribers (non-blocking: slow subscribers miss events).
func (el *EventLog) Emit(event Event) error {
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}

	el.mu.Lock()
	defer el.mu.Unlock()

	if _, err := el.file.Write(append(b, '\n')); err != nil {
		return err
	}

	for _, ch := range el.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	return nil
}

// Subscribe returns a buffered channel that receives emitted events.
func (el *EventLog) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	el.mu.Lock()
	el.subscribers = append(el.subscribers, ch)
	el.mu.Unlock()
	return ch
}

// Query reads the JSONL file and returns events at or after since whose type
// is in types (empty types slice means all types).
func (el *EventLog) Query(since time.Time, types []EventType) ([]Event, error) {
	f, err := os.Open(el.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	typeSet := make(map[EventType]struct{}, len(types))
	for _, t := range types {
		typeSet[t] = struct{}{}
	}

	var result []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Timestamp.Before(since) {
			continue
		}
		if len(typeSet) > 0 {
			if _, ok := typeSet[ev.Type]; !ok {
				continue
			}
		}
		result = append(result, ev)
	}
	return result, scanner.Err()
}

// Close closes all subscriber channels and the underlying file.
func (el *EventLog) Close() error {
	el.mu.Lock()
	defer el.mu.Unlock()

	for _, ch := range el.subscribers {
		close(ch)
	}
	el.subscribers = nil

	return el.file.Close()
}
