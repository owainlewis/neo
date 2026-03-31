# Opus

[![Rust](https://img.shields.io/badge/rust-1.75%2B-orange.svg)](https://www.rust-lang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Opus is a minimalist coding agent built in Rust that helps with software engineering tasks through an interactive command-line interface. It leverages AI models (currently Anthropic Claude) with built-in tools for file operations and command execution.

## Table of Contents

- [Features](#features)
- [Built-in Tools](#built-in-tools)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [Architecture](#architecture)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)
- [Troubleshooting](#troubleshooting)

## Features

- **Interactive CLI**: Chat-based interface with history support
- **File Operations**: Read, edit, and manipulate files directly
- **Command Execution**: Run bash commands, tests, git operations, etc.
- **Streaming Responses**: Real-time AI response streaming
- **Tool Integration**: Built-in tools for common development tasks
- **Multi-turn Conversations**: Maintains context across interactions

## Built-in Tools

- **`read`**: Read file contents with optional line limits and offsets
- **`edit`**: Edit files by replacing exact string matches
- **`bash`**: Execute shell commands with optional timeout

## Installation

### Prerequisites

- Rust 1.75+ (with 2024 edition support)
- Anthropic API key

### Build from Source

```bash
git clone https://github.com/yourusername/opus.git
cd opus
cargo build --release
```

The binary will be available at `target/release/opus`.

## Configuration

### Environment Variables

Set up your environment by copying the example file and adding your API key:

```bash
cp .env.example .env
# Edit .env and add your Anthropic API key
```

Or set environment variables directly:

```bash
# Required
export ANTHROPIC_API_KEY="your_api_key_here"

# Optional
export OPUS_MODEL="claude-sonnet-4-20250514"  # Default model
```

### Supported Models

- `claude-sonnet-4-20250514` (default)
- Other Anthropic Claude models (configurable via `OPUS_MODEL`)

## Usage

### Basic Usage

```bash
./target/release/opus
```

This starts an interactive session:

```
opus v0.1.0
Model: claude-sonnet-4-20250514
Type your request. Ctrl+D to exit.

> Help me create a new Python file with a simple web server
```

### Example Interactions

**File Operations:**
```
> Read the contents of main.rs and explain what it does
> Create a new function in utils.py that validates email addresses
> Fix the syntax error in line 45 of config.json
```

**Development Tasks:**
```
> Run the test suite and fix any failing tests
> Set up a new Git repository and make initial commit
> Install dependencies and start the development server
```

**Code Analysis:**
```
> Analyze this codebase and suggest improvements
> Find all TODO comments in the project
> Generate documentation for the API endpoints
```

### Command Line Controls

- **Enter**: Submit your request
- **Ctrl+C**: Cancel current input (continue session)
- **Ctrl+D**: Exit the application

## Architecture

### Core Components

- **Agent (`src/agent.rs`)**: Main conversation loop and tool orchestration
- **Model Provider (`src/model/`)**: AI model integration (currently Anthropic)
- **Tools (`src/tools/`)**: Built-in tool implementations
- **CLI (`src/main.rs`)**: Interactive command-line interface

### Tool System

Tools are automatically discovered and made available to the AI model. Each tool:
- Defines its schema and parameters
- Executes asynchronously
- Returns structured results
- Handles errors gracefully

### State Management

- Maintains conversation history (up to 25 turns by default)
- Tracks token usage across requests
- Preserves context for multi-turn interactions

## Development

### Project Goals

- **Minimalist Core**: Simple, focused architecture
- **Fast Native Binaries**: Rust performance for CLI tools
- **Easily Extensible**: Plugin-friendly tool system
- **Multi-model Support**: Designed for multiple AI providers

### Adding New Tools

1. Create a new tool file in `src/tools/`
2. Implement the tool interface
3. Register it in `src/tools/mod.rs`

Example tool structure:
```rust
pub struct MyTool;

impl Tool for MyTool {
    fn definition(&self) -> ToolDefinition {
        // Define tool schema
    }
    
    async fn execute(&self, input: Value) -> Result<String, String> {
        // Implement tool logic
    }
}
```

### Dependencies

Key dependencies that power Opus:

| Crate | Purpose | Version |
|-------|---------|---------|
| `tokio` | Async runtime | 1.x |
| `reqwest` | HTTP client for API calls | 0.12 |
| `serde/serde_json` | JSON serialization | 1.x |
| `rustyline` | Interactive CLI with history | 15.x |
| `futures` | Stream processing | 0.3 |
| `async-trait` | Async traits | 0.1 |

## Contributing

We welcome contributions! Here's how to get started:

### Quick Start

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes
4. Add tests if applicable
5. Ensure code passes tests: `cargo test`
6. Commit changes: `git commit -m 'Add amazing feature'`
7. Push to branch: `git push origin feature/amazing-feature`
8. Submit a pull request

### Development Setup

```bash
# Clone your fork
git clone https://github.com/yourusername/opus.git
cd opus

# Set up environment
cp .env.example .env
# Add your API key to .env

# Run in development mode
cargo run

# Run tests
cargo test

# Check formatting and linting
cargo fmt --check
cargo clippy
```

### Code Guidelines

- Follow Rust conventions and use `cargo fmt`
- Add tests for new functionality
- Update documentation for public APIs
- Keep commits focused and write clear commit messages

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Troubleshooting

### Common Issues

#### API Key Not Found
```
Error: ANTHROPIC_API_KEY not set. Add it to .env or export it.
```
**Solution:** 
1. Copy `.env.example` to `.env`: `cp .env.example .env`
2. Edit `.env` and add your Anthropic API key
3. Or export directly: `export ANTHROPIC_API_KEY="your_key"`

#### Model Not Available
**Solution:**
- Check that your API key has access to the specified model
- Verify the model name in the `OPUS_MODEL` environment variable
- Try using the default model: `claude-sonnet-4-20250514`

#### Build Errors
**Solution:**
- Ensure you have Rust 1.75+ with 2024 edition support: `rustc --version`
- Update dependencies: `cargo update`
- Clean build: `cargo clean && cargo build`

#### Permission Denied on Binary
**Solution:**
```bash
chmod +x target/release/opus
```

### Getting Help

If you encounter issues not covered here:

1. Check the [existing issues](https://github.com/yourusername/opus/issues)
2. Create a [new issue](https://github.com/yourusername/opus/issues/new) with:
   - Your operating system and Rust version
   - Complete error message
   - Steps to reproduce the problem
   - Relevant configuration (without API keys)

### Performance Tips

- **Large Files**: Use the `limit` parameter with the `read` tool for large files
- **Long Operations**: The `bash` tool has configurable timeouts for long-running commands  
- **Memory Usage**: Conversation history is limited to 25 turns by default to manage memory 