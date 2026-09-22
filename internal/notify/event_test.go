package notify_test

import (
	"testing"
	"time"

	"github.com/pflege-de-labs/compactor/internal/notify"
)

func TestParseEvents_ObjectCreatedIsParsedAndKeyIsURLDecoded(t *testing.T) {
	payload := []byte(`{
		"Records": [{
			"eventName": "s3:ObjectCreated:Put",
			"eventTime": "2026-09-22T11:06:00.000Z",
			"s3": {
				"bucket": {"name": "events"},
				"object": {"key": "2026/09/22/abc%20def.json"}
			}
		}]
	}`)

	events, err := notify.ParseEvents(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.Bucket != "events" {
		t.Errorf("bucket = %q, want %q", got.Bucket, "events")
	}
	if got.Key != "2026/09/22/abc def.json" {
		t.Errorf("key = %q, want URL-decoded %q", got.Key, "2026/09/22/abc def.json")
	}
	wantTime := time.Date(2026, 9, 22, 11, 6, 0, 0, time.UTC)
	if !got.Time.Equal(wantTime) {
		t.Errorf("time = %v, want %v", got.Time, wantTime)
	}
}

func TestParseEvents_NonCreatedRecordsAreIgnored(t *testing.T) {
	payload := []byte(`{
		"Records": [
			{"eventName": "s3:ObjectRemoved:Delete", "s3": {"bucket": {"name": "events"}, "object": {"key": "x.json"}}},
			{"eventName": "s3:ObjectCreated:Post", "s3": {"bucket": {"name": "events"}, "object": {"key": "y.json"}}}
		]
	}`)

	events, err := notify.ParseEvents(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 || events[0].Key != "y.json" {
		t.Fatalf("expected only the ObjectCreated record, got %+v", events)
	}
}

func TestParseEvents_InvalidJSON(t *testing.T) {
	if _, err := notify.ParseEvents([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}
