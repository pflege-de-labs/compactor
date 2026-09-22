// Package cli wires compactor's kong command surface to the
// config/storage/crypto/checkpoint/rollup packages.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/alecthomas/kong"

	"github.com/pflege-de-labs/compactor/internal/checkpoint"
	"github.com/pflege-de-labs/compactor/internal/config"
	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/metrics"
	"github.com/pflege-de-labs/compactor/internal/rollup"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

type CLI struct {
	ConfigPath kong.ConfigFlag `name:"config" placeholder:"PATH" help:"Path to YAML config file (default: XDG config dir)."`

	S3             config.S3          `embed:"" prefix:"s3-"`
	Paths          config.Paths       `embed:"" prefix:"paths-"`
	Encryption     config.Encryption  `embed:"" prefix:"encryption-"`
	RollupConfig   config.Rollup      `embed:"" prefix:"rollup-"`
	MaterializeCfg config.Materialize `embed:"" prefix:"materialize-"`
	ListenConfig   config.Listen      `embed:"" prefix:"listen-"`
	Logging        config.Logging     `embed:"" prefix:"logging-"`

	Rollup      RollupCmd      `cmd:"" help:"Roll up source events into day/week/month JSONL."`
	Materialize MaterializeCmd `cmd:"" help:"Decrypt a rollup object into a local scratch file for DuckDB."`
	Checkpoint  CheckpointCmd  `cmd:"" help:"Inspect or reset checkpoint state."`
	Listen      ListenCmd      `cmd:"" help:"Run as a long-lived event-driven daemon (NATS-triggered rollups, health probes)."`

	Man         ManCmd         `cmd:"" help:"Generate the man page."`
	Completions CompletionsCmd `cmd:"" help:"Generate a shell completion script."`

	Version kong.VersionFlag `help:"Print version and exit."`
}

type RollupCmd struct {
	Hourly       RollupHourlyCmd       `cmd:"" help:"Incremental day rollup (current day + configured stragglers)."`
	PromoteWeek  RollupPromoteWeekCmd  `cmd:"" help:"Rebuild a week rollup from its day rollups."`
	PromoteMonth RollupPromoteMonthCmd `cmd:"" help:"Rebuild a month rollup from its week rollups."`
}

// App holds the fully wired dependencies shared by every command,
// built once from config in Bootstrap.
type App struct {
	Config      config.Config
	Store       storage.ObjectStore
	Registry    *crypto.Registry
	Checkpoints checkpoint.Store
	Engine      *rollup.Engine
	Logger      *slog.Logger
	Metrics     *metrics.Recorder
}

// Shutdown flushes metrics before the process exits. Its error is
// deliberately non-fatal to callers: a metrics backend being
// unreachable must never turn an otherwise-successful rollup run into
// a failed one.
func (a *App) Shutdown(ctx context.Context) error {
	return a.Metrics.Shutdown(ctx)
}

// Bootstrap builds every dependency the CLI commands need from root,
// which kong has already populated from flags/YAML/defaults by the
// time this is called (see cmd/compactor/main.go).
func Bootstrap(ctx context.Context, root *CLI) (*App, error) {
	cfg := config.Config{
		S3:          root.S3,
		Paths:       root.Paths,
		Encryption:  root.Encryption,
		Rollup:      root.RollupConfig,
		Materialize: root.MaterializeCfg,
		Listen:      root.ListenConfig,
		Logging:     root.Logging,
	}

	logger := newLogger(cfg.Logging)

	store, err := storage.NewS3Store(ctx, storage.S3Config{
		Endpoint:     cfg.S3.Endpoint,
		Region:       cfg.S3.Region,
		Bucket:       cfg.S3.Bucket,
		UsePathStyle: cfg.S3.UsePathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("cli: bootstrap storage: %w", err)
	}

	registry, err := buildRegistry(cfg.Encryption)
	if err != nil {
		return nil, fmt.Errorf("cli: bootstrap encryption: %w", err)
	}

	metricsRecorder, err := metrics.NewRecorder(ctx, "compactor")
	if err != nil {
		return nil, fmt.Errorf("cli: bootstrap metrics: %w", err)
	}

	checkpoints := checkpoint.NewS3Store(store, cfg.Paths.CheckpointPrefix)

	engine := &rollup.Engine{
		Store:        store,
		Lister:       storage.NewPollLister(store),
		Checkpoints:  checkpoints,
		Registry:     registry,
		OutputScheme: crypto.Scheme(cfg.Encryption.Scheme),
		SourcePrefix: cfg.Paths.SourcePrefix,
		RollupPrefix: cfg.Paths.RollupPrefix,
		Clock:        time.Now,
		Logger:       logger,
	}

	return &App{
		Config:      cfg,
		Store:       store,
		Registry:    registry,
		Checkpoints: checkpoints,
		Engine:      engine,
		Logger:      logger,
		Metrics:     metricsRecorder,
	}, nil
}

// sourceAESCTRGzipKeyEnv, if set, enables decoding a source format some
// upstream producers use (gzip-compressed JSONL, AES-CTR encrypted,
// IV embedded in the filename — see crypto.AESCTRGzipCodec). It's an
// env var rather than a YAML field so the key material never has to
// live in a config file; the same Kubernetes Secret the producer uses
// can be mounted into compactor's pod under this name.
const sourceAESCTRGzipKeyEnv = "COMPACTOR_SOURCE_AES_CTR_GZIP_KEY"

func buildRegistry(enc config.Encryption) (*crypto.Registry, error) {
	codecs := []crypto.Codec{crypto.NoopCodec{}}

	if enc.Scheme == "age" || len(enc.Age.Recipients) > 0 {
		ageCodec, err := crypto.NewAgeCodec(enc.Age.Recipients, enc.Age.IdentityFile)
		if err != nil {
			return nil, fmt.Errorf("age codec: %w", err)
		}
		codecs = append(codecs, ageCodec)
	}

	if hexKey := os.Getenv(sourceAESCTRGzipKeyEnv); hexKey != "" {
		aesCodec, err := crypto.NewAESCTRGzipCodec(hexKey)
		if err != nil {
			return nil, fmt.Errorf("aes-ctr-gzip source codec: %w", err)
		}
		codecs = append(codecs, aesCodec)
	}

	return crypto.NewRegistry(codecs...), nil
}

func newLogger(cfg config.Logging) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}
