package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMailboxMessageJSON(t *testing.T) {
	now := time.Now()
	msg := &MailboxMessage{
		MessageID:   "msg_123",
		FromWorker:  "worker-1",
		ToWorker:    "worker-2",
		Body:        "Please review my changes",
		CreatedAt:   now,
		NotifiedAt:  now.Add(1 * time.Second),
		DeliveredAt: now.Add(2 * time.Second),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal message: %v", err)
	}

	var decoded MailboxMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}

	if decoded.MessageID != msg.MessageID {
		t.Errorf("MessageID mismatch: got %s, want %s", decoded.MessageID, msg.MessageID)
	}

	if decoded.FromWorker != msg.FromWorker {
		t.Errorf("FromWorker mismatch: got %s, want %s", decoded.FromWorker, msg.FromWorker)
	}

	if decoded.ToWorker != msg.ToWorker {
		t.Errorf("ToWorker mismatch: got %s, want %s", decoded.ToWorker, msg.ToWorker)
	}

	if decoded.Body != msg.Body {
		t.Errorf("Body mismatch: got %s, want %s", decoded.Body, msg.Body)
	}

	if !decoded.NotifiedAt.Equal(msg.NotifiedAt) {
		t.Errorf("NotifiedAt mismatch: got %v, want %v", decoded.NotifiedAt, msg.NotifiedAt)
	}

	if !decoded.DeliveredAt.Equal(msg.DeliveredAt) {
		t.Errorf("DeliveredAt mismatch: got %v, want %v", decoded.DeliveredAt, msg.DeliveredAt)
	}
}

func TestUnreadMessageFiltering(t *testing.T) {
	now := time.Now()
	messages := []MailboxMessage{
		{
			MessageID:   "msg_1",
			FromWorker:  "worker-1",
			ToWorker:    "worker-2",
			Body:        "Message 1",
			CreatedAt:   now,
			DeliveredAt: now.Add(1 * time.Second), // Delivered
		},
		{
			MessageID:  "msg_2",
			FromWorker: "worker-1",
			ToWorker:   "worker-2",
			Body:       "Message 2",
			CreatedAt:  now,
			// Not delivered
		},
		{
			MessageID:  "msg_3",
			FromWorker: "worker-3",
			ToWorker:   "worker-2",
			Body:       "Message 3",
			CreatedAt:  now,
			// Not delivered
		},
	}

	var unread []MailboxMessage
	for _, msg := range messages {
		if msg.DeliveredAt.IsZero() {
			unread = append(unread, msg)
		}
	}

	if len(unread) != 2 {
		t.Errorf("Expected 2 unread messages, got %d", len(unread))
	}

	for _, msg := range unread {
		if !msg.DeliveredAt.IsZero() {
			t.Errorf("Unread message %s has DeliveredAt set", msg.MessageID)
		}
	}
}

func TestMessageNotificationFlow(t *testing.T) {
	now := time.Now()
	msg := &MailboxMessage{
		MessageID:  "msg_123",
		FromWorker: "worker-1",
		ToWorker:   "worker-2",
		Body:       "Test message",
		CreatedAt:  now,
	}

	// Initially, no notification or delivery timestamps
	if !msg.NotifiedAt.IsZero() {
		t.Error("NotifiedAt should be zero initially")
	}
	if !msg.DeliveredAt.IsZero() {
		t.Error("DeliveredAt should be zero initially")
	}

	// After notification
	msg.NotifiedAt = now.Add(1 * time.Second)
	if msg.NotifiedAt.IsZero() {
		t.Error("NotifiedAt should be set after notification")
	}
	if !msg.DeliveredAt.IsZero() {
		t.Error("DeliveredAt should still be zero after notification")
	}

	// After delivery
	msg.DeliveredAt = now.Add(2 * time.Second)
	if msg.DeliveredAt.IsZero() {
		t.Error("DeliveredAt should be set after delivery")
	}

	// Delivery should be after notification
	if msg.DeliveredAt.Before(msg.NotifiedAt) {
		t.Error("DeliveredAt should be after NotifiedAt")
	}
}
