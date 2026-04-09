// Package moderator implements an automated dispatcher and runner for
// Kanban-style tasks targeting OpenClaw instances. It is intentionally
// dependency-inverted: it imports only stdlib and defines narrow interfaces
// (ports) for everything it needs from the rest of claworc. Adapters wiring
// these ports to sshproxy / database / llmgateway live OUTSIDE this package
// and are passed in via moderator.New.
//
// This isolation makes the moderator independently testable with fakes and
// keeps it portable — it could in principle be lifted into its own Go module
// without changes.
package moderator

import (
	"context"
	"time"
)

// ---- DTOs (plain values, no GORM tags) ---------------------------------

type Board struct {
	ID                uint
	Name              string
	Description       string
	EligibleInstances []uint
}

type Task struct {
	ID                   uint
	BoardID              uint
	Title                string
	Description          string
	Status               string
	AssignedInstanceID   *uint
	OpenClawSessionID    string
	OpenClawRunID        string
	EvaluatorProviderKey string
	EvaluatorModel       string
}

type Comment struct {
	ID                uint
	TaskID            uint
	Kind              string // routing|assistant|tool|moderator|evaluation|error
	Author            string
	Body              string
	OpenClawSessionID string
}

type Artifact struct {
	ID          uint
	TaskID      uint
	Path        string
	SizeBytes   int64
	SHA256      string
	StoragePath string
}

type Soul struct {
	InstanceID uint
	Summary    string
	Skills     []string
	UpdatedAt  time.Time
}

type FileEntry struct {
	Path    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

// ---- Ports -------------------------------------------------------------

// GatewayDialer opens an authenticated connection to an OpenClaw instance's
// gateway WebSocket and returns it ready for chat.send / lifecycle frames.
type GatewayDialer interface {
	Dial(ctx context.Context, instanceID uint, sessionKey string) (GatewayConn, error)
}

// GatewayConn is a thin abstraction over a JSON WebSocket so the runner can
// be tested with a fake.
type GatewayConn interface {
	Send(ctx context.Context, frame []byte) error
	Recv(ctx context.Context) ([]byte, error)
	Close() error
}

// WorkspaceFS reads and writes files inside an OpenClaw instance's workspace
// via whatever channel the host wires up (typically SSH exec).
type WorkspaceFS interface {
	List(ctx context.Context, instanceID uint, dir string) ([]FileEntry, error)
	Read(ctx context.Context, instanceID uint, path string) ([]byte, error)
	Write(ctx context.Context, instanceID uint, path string, data []byte) error
	MkdirAll(ctx context.Context, instanceID uint, dir string) error
	RemoveAll(ctx context.Context, instanceID uint, path string) error
}

// LLMClient is the moderator's own LLM call path (for ranking, summarizing,
// and evaluating). It is independent from whatever model the OpenClaw run
// itself uses.
type LLMClient interface {
	Complete(ctx context.Context, providerKey, model, prompt string) (string, error)
}

// Store is the moderator's persistence port. The moderator never sees a
// *gorm.DB directly.
type Store interface {
	GetTask(ctx context.Context, id uint) (Task, error)
	UpdateTask(ctx context.Context, id uint, fields map[string]any) error
	GetBoard(ctx context.Context, id uint) (Board, error)

	InsertComment(ctx context.Context, c Comment) (uint, error)
	SetCommentBody(ctx context.Context, id uint, body string) error
	ListComments(ctx context.Context, taskID uint) ([]Comment, error)

	InsertArtifact(ctx context.Context, a Artifact) error
	ListTaskArtifacts(ctx context.Context, taskID uint) ([]Artifact, error)

	GetSouls(ctx context.Context, instanceIDs []uint) ([]Soul, error)
	UpsertSoul(ctx context.Context, s Soul) error
}

// Settings exposes the moderator's tunable knobs (read from the settings
// table by the adapter).
type Settings interface {
	ModeratorProvider() (key, model string)
	SummaryInterval() time.Duration
	ArtifactMaxBytes() int64
	ArtifactStorageDir() string
	WorkspaceDir() string   // e.g. "/home/claworc/.openclaw/workspace"
	TaskOutcomeDir() string // base dir on instance for task outputs, default "/home/claworc/tasks"
}

// InstanceLister enumerates known instance IDs (used by the periodic
// summarizer).
type InstanceLister interface {
	ListInstanceIDs(ctx context.Context) ([]uint, error)
	InstanceName(ctx context.Context, id uint) (string, error)
}
