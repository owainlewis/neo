# Neo

[![Rust](https://img.shields.io/badge/rust-1.75%2B-orange.svg)](https://www.rust-lang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Neo is a modular coding agent built in Rust. It has a pure, policy-free core that can be extended via hooks and tool bundles — build a coding assistant, a research agent, a trading bot, or anything else by swapping the system prompt and tools.

## Architecture

```
neo-core          Pure agent loop, streaming Provider, Hooks, SubagentSpawner
neo-coding        Coding bundle: bash/read/edit/write tools, system prompt, PlanModeHook
binary            TUI, ApprovalHook, CLI arg parsing
```

The core knows nothing about coding, files, or approval policy. Everything is injected.

## Features

- **Streaming responses** — real-time text deltas from Anthropic's SSE API
- **Hook system** — 5 composable hooks replace baked-in plan mode, approval, compaction
- **Parallel subagents** — `dispatch` tool fans out N independent workers
- **Customizable system prompt** — `neo -p ./trader.md` gives you a non-coding agent
- **Plan mode** — read-only exploration via a hook, not hardcoded in the loop

## Installation

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
cargo build --release
```

Binary at `target/release/neo`.

## Configuration

```bash
# Required
export ANTHROPIC_API_KEY="your_key"

# Optional
export NEO_MODEL="claude-sonnet-4-20250514"
```

Or use `~/.neo/config.toml`:

```toml
model = "claude-sonnet-4-20250514"
```

## Usage

```bash
neo                                  # coding agent (default)
neo --system-prompt ./trader.md      # pure agent with custom prompt
neo --help
```

### Slash commands

```
/plan      Plan mode (read-only tools)
/execute   Execute mode (all tools)
/model     Show current model
/clear     Clear conversation
/help      Show help
/exit      Quit
```

## Built-in Tools

| Tool | Description |
|------|-------------|
| `read` | Read files with line numbers, offset, limit |
| `edit` | Replace exact string matches in files |
| `write` | Create or overwrite files |
| `bash` | Execute shell commands with timeout |
| `dispatch` | Fan out parallel subagent workers |

## Adding Tools

Implement the `Tool` trait from `neo-core`:

```rust
use neo_core::{Tool, ToolOutput};

pub struct MyTool;

#[async_trait::async_trait]
impl Tool for MyTool {
    fn name(&self) -> &str { "my_tool" }
    fn description(&self) -> &str { "Does something useful" }
    fn input_schema(&self) -> serde_json::Value { /* JSON schema */ }
    fn is_read_only(&self) -> bool { true }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        Ok(ToolOutput::text("result".into()))
    }
}
```

## License

MIT License - see [LICENSE](LICENSE) file for details.
