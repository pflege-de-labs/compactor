package notify

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSConfig configures the JetStream stream/consumer compactor reads
// MinIO bucket notifications from. The stream and its NATS notification
// target are expected to already exist (provisioned alongside MinIO);
// compactor only creates/reuses a durable pull consumer on it.
type NATSConfig struct {
	URL      string
	Stream   string
	Subject  string // filter subject within Stream, e.g. "minio.events.>"
	Consumer string // durable consumer name
}

// NATSSubscriber consumes bucket notification messages from a
// JetStream stream.
type NATSSubscriber struct {
	conn     *nats.Conn
	consumer jetstream.Consumer
}

// ConnectNATS connects to NATS and creates (or reuses) a durable pull
// consumer on cfg.Stream filtered to cfg.Subject.
func ConnectNATS(ctx context.Context, cfg NATSConfig) (*NATSSubscriber, error) {
	nc, err := nats.Connect(cfg.URL, nats.Name("compactor"))
	if err != nil {
		return nil, fmt.Errorf("notify: connect nats %s: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("notify: init jetstream: %w", err)
	}

	stream, err := js.Stream(ctx, cfg.Stream)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("notify: get stream %q: %w", cfg.Stream, err)
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       cfg.Consumer,
		FilterSubject: cfg.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("notify: create consumer %q on stream %q: %w", cfg.Consumer, cfg.Stream, err)
	}

	return &NATSSubscriber{conn: nc, consumer: consumer}, nil
}

// Subscribe consumes messages until ctx is done, calling handler for
// every parsed ObjectCreated Event found in each message. A message is
// Nak'd (triggering JetStream redelivery) if parsing fails or handler
// returns an error for any event in it; otherwise it's Ack'd.
func (s *NATSSubscriber) Subscribe(ctx context.Context, handler func(Event) error) error {
	consumeCtx, err := s.consumer.Consume(func(msg jetstream.Msg) {
		events, err := ParseEvents(msg.Data())
		if err != nil {
			_ = msg.Nak()
			return
		}
		for _, ev := range events {
			if err := handler(ev); err != nil {
				_ = msg.Nak()
				return
			}
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("notify: start consume: %w", err)
	}
	defer consumeCtx.Stop()

	<-ctx.Done()
	return nil
}

// Close releases the underlying NATS connection.
func (s *NATSSubscriber) Close() {
	s.conn.Close()
}
