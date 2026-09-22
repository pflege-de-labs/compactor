// Package notify parses and consumes MinIO/S3-compatible bucket
// notification events, so compactor can react to new objects promptly
// instead of waiting for the next scheduled poll.
package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Event is a normalized object-created notification. MinIO reuses the
// same S3 event schema across every notification target (webhook,
// Kafka, NATS, AMQP, Redis, ...), so this parser is shared regardless
// of transport.
type Event struct {
	Bucket string
	Key    string
	Name   string
	Time   time.Time
}

type s3EventPayload struct {
	Records []s3EventRecord `json:"Records"`
}

type s3EventRecord struct {
	EventName string `json:"eventName"`
	EventTime string `json:"eventTime"`
	S3        struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key string `json:"key"`
		} `json:"object"`
	} `json:"s3"`
}

// ParseEvents parses a bucket notification payload into Events,
// keeping only ObjectCreated records (ObjectRemoved and others are not
// relevant to rollup — a removed source object doesn't need
// reconciling). Object keys are URL-decoded per the S3 event spec.
func ParseEvents(data []byte) ([]Event, error) {
	var payload s3EventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("notify: parse event payload: %w", err)
	}

	var events []Event
	for _, rec := range payload.Records {
		if !strings.HasPrefix(rec.EventName, "s3:ObjectCreated:") {
			continue
		}

		key, err := url.QueryUnescape(rec.S3.Object.Key)
		if err != nil {
			key = rec.S3.Object.Key
		}

		t, _ := time.Parse(time.RFC3339, rec.EventTime)
		events = append(events, Event{
			Bucket: rec.S3.Bucket.Name,
			Key:    key,
			Name:   rec.EventName,
			Time:   t,
		})
	}
	return events, nil
}
