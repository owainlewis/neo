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

// NewSummarizerForContextWindow builds a Summarizer whose trigger is derived
// from an optional context-window override. A non-positive override preserves
// NewSummarizer's default behavior.
func NewSummarizerForContextWindow(p llm.Provider, model string, contextWindowTokens int) Summarizer {
	s := NewSummarizer(p, model)
	if contextWindowTokens > 0 {
		s.TriggerTokens = TriggerTokensForContextWindow(contextWindowTokens)
	}
	return s
}

// TriggerTokensForContextWindow returns the estimated transcript size at which
// compaction should run for a model context window.
func TriggerTokensForContextWindow(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = DefaultContextWindowTokens
	}
	return int(float64(contextWindowTokens) * triggerRatio)
}

func (s Summarizer) Compact(ctx context.Context, messages []llm.Message, promptTokens int) (Result, error) {
	trigger := s.TriggerTokens
	if trigger <= 0 {
		trigger = TriggerTokensForContextWindow(DefaultContextWindowTokens)
	}
	keep := s.KeepRecent
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	size := promptTokens
	if size <= 0 {
		// Nothing observed yet, so fall back to the estimate for the first turn.
		size = EstimateTokens(messages)
	}
	if len(messages) <= keep || size < trigger {
		return Result{Messages: messages}, nil
	}
	split := SafeSplitPoint(messages, len(messages)-keep)
	if split <= 0 {
		// No safe boundary to cut at; leave the transcript alone rather than
		// risk orphaning a tool_result.
		return Result{Messages: messages}, nil
	}
	summary, usage, err := s.summarize(ctx, messages[:split])
	if err != nil {
		return Result{Usage: usage}, fmt.Errorf("compact transcript: %w", err)
	}
	out := make([]llm.Message, 0, len(messages)-split+1)
	out = append(out, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type: "text",
		Text: summaryPreamble + summary,
	}}})
	return Result{Messages: append(out, messages[split:]...), Usage: usage}, nil
}

// summarize asks the provider for a summary of head. A head ending before a
// fresh user turn gets a user instruction. A head ending in a completed tool
// exchange gets the instruction through the system prompt so its role sequence
// remains provider-valid.
func (s Summarizer) summarize(ctx context.Context, head []llm.Message) (string, llm.Usage, error) {
	msgs := make([]llm.Message, 0, len(head)+1)
	msgs = append(msgs, head...)
	system := summarySystem
	if len(head) > 0 && hasToolResult(head[len(head)-1]) {
		// A completed tool exchange already ends in a user-role tool_result.
		// Put the instruction in the system prompt so the summary request keeps
		// the provider-valid assistant tool_use/user tool_result sequence.
		system += "\n\n" + summaryInstruction
	} else {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
			Type: "text", Text: summaryInstruction,
		}}})
	}
	resp, err := s.Provider.Complete(ctx, llm.Request{
		Model:    s.Model,
		System:   system,
		Messages: msgs,
	})
	if err != nil {
		if resp != nil {
			return "", resp.Usage, err
		}
		return "", llm.Usage{}, err
	}
	var b strings.Builder
	for _, blk := range resp.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", resp.Usage, errors.New("summarization returned no text")
	}
	return text, resp.Usage, nil
}

// EstimateTokens approximates the token count of a transcript at ~4 characters
// per token. It is only used before the first provider response, when there is
// no reported size to use instead; it undercounts because it cannot see the
// system prompt or the tool definitions that are sent alongside.
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
