package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// Compaction defaults. The context window default is intentionally conservative
// for modern coding models; users on larger-context models can override it in
// config without requiring a model catalog.
const (
	DefaultContextWindowTokens = 200_000
	DefaultKeepRecent          = 20

	triggerRatio = 0.70
)

const summarySystem = `You summarize coding agent transcripts. Produce a compact summary that preserves:
- the user's goal and any constraints they stated,
- decisions already made and why,
- files created or changed, and commands run,
- unresolved errors or open questions,
- enough recent context for the agent to continue seamlessly.

Drop repeated logs, obsolete exploration, and large tool output once its conclusion is captured. Write plain prose; do not address the user.`

const summaryInstruction = "Summarize the conversation above following your instructions. Reply with only the summary."

const summaryPreamble = "[Earlier conversation was compacted to fit the context window. Summary of what happened so far:]\n\n"

// Summarizer compacts long transcripts by asking the provider to summarize the
// oldest turns, replacing them with a single user message that carries the
// summary. The most recent messages are kept verbatim, cut at a safe split
// point so no tool_result loses its tool_use.
type Summarizer struct {
	Provider llm.Provider
	Model    string
	// TriggerTokens is the estimated transcript size at which compaction runs
	// (default TriggerTokensForContextWindow(DefaultContextWindowTokens)).
	TriggerTokens int
	// KeepRecent is the number of trailing messages preserved verbatim
	// (default DefaultKeepRecent).
	KeepRecent int
}

// NewSummarizer builds a Summarizer with default thresholds.
func NewSummarizer(p llm.Provider, model string) Summarizer {
	return Summarizer{Provider: p, Model: model}
}

// TriggerTokensForContextWindow returns the estimated transcript size at which
// compaction should run for a model context window.
func TriggerTokensForContextWindow(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = DefaultContextWindowTokens
	}
	return int(float64(contextWindowTokens) * triggerRatio)
}

func (s Summarizer) Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	trigger := s.TriggerTokens
	if trigger <= 0 {
		trigger = TriggerTokensForContextWindow(DefaultContextWindowTokens)
	}
	keep := s.KeepRecent
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	if len(messages) <= keep || EstimateTokens(messages) < trigger {
		return messages, nil
	}
	split := SafeSplitPoint(messages, len(messages)-keep)
	if split <= 0 {
		// No safe boundary to cut at; leave the transcript alone rather than
		// risk orphaning a tool_result.
		return messages, nil
	}
	summary, err := s.summarize(ctx, messages[:split])
	if err != nil {
		return nil, fmt.Errorf("compact transcript: %w", err)
	}
	out := make([]llm.Message, 0, len(messages)-split+1)
	out = append(out, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type: "text",
		Text: summaryPreamble + summary,
	}}})
	return append(out, messages[split:]...), nil
}

// summarize asks the provider for a summary of head by appending a user
// instruction. head always ends just before a fresh user turn, so appending a
// user message keeps roles alternating.
func (s Summarizer) summarize(ctx context.Context, head []llm.Message) (string, error) {
	msgs := make([]llm.Message, 0, len(head)+1)
	msgs = append(msgs, head...)
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type: "text", Text: summaryInstruction,
	}}})
	resp, err := s.Provider.Complete(ctx, llm.Request{
		Model:    s.Model,
		System:   summarySystem,
		Messages: msgs,
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, blk := range resp.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", errors.New("summarization returned no text")
	}
	return text, nil
}

// EstimateTokens approximates the token count of a transcript at ~4 characters
// per token. It is deliberately rough: it only needs to be in the right order
// of magnitude to decide when to compact.
func EstimateTokens(messages []llm.Message) int {
	chars := 0
	for _, m := range messages {
		for _, b := range m.Content {
			chars += len(b.Text) + len(b.Content) + len(b.Raw)
			if len(b.Input) > 0 {
				if j, err := json.Marshal(b.Input); err == nil {
					chars += len(j)
				}
			}
			if b.Source != nil {
				chars += len(b.Source.Data)
			}
		}
	}
	return chars / 4
}
