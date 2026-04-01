use crate::agent::AgentEvent;
use crate::model::types::Usage;
use std::io::Write;

// ANSI codes
pub const RESET: &str = "\x1b[0m";
pub const BOLD: &str = "\x1b[1m";
const DIM: &str = "\x1b[2m";
const RED: &str = "\x1b[31m";
const GREEN: &str = "\x1b[32m";
const CYAN: &str = "\x1b[36m";
const WHITE: &str = "\x1b[37m";
const YELLOW: &str = "\x1b[33m";

// Glyphs
const SUCCESS: &str = "✓";
const FAILURE: &str = "✗";

pub struct Renderer {
    response_started: bool,
}

impl Renderer {
    pub fn new() -> Self {
        Self {
            response_started: false,
        }
    }

    pub fn banner(&self, model: &str) {
        let cwd = std::env::current_dir()
            .map(|p| {
                let home = std::env::var("HOME").unwrap_or_default();
                let s = p.to_string_lossy().to_string();
                if !home.is_empty() && s.starts_with(&home) {
                    format!("~{}", &s[home.len()..])
                } else {
                    s
                }
            })
            .unwrap_or_else(|_| ".".to_string());

        println!();
        println!("  {}{}›_{} {}opus{} v0.1.0", BOLD, WHITE, RESET, BOLD, RESET);
        println!();
        println!("  {}model:     {}{}", DIM, RESET, model);
        println!("  {}directory: {}{}", DIM, RESET, cwd);
        println!();
    }

    pub fn handle_event(&mut self, event: &AgentEvent) {
        match event {
            AgentEvent::Text(t) => self.render_text(t),
            AgentEvent::ToolComplete {
                name,
                input,
                result,
                is_error,
                duration_ms,
            } => self.render_tool(name, input, result, *is_error, *duration_ms),
            AgentEvent::Done { usage } => self.render_done(usage),
            AgentEvent::Error(e) => self.render_error(e),
        }
    }

    fn render_text(&mut self, text: &str) {
        if !self.response_started {
            self.response_started = true;
            println!();
        }
        for (i, line) in text.split('\n').enumerate() {
            if i > 0 {
                print!("\n");
            }
            print!("  {}", line);
        }
        let _ = std::io::stdout().flush();
    }

    fn render_tool(
        &mut self,
        name: &str,
        input: &str,
        result: &str,
        is_error: bool,
        duration_ms: u64,
    ) {
        if self.response_started {
            println!();
            self.response_started = false;
        }

        let display_input = tool_input_summary(name, input);
        let time_str = if duration_ms > 1000 {
            format!(" {}({:.1}s){}", DIM, duration_ms as f64 / 1000.0, RESET)
        } else {
            String::new()
        };

        let (glyph, glyph_color) = if is_error {
            (FAILURE, RED)
        } else {
            (SUCCESS, GREEN)
        };

        println!();
        println!(
            "  {}{}{} {}{}{}",
            BOLD, name, RESET, DIM, display_input, RESET
        );
        println!(
            "  {}{}{} {}{}{}{}",
            glyph_color, glyph, RESET, DIM, truncate_line(result, 200), RESET, time_str,
        );
    }

    fn render_done(&mut self, usage: &Usage) {
        if self.response_started {
            println!();
        }
        self.response_started = false;
        println!(
            "\n  {}{} · {} in · {} out{}\n",
            DIM,
            short_model_name(),
            format_tokens(usage.input_tokens),
            format_tokens(usage.output_tokens),
            RESET
        );
    }

    fn render_error(&mut self, error: &str) {
        if self.response_started {
            println!();
        }
        self.response_started = false;
        eprintln!("\n  {}{}error:{} {}", RED, BOLD, RESET, error);
    }

    pub fn info(&self, msg: &str) {
        println!("\n  {}{}{}", DIM, msg, RESET);
    }

    pub fn warn(&self, msg: &str) {
        println!("\n  {}{}{}", YELLOW, msg, RESET);
    }

    pub fn goodbye(&self) {
        println!("\n  {}Goodbye.{}", DIM, RESET);
    }
}

fn short_model_name() -> String {
    let model = std::env::var("OPUS_MODEL").unwrap_or_default();
    if model.is_empty() {
        return "opus".to_string();
    }
    // Extract the short name: "claude-opus-4-5-20250918" -> "opus-4-5"
    model
        .strip_prefix("claude-")
        .unwrap_or(&model)
        .split('-')
        .take_while(|s| s.parse::<u32>().is_ok() || s.len() <= 6)
        .collect::<Vec<_>>()
        .join("-")
}

fn format_tokens(tokens: u32) -> String {
    if tokens >= 1000 {
        format!("{:.1}k", tokens as f64 / 1000.0)
    } else {
        tokens.to_string()
    }
}

fn tool_input_summary(name: &str, input_json: &str) -> String {
    let Ok(v) = serde_json::from_str::<serde_json::Value>(input_json) else {
        return truncate_line(input_json, 80);
    };

    match name {
        "bash" => v["command"]
            .as_str()
            .map(|c| truncate_line(c, 80))
            .unwrap_or_default(),
        "read" | "edit" | "write" => v["file_path"]
            .as_str()
            .map(|p| shorten_path(p))
            .unwrap_or_default(),
        _ => truncate_line(input_json, 80),
    }
}

fn shorten_path(path: &str) -> String {
    let home = std::env::var("HOME").unwrap_or_default();
    let path = if !home.is_empty() && path.starts_with(&home) {
        format!("~{}", &path[home.len()..])
    } else {
        path.to_string()
    };
    let parts: Vec<&str> = path.split('/').collect();
    if parts.len() <= 4 {
        path
    } else {
        format!(".../{}", parts[parts.len() - 3..].join("/"))
    }
}

fn truncate_line(s: &str, max: usize) -> String {
    let first_line = s.lines().next().unwrap_or(s);
    if first_line.len() <= max {
        first_line.to_string()
    } else {
        format!("{}…", &first_line[..max])
    }
}

// truncate_result was identical to truncate_line — use truncate_line instead

pub fn prompt_string() -> String {
    format!("  {}›{} ", CYAN, RESET)
}

/// Prompt user to approve a write tool execution.
/// Shows the tool name and a summary of what it will do.
pub fn prompt_approval(tool_name: &str, input: &serde_json::Value) -> bool {
    let summary = match tool_name {
        "bash" => input["command"]
            .as_str()
            .map(|c| truncate_line(c, 80))
            .unwrap_or_default(),
        "edit" | "write" => input["file_path"]
            .as_str()
            .map(|p| shorten_path(p))
            .unwrap_or_default(),
        _ => format!("{:?}", input),
    };

    println!();
    println!(
        "  {}{}{} {}{}{}",
        BOLD, tool_name, RESET, DIM, summary, RESET
    );
    print!("  {}Allow? [y/n]{} ", YELLOW, RESET);
    let _ = std::io::stdout().flush();

    let mut response = String::new();
    if std::io::stdin().read_line(&mut response).is_err() {
        return false;
    }
    matches!(response.trim().to_lowercase().as_str(), "y" | "yes")
}
