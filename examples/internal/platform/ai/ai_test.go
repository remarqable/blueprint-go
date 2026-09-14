package ai_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"blueprintexample/internal/platform/ai"
)

func supplied(ids ...int64) map[int64]bool {
	m := map[int64]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestValidateAcceptsWellFormedFact(t *testing.T) {
	f := ai.ExtractedFact{Kind: "request", Subject: "dns change",
		Confidence: 0.9, EvidenceMessageIDs: []int64{1, 2}}
	assert.NoError(t, f.Validate(supplied(1, 2, 3)))
}

// The one that matters: a model asked to cite evidence will sometimes cite a
// plausible-looking id it never saw.
func TestValidateRejectsFabricatedCitation(t *testing.T) {
	f := ai.ExtractedFact{Kind: "decision", Subject: "pricing",
		Confidence: 0.8, EvidenceMessageIDs: []int64{99}}
	err := f.Validate(supplied(1, 2, 3))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not in the input")
}

func TestValidateRejectsJunk(t *testing.T) {
	base := supplied(1)
	cases := map[string]ai.ExtractedFact{
		"unknown kind":   {Kind: "vibe", Confidence: 0.5, EvidenceMessageIDs: []int64{1}},
		"confidence > 1": {Kind: "request", Confidence: 4, EvidenceMessageIDs: []int64{1}},
		"no evidence":    {Kind: "request", Confidence: 0.5},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) { assert.Error(t, f.Validate(base)) })
	}
}

// Application tests never reach a provider.
func TestFakeRecordsCallsAndSatisfiesProvider(t *testing.T) {
	var p ai.Provider = &ai.Fake{
		Responses: map[string]*ai.Response{"interpret": {Text: "ok"}},
	}
	resp, err := p.Complete(context.Background(), ai.Request{PromptName: "interpret"})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Text)

	_, err = p.Complete(context.Background(), ai.Request{PromptName: "missing"})
	assert.Error(t, err, "an unconfigured prompt must fail loudly in tests")
}
