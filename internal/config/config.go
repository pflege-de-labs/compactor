// Package config defines compactor's configuration schema as kong
// flags — populated from CLI args, a YAML config file (via
// github.com/alecthomas/kong-yaml, wired in cmd/compactor/main.go), or
// their `default:` tags, in that order of precedence — plus the XDG
// default config file location.
package config

import (
	"time"

	"github.com/adrg/xdg"
)

type S3 struct {
	Endpoint     string `help:"S3 endpoint (empty = AWS default; set for MinIO, e.g. http://localhost:9000)."`
	Region       string `help:"AWS region." default:"us-east-1"`
	Bucket       string `help:"Bucket name."`
	UsePathStyle bool   `name:"use-path-style" help:"Use path-style S3 addressing (most MinIO deployments)."`
}

type Paths struct {
	SourcePrefix     string `name:"source-prefix" help:"Base prefix under which year/month/day raw events live."`
	RollupPrefix     string `name:"rollup-prefix" help:"Rollup key prefix." default:"rollups"`
	CheckpointPrefix string `name:"checkpoint-prefix" help:"Checkpoint key prefix." default:"checkpoints"`
}

type AgeConfig struct {
	Recipients   []string `help:"age1... recipient public keys."`
	IdentityFile string   `name:"identity-file" help:"Path to age identity file (required for decrypt/materialize)." type:"path"`
}

// Encryption configures the rollup OUTPUT scheme. Source objects are
// detected and decoded independently and may use other schemes; see
// docs/adr/0008-decouple-source-decode-scheme-from-rollup-output-scheme.md.
type Encryption struct {
	Scheme string    `help:"Rollup output scheme." enum:"none,age" default:"none"`
	Age    AgeConfig `embed:"" prefix:"age-"`
}

type Rollup struct {
	StragglerDays int    `name:"straggler-days" help:"Also re-check N prior days on every hourly run." default:"1"`
	WeekStart     string `name:"week-start" help:"Reserved; ISO 8601 (Monday-start) weeks are used today." default:"monday"`
}

type Materialize struct {
	CacheTTL time.Duration `name:"cache-ttl" help:"Materialized scratch file TTL, for the expiry sweep." default:"1h"`
}

type NATSConfig struct {
	URL      string `help:"NATS server URL." default:"nats://127.0.0.1:4222"`
	Stream   string `help:"JetStream stream MinIO's NATS notification target publishes to." default:"minio-events"`
	Subject  string `help:"Filter subject within the stream." default:"minio.events.>"`
	Consumer string `help:"Durable JetStream consumer name." default:"compactor"`
}

// Listen configures `compactor listen`, the long-running event-driven
// mode; `compactor rollup ...` / `materialize` / `checkpoint ...`
// ignore it.
type Listen struct {
	NATS     NATSConfig    `embed:"" prefix:"nats-"`
	HTTPAddr string        `name:"http-addr" help:"Address to serve /healthz and /readyz on." default:":8080"`
	Debounce time.Duration `help:"Coalesce a burst of notifications for one day into a single reconcile." default:"5s"`
}

type Logging struct {
	Level  string `help:"Log level." enum:"debug,info,warn,error" default:"info"`
	Format string `help:"Log format." enum:"text,json" default:"text"`
}

// Config aggregates the parsed flag groups for convenient passing
// around after kong has populated them; see cli.Bootstrap.
type Config struct {
	S3          S3
	Paths       Paths
	Encryption  Encryption
	Rollup      Rollup
	Materialize Materialize
	Listen      Listen
	Logging     Logging
}

// DefaultPath returns $XDG_CONFIG_HOME/compactor/config.yaml, the
// default candidate passed to kong.Configuration in cmd/compactor/main.go.
func DefaultPath() (string, error) {
	return xdg.ConfigFile("compactor/config.yaml")
}
