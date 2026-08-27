package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func TestParseStream_AssemblesTextAndToolInput(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":11,"cache_read_input_tokens":22}}}`,
		``,
		`event: ping`,
		`data: {"type":"ping"}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me "}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"look."}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"read_file"}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}`,
		``,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":33}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	resp, err := parseStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2: %+v", len(resp.Content), resp.Content)
	}
	if got := resp.Content[0].Text; got != "Let me look." {
		t.Fatalf("text = %q, want the deltas joined", got)
	}
	tool := resp.Content[1]
	if tool.Type != "tool_use" || tool.ID != "t1" || tool.Name != "read_file" {
		t.Fatalf("bad tool block: %+v", tool)
	}
	if tool.Input["path"] != "a.go" {
		t.Fatalf("tool input = %+v, want path a.go", tool.Input)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop reason = %q", resp.StopReason)
	}
	want := llm.Usage{InputTokens: 11, CacheReadTokens: 22, OutputTokens: 33}
	if resp.Usage != want {
		t.Fatalf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestParseStream_ToolWithNoArguments(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"message_start","message":{}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"branch"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n\n")

	resp, err := parseStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("a tool called with no arguments is valid: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Name != "branch" {
		t.Fatalf("bad content: %+v", resp.Content)
	}
}

func TestParseStream_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, stream, want string
	}{
		{"mid-stream error event",
			"data: {\"type\":\"message_start\",\"message\":{}}\n\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n",
			"overloaded"},
		{"truncated before any event", "", "stream ended before the response was complete"},
		{"truncated mid-response",
			"data: {\"type\":\"message_start\",\"message\":{}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"half a th\"}}\n\n",
			"stream ended before the response was complete"},
		{"unparseable event", "data: {not json}\n\n", "decode stream event"},
		{"unparseable tool input",
			"data: {\"type\":\"message_start\",\"message\":{}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t\",\"name\":\"bash\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{oops\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"decode tool input for bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStream(strings.NewReader(tc.stream))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// Cancelling mid-generation must surface as a context error. Before the read
// error was propagated, a cancelled request looked like an empty success and
// failed later with a decode error, hiding what actually happened.
func TestComplete_CancellationReportsItself(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-released
	}))
	defer srv.Close()
	defer close(released)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := newTestClient(srv).Complete(ctx, llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want a context cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}
