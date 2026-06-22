package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

type Status string

const (
	Pending Status = "pending"
	Running Status = "running"
	Done    Status = "done"
	Failed  Status = "failed"
	Skipped Status = "skipped"
)

type Item struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status Status `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type State struct {
	Title string `json:"title,omitempty"`
	Items []Item `json:"items"`
}

type Event struct {
	Action string `json:"action"`
	State  State  `json:"state,omitempty"`
	ID     string `json:"id,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type Tool struct {
	Events chan<- Event
}

func (Tool) Name() string { return "workflow" }

func (Tool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "workflow",
		Description: "Create or update the visible workflow checklist. Use for multi-step tasks; Neo attaches tool and subagent activity automatically.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"create", "start", "complete", "fail", "skip", "clear"},
				},
				"title": map[string]any{"type": "string"},
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":   map[string]any{"type": "string"},
							"text": map[string]any{"type": "string"},
						},
						"required": []string{"id", "text"},
					},
				},
				"id":     map[string]any{"type": "string"},
				"detail": map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
	}
}

func (t Tool) Run(_ context.Context, input map[string]any) (string, error) {
	action := strings.TrimSpace(stringValue(input["action"]))
	if action == "" {
		return "", fmt.Errorf("workflow: missing action")
	}
	ev := Event{Action: action, ID: strings.TrimSpace(stringValue(input["id"])), Detail: strings.TrimSpace(stringValue(input["detail"]))}
	if action == "create" {
		ev.State.Title = strings.TrimSpace(stringValue(input["title"]))
		items, err := parseItems(input["items"])
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "", fmt.Errorf("workflow create: provide at least one item")
		}
		ev.State.Items = items
	}
	if action != "create" && action != "clear" && ev.ID == "" {
		return "", fmt.Errorf("workflow %s: missing id", action)
	}
	if t.Events != nil {
		select {
		case t.Events <- ev:
		default:
		}
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "action": action})
	return string(b), nil
}

func parseItems(v any) ([]Item, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("workflow create: items must be an array")
	}
	items := make([]Item, 0, len(raw))
	for i, rv := range raw {
		m, ok := rv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow create: item %d must be an object", i+1)
		}
		id := strings.TrimSpace(stringValue(m["id"]))
		text := strings.TrimSpace(stringValue(m["text"]))
		if id == "" || text == "" {
			return nil, fmt.Errorf("workflow create: item %d needs id and text", i+1)
		}
		items = append(items, Item{ID: id, Text: text, Status: Pending})
	}
	return items, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
