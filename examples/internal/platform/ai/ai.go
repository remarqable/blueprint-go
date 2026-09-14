// Package ai puts every model call behind one interface. Model output is
// untrusted input: it is validated against a schema before it reaches storage.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type Request struct {
	Model      string
	PromptName string
	PromptHash string
	System     string
	Messages   []Message
	MaxTokens  int
	TenantID   int64
	TraceID    string
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
}

type Response struct {
	Text       string
	Raw        json.RawMessage
	Usage      Usage
	Model      string
	StopReason string
	Latency    time.Duration
}

type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Models come from config, never inline at a call site. Pin exact ids: an
// alias can change behaviour under you with no deploy on your side.
type Models struct {
	Fast     string // high-volume classification, routing
	Standard string // extraction, summarisation -- the default
	Deep     string // ambiguity, multi-step reasoning, user-facing answers
}

func DefaultModels() Models {
	return Models{
		Fast:     "claude-haiku-4-5-20251001",
		Standard: "claude-sonnet-5",
		Deep:     "claude-opus-5",
	}
}

// ExtractedFact is one claim the model made about a conversation.
type ExtractedFact struct {
	Kind               string  `json:"kind"`
	Subject            string  `json:"subject"`
	Confidence         float64 `json:"confidence"`
	EvidenceMessageIDs []int64 `json:"evidence_message_ids"`
}

var validKinds = map[string]bool{
	"request": true, "decision": true, "resource": true, "topic": true,
}

// Validate rejects model output that is well-formed JSON but wrong for the
// domain. Reject, never repair: a coerced fact is a fabricated one.
func (f ExtractedFact) Validate(supplied map[int64]bool) error {
	if !validKinds[f.Kind] {
		return fmt.Errorf("unknown kind %q", f.Kind)
	}
	if f.Confidence < 0 || f.Confidence > 1 {
		return fmt.Errorf("confidence %v out of range", f.Confidence)
	}
	if len(f.EvidenceMessageIDs) == 0 {
		return fmt.Errorf("fact without evidence")
	}
	// A model asked to cite evidence will sometimes cite a plausible-looking
	// id it never saw. An unverifiable citation is a fabricated one.
	for _, id := range f.EvidenceMessageIDs {
		if !supplied[id] {
			return fmt.Errorf("cited message %d was not in the input", id)
		}
	}
	return nil
}

// Call is the cost ledger. Every call is recorded, not a sample: unmetered
// inference is how a free tier becomes a liability.
type Call struct {
	ID          int64  `gorm:"primaryKey"`
	TenantID    *int64 `gorm:"index:idx_ai_call_tenant_day,priority:1"`
	Purpose     string `gorm:"not null"`
	Model       string `gorm:"not null"`
	PromptName  string
	PromptHash  string
	InputTokens int
	OutputToken int
	CostMicros  int64 // integer millionths; never float money
	LatencyMS   int
	OK          bool
	Error       string
	TraceID     string
	CreatedAt   time.Time `gorm:"index:idx_ai_call_tenant_day,priority:2,sort:desc"`
}

func (Call) TableName() string { return "ai_call" }
