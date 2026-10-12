package application

import (
	"testing"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	output := NewOutputBuffer()
	attachment := output.Attach(1)
	defer attachment.Close()
	for range 1024 {
		output.Publish(ports.StreamRecord{Kind: ports.StreamStdout, Data: []byte("record")})
	}
	if attachment.Dropped() == 0 {
		t.Fatal("slow subscriber did not report dropped output")
	}
}

func TestOutputRingBoundsAndReportsEviction(t *testing.T) {
	output := NewOutputBuffer()
	for range 300 {
		output.Publish(ports.StreamRecord{Kind: ports.StreamStderr, Data: []byte("x")})
	}
	records, dropped := output.Snapshot(0)
	if len(records) != 256 || dropped != 44 {
		t.Fatalf("records=%d dropped=%d, want 256/44", len(records), dropped)
	}
}
