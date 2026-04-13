# Neo

[![Rust](https://img.shields.io/badge/rust-1.85%2B-orange.svg)](https://www.rust-lang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

The best AI agent in the world. Minimal. Extensible. Not just for coding.

Neo is a general-purpose AI agent with a pure, policy-free core. Swap the system prompt and tools to build anything — a coding assistant, a research agent, a trading bot, a content writer. The core doesn't know what it's being used for. That's the point.

## Philosophy

- **No permission popups.** Tools run freely. A danger guard blacklist catches genuinely destructive commands. Run in a container if you want full isolation.
- **No baked-in behavior.** Plan mode, safety policies, context compaction — everything is a composable hook, not hardcoded in the loop.
- **Parallel by default.** Fan out 10 research workers with `dispatch`. Read-only tools run concurrently. The agent doesn't wait when it doesn't have to.
- **One prompt away from a different agent.** `neo -p ./trader.md` is a trading agent. `neo -p ./writer.md` is a content agent. Same core, same tools, different brain.
- **Minimal, not incomplete.** Small codebase (~2500 lines of Rust), zero unnecessary abstractions, but nothing is missing. Streaming, hooks, subagents, TUI — it's all there.

## Architecture

```
neo-core       Pure agent loop, streaming Provider, Hooks, SubagentSpawner
neo-coding     Coding bundle: bash/read/edit/write tools, default prompt
binary         TUI, DangerGuard, CLI args
```

The core knows nothing about coding, files, or approval policy. Everything is injected.

## Quick start

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
cargo build --release
export ANTHROPIC_API_KEY="your_key"
./target/release/neo
```

## Usage

```bash
neo                                  # coding agent (default)
neo -p ./custom-prompt.md            # any agent you want
neo --yolo                           # disable danger guard
neo --help                           # show options
```

### Keyboard shortcuts

| Key | Action |
|-----|--------|
| Enter | Submit |
| Alt+Enter | New line (multiline input) |
| Shift+Tab | Toggle plan/execute mode |
| Ctrl+W | Delete word backward |
| Ctrl+U | Kill to start of line |
| Ctrl+K | Kill to end of line |
| Ctrl+C | Quit |

### Slash commands

```
/plan        Plan mode (read-only tools)
/execute     Execute mode (all tools)
/model       Show current model
/clear       Clear conversation
/help        Show help
/exit        Quit
```

## Configuration

Single config file: `~/.neo/config.toml`

```toml
model = "claude-opus-4-6"
max_tokens = 16384

[guard]
block = [
    "kubectl delete",
    "terraform destroy",
]
allow = [
    "reset --hard",
]
```

Or environment variables:

```bash
export ANTHROPIC_API_KEY="your_key"
export NEO_MODEL="claude-sonnet-4-20250514"
```

## Tools

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `read` | Read files with line numbers |
| `edit` | Replace exact string matches |
| `write` | Create or overwrite files |
| `dispatch` | Fan out parallel subagent workers |

### Adding your own

```rust
use neo_core::{Tool, ToolOutput};

pub struct MyTool;

#[async_trait::async_trait]
impl Tool for MyTool {
    fn name(&self) -> &str { "my_tool" }
    fn description(&self) -> &str { "Does something useful" }
    fn input_schema(&self) -> serde_json::Value { todo!() }
    fn is_read_only(&self) -> bool { true }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        Ok(ToolOutput::text("result".into()))
    }
}
```

## Hooks

Everything pluggable:

```rust
#[async_trait]
impl Hooks for MyHook {
    async fn augment_system_prompt(&self, prompt: String) -> String { ... }
    async fn filter_tools(&self, tools: Vec<ToolDefinition>) -> Vec<ToolDefinition> { ... }
    async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision { ... }
    async fn after_tool_call(&self, call: &ToolUseBlock, result: ToolResult) -> ToolResult { ... }
    async fn transform_context(&self, messages: &mut Vec<Message>) { ... }
}
```

Compose with `HookChain::new().add(hook_a).add(hook_b)`.

## License

MIT
