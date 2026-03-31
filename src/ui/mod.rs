use crate::agent::AgentEvent;
use crate::model::types::Usage;
use std::io::Write;

// ANSI codes
const RESET: &str = "\x1b[0m";
const BOLD: &str = "\x1b[1m";
const DIM: &str = "\x1b[2m";
const RED: &str = "\x1b[31m";
const GREEN: &str = "\x1b[32m";
const CYAN: &str = "\x1b[36m";
const WHITE: &str = "\x1b[37m";

// Glyphs
const RESPONSE_INDICATOR: &str = "⎿";
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
        println!(
            "\n  {}{}opus{} {}v0.1.0{}",
            BOLD, WHITE, RESET, DIM, RESET
        );
        println!("  {}Model: {}{}\n", DIM, model, RESET);
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
            print!("\n  {} ", RESPONSE_INDICATOR);
        }
        for (i, line) in text.split('\n').enumerate() {
            if i > 0 {
                print!("\n    ");
            }
            print!("{}", line);
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
            format!(" {DIM}({:.1}s){RESET}", duration_ms as f64 / 1000.0)
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
            "    {}{}{} {}{}{}",
            BOLD, name, RESET, DIM, display_input, RESET
        );
        println!(
            "    {}{}{} {}{}{}{}",
            glyph_color,
            glyph,
            RESET,
            DIM,
            truncate_result(result, 200),
            RESET,
            time_str,
        );
    }

    fn render_done(&mut self, usage: &Usage) {
        if self.response_started {
            println!();
        }
        self.response_started = false;
        println!(
            "\n  {}tokens: {} in · {} out{}\n",
            DIM, usage.input_tokens, usage.output_tokens, RESET
        );
    }

    fn render_error(&mut self, error: &str) {
        if self.response_started {
            println!();
        }
        self.response_started = false;
        eprintln!("\n  {}{}error:{} {}", RED, BOLD, RESET, error);
    }

    pub fn goodbye(&self) {
        println!("\n  {}Goodbye.{}", DIM, RESET);
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
    let parts: Vec<&str> = path.split('/').collect();
    if parts.len() <= 3 {
        path.to_string()
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

fn truncate_result(s: &str, max: usize) -> String {
    let first_line = s.lines().next().unwrap_or(s);
    if first_line.len() <= max {
        first_line.to_string()
    } else {
        format!("{}…", &first_line[..max])
    }
}

pub fn prompt_string() -> String {
    format!("  {}>{} ", CYAN, RESET)
}
