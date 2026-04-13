use neo_core::{AgentEvent, Usage};
use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::prelude::*;
use ratatui::widgets::*;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

const SPINNER_FRAMES: &[&str] = &["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const MAX_RESULT_LINES: usize = 8;

// --- App state ---

#[derive(PartialEq)]
enum Mode {
    Input,
    Processing,
}

pub struct App {
    output: Vec<Line<'static>>,
    scroll_offset: usize,

    input: String,
    cursor: usize,
    history: Vec<String>,
    history_idx: Option<usize>,

    mode: Mode,
    spinner_frame: usize,

    streaming: bool,
    streaming_buffer: String,
    streaming_start_idx: Option<usize>,

    plan_enabled: Option<Arc<AtomicBool>>,

    pub model: String,
    pub usage: Usage,
    pub should_quit: bool,
}

impl App {
    pub fn new(model: String) -> Self {
        let mut app = Self {
            output: Vec::new(),
            scroll_offset: 0,
            input: String::new(),
            cursor: 0,
            history: Vec::new(),
            history_idx: None,
            mode: Mode::Input,
            spinner_frame: 0,
            streaming: false,
            streaming_buffer: String::new(),
            streaming_start_idx: None,
            plan_enabled: None,
            model,
            usage: Usage::default(),
            should_quit: false,
        };

        // Minimal banner
        app.output.push(Line::from(""));
        app.output.push(Line::from(vec![
            Span::styled("  neo", Style::default().bold().fg(Color::White)),
            Span::styled(" v0.1.0", Style::default().dim()),
        ]));
        app.output.push(Line::from(""));

        app
    }

    /// Connect the plan mode toggle so the UI can flip it via Shift+Tab.
    pub fn set_plan_enabled(&mut self, handle: Arc<AtomicBool>) {
        self.plan_enabled = Some(handle);
    }

    pub fn is_plan_mode(&self) -> bool {
        self.plan_enabled
            .as_ref()
            .map(|h| h.load(Ordering::Relaxed))
            .unwrap_or(false)
    }

    fn toggle_plan_mode(&mut self) {
        if let Some(ref handle) = self.plan_enabled {
            let was = handle.load(Ordering::Relaxed);
            handle.store(!was, Ordering::Relaxed);
            let msg = if !was {
                "Plan mode — read-only tools only"
            } else {
                "Execute mode — all tools"
            };
            self.output.push(Line::from(Span::styled(
                format!("  {}", msg),
                Style::default().dim(),
            )));
            self.scroll_offset = 0;
        }
    }

    pub fn set_processing(&mut self) {
        self.mode = Mode::Processing;
    }

    /// Push a blank line only if the last line isn't already blank.
    fn push_blank(&mut self) {
        let last_is_blank = self
            .output
            .last()
            .map(|l| l.spans.is_empty() || l.spans.iter().all(|s| s.content.trim().is_empty()))
            .unwrap_or(true);
        if !last_is_blank {
            self.output.push(Line::from(""));
        }
    }

    pub fn echo_input(&mut self, text: &str) {
        self.push_blank();
        for (i, line) in text.split('\n').enumerate() {
            let prefix = if i == 0 { "  > " } else { "  : " };
            self.output.push(Line::from(vec![
                Span::styled(prefix, Style::default().fg(Color::White).bold()),
                Span::styled(line.to_string(), Style::default().fg(Color::White).bold()),
            ]));
        }
        self.push_blank();
        self.scroll_offset = 0;
    }

    // --- Event handling ---

    fn end_streaming(&mut self) {
        self.streaming = false;
        self.streaming_start_idx = None;
    }

    pub fn handle_agent_event(&mut self, event: AgentEvent) {
        match event {
            AgentEvent::Thinking => {
                self.mode = Mode::Processing;
            }
            AgentEvent::ResponseReceived => {}
            AgentEvent::TextDelta(delta) => {
                if !self.streaming {
                    self.streaming = true;
                    self.streaming_buffer.clear();
                    self.streaming_start_idx = Some(self.output.len());
                }
                self.streaming_buffer.push_str(&delta);

                let start = self.streaming_start_idx.unwrap();
                self.output.truncate(start);
                let trimmed = self.streaming_buffer.trim_start_matches('\n');
                for line in trimmed.split('\n') {
                    self.output.push(Line::from(Span::styled(
                        format!("  {}", line),
                        Style::default().fg(Color::Rgb(220, 220, 230)),
                    )));
                }
                self.scroll_offset = 0;
            }
            AgentEvent::Text(text) => {
                self.end_streaming();
                for line in text.lines() {
                    self.output.push(Line::from(Span::styled(
                        format!("  {}", line),
                        Style::default().fg(Color::Rgb(220, 220, 230)),
                    )));
                }
                self.scroll_offset = 0;
            }
            AgentEvent::ToolComplete {
                name,
                input,
                result,
                is_error,
                duration_ms,
            } => {
                self.end_streaming();
                self.push_blank();

                let result_color = Color::Rgb(150, 150, 165);

                // Tool header — PI style: "$ command" for bash, "read path" for read, etc.
                let header = tool_header(&name, &input);
                let header_color = if is_error { Color::Red } else { Color::Rgb(180, 180, 190) };
                self.output.push(Line::from(Span::styled(
                    format!("  {}", header),
                    Style::default().fg(header_color),
                )));

                // Result lines — show last N lines, truncate from top
                let result_lines: Vec<&str> = result.lines().collect();
                let total = result_lines.len();
                let hidden = total.saturating_sub(MAX_RESULT_LINES);
                let visible_start = hidden;

                if hidden > 0 {
                    self.output.push(Line::from(Span::styled(
                        format!("  ... ({} earlier lines)", hidden),
                        Style::default().fg(Color::Rgb(80, 80, 90)).italic(),
                    )));
                }

                for line in &result_lines[visible_start..] {
                    self.output.push(Line::from(Span::styled(
                        format!("  {}", line),
                        Style::default().fg(result_color),
                    )));
                }

                // Timing on its own line
                let secs = duration_ms as f64 / 1000.0;
                self.output.push(Line::from(Span::styled(
                    format!("  Took {:.1}s", secs),
                    Style::default().fg(Color::Rgb(80, 80, 90)),
                )));

                self.scroll_offset = 0;
            }
            AgentEvent::Done { usage } => {
                self.end_streaming();
                let duration = format!(
                    "{} in {} out",
                    format_tokens(usage.input_tokens),
                    format_tokens(usage.output_tokens),
                );
                self.usage = usage;
                self.mode = Mode::Input;
                self.push_blank();
                self.output.push(Line::from(Span::styled(
                    format!("  ✓ {}", duration),
                    Style::default().fg(Color::Rgb(80, 80, 90)),
                )));
                self.push_blank();
                self.scroll_offset = 0;
            }
            AgentEvent::Error(e) => {
                self.end_streaming();
                self.push_blank();
                self.output.push(Line::from(vec![
                    Span::styled("  ✗ ", Style::default().fg(Color::Red)),
                    Span::styled(e, Style::default().fg(Color::Red)),
                ]));
                self.mode = Mode::Input;
                self.scroll_offset = 0;
            }
            AgentEvent::Info(msg) => {
                for line in msg.lines() {
                    self.output.push(Line::from(Span::styled(
                        format!("  {}", line),
                        Style::default().dim(),
                    )));
                }
                self.mode = Mode::Input;
                self.scroll_offset = 0;
            }
            AgentEvent::Warning(msg) => {
                self.output.push(Line::from(vec![
                    Span::styled("  ! ", Style::default().fg(Color::Yellow)),
                    Span::styled(msg, Style::default().fg(Color::Yellow)),
                ]));
                self.mode = Mode::Input;
                self.scroll_offset = 0;
            }
        }
    }

    // --- Key handling ---

    pub fn handle_key(&mut self, key: KeyEvent) -> Option<String> {
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            match key.code {
                KeyCode::Char('c') | KeyCode::Char('d') => {
                    self.should_quit = true;
                    return None;
                }
                KeyCode::Char('w') if self.mode == Mode::Input => {
                    // Delete word backward
                    if self.cursor > 0 {
                        let mut end = self.cursor;
                        // Skip trailing whitespace
                        while end > 0 && self.input.as_bytes().get(end - 1) == Some(&b' ') {
                            end -= 1;
                        }
                        // Delete back to word boundary
                        let mut start = end;
                        while start > 0 {
                            let b = self.input.as_bytes()[start - 1];
                            if b == b' ' || b == b'\n' {
                                break;
                            }
                            start -= 1;
                        }
                        if start == end {
                            // Was only whitespace/newlines — delete those
                            start = end.saturating_sub(1);
                            // Back up over the whitespace/newline we skipped past
                            while start > 0 {
                                let b = self.input.as_bytes()[start - 1];
                                if b != b' ' && b != b'\n' {
                                    break;
                                }
                                start -= 1;
                            }
                        }
                        self.input.drain(start..self.cursor);
                        self.cursor = start;
                    }
                    return None;
                }
                KeyCode::Char('u') if self.mode == Mode::Input => {
                    // Kill line backward (cursor to start of current line)
                    let line_start = self.input[..self.cursor]
                        .rfind('\n')
                        .map(|p| p + 1)
                        .unwrap_or(0);
                    self.input.drain(line_start..self.cursor);
                    self.cursor = line_start;
                    return None;
                }
                KeyCode::Char('k') if self.mode == Mode::Input => {
                    // Kill to end of line
                    let line_end = self.input[self.cursor..]
                        .find('\n')
                        .map(|p| self.cursor + p)
                        .unwrap_or(self.input.len());
                    self.input.drain(self.cursor..line_end);
                    return None;
                }
                _ => {}
            }
        }

        // Shift+Tab toggles plan/execute mode
        if key.code == KeyCode::BackTab {
            self.toggle_plan_mode();
            return None;
        }

        if self.mode == Mode::Processing {
            match key.code {
                KeyCode::PageUp => self.scroll_up(10),
                KeyCode::PageDown => self.scroll_down(10),
                _ => {}
            }
            return None;
        }

        match key.code {
            KeyCode::Enter
                if key.modifiers.contains(KeyModifiers::SHIFT)
                    || key.modifiers.contains(KeyModifiers::ALT) =>
            {
                // Shift+Enter or Alt+Enter inserts a newline for multiline input.
                // Alt+Enter works in most terminals; Shift+Enter needs kitty protocol.
                self.input.insert(self.cursor, '\n');
                self.cursor += 1;
                None
            }
            KeyCode::Enter => {
                let input: String = self.input.drain(..).collect();
                self.cursor = 0;
                if input.trim().is_empty() {
                    return None;
                }
                self.history.push(input.clone());
                self.history_idx = None;
                Some(input)
            }
            KeyCode::Char(c) => {
                self.input.insert(self.cursor, c);
                self.cursor += c.len_utf8();
                None
            }
            KeyCode::Backspace => {
                if self.cursor > 0 {
                    let mut prev = self.cursor - 1;
                    while prev > 0 && !self.input.is_char_boundary(prev) {
                        prev -= 1;
                    }
                    self.input.drain(prev..self.cursor);
                    self.cursor = prev;
                }
                None
            }
            KeyCode::Delete => {
                if self.cursor < self.input.len() {
                    let mut next = self.cursor + 1;
                    while next < self.input.len() && !self.input.is_char_boundary(next) {
                        next += 1;
                    }
                    self.input.drain(self.cursor..next);
                }
                None
            }
            KeyCode::Left => {
                if self.cursor > 0 {
                    self.cursor -= 1;
                    while self.cursor > 0 && !self.input.is_char_boundary(self.cursor) {
                        self.cursor -= 1;
                    }
                }
                None
            }
            KeyCode::Right => {
                if self.cursor < self.input.len() {
                    self.cursor += 1;
                    while self.cursor < self.input.len()
                        && !self.input.is_char_boundary(self.cursor)
                    {
                        self.cursor += 1;
                    }
                }
                None
            }
            KeyCode::Home => {
                self.cursor = 0;
                None
            }
            KeyCode::End => {
                self.cursor = self.input.len();
                None
            }
            KeyCode::Up => {
                if !self.history.is_empty() {
                    let idx = match self.history_idx {
                        Some(i) if i > 0 => i - 1,
                        Some(i) => i,
                        None => self.history.len() - 1,
                    };
                    self.history_idx = Some(idx);
                    self.input = self.history[idx].clone();
                    self.cursor = self.input.len();
                }
                None
            }
            KeyCode::Down => {
                if let Some(idx) = self.history_idx {
                    if idx + 1 < self.history.len() {
                        self.history_idx = Some(idx + 1);
                        self.input = self.history[idx + 1].clone();
                        self.cursor = self.input.len();
                    } else {
                        self.history_idx = None;
                        self.input.clear();
                        self.cursor = 0;
                    }
                }
                None
            }
            KeyCode::PageUp => {
                self.scroll_up(10);
                None
            }
            KeyCode::PageDown => {
                self.scroll_down(10);
                None
            }
            _ => None,
        }
    }

    fn scroll_up(&mut self, n: usize) {
        self.scroll_offset = (self.scroll_offset + n).min(self.output.len().saturating_sub(1));
    }

    fn scroll_down(&mut self, n: usize) {
        self.scroll_offset = self.scroll_offset.saturating_sub(n);
    }

    // --- Drawing ---

    fn input_height(&self) -> u16 {
        let line_count = self.input.chars().filter(|c| *c == '\n').count() + 1;
        // +2 for top and bottom padding rows
        (line_count as u16).max(1) + 2
    }

    pub fn draw(&mut self, frame: &mut Frame) {
        let input_h = self.input_height();
        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Min(1),              // output area
                Constraint::Length(1),            // separator
                Constraint::Length(input_h),      // input
            ])
            .split(frame.area());

        self.draw_output(frame, chunks[0]);
        self.draw_separator(frame, chunks[1]);
        self.draw_input(frame, chunks[2]);
    }

    /// Pre-wrap a line to fit within the given width, preserving style.
    /// Returns one or more lines.
    fn wrap_line(line: &Line<'static>, width: usize) -> Vec<Line<'static>> {
        if width == 0 {
            return vec![line.clone()];
        }
        // Compute total character width
        let total_chars: usize = line.spans.iter().map(|s| s.content.chars().count()).count();
        let total_len: usize = line.spans.iter().map(|s| s.content.len()).sum();
        if total_len == 0 || total_chars <= width {
            return vec![line.clone()];
        }

        // Flatten into (char, style) pairs then chunk by width
        let mut chars_and_styles: Vec<(char, Style)> = Vec::new();
        for span in &line.spans {
            for c in span.content.chars() {
                chars_and_styles.push((c, span.style));
            }
        }

        let mut result = Vec::new();
        for chunk in chars_and_styles.chunks(width) {
            // Group consecutive same-style chars back into Spans
            let mut spans: Vec<Span<'static>> = Vec::new();
            let mut current_style = chunk[0].1;
            let mut current_text = String::new();
            for &(c, style) in chunk {
                if style == current_style {
                    current_text.push(c);
                } else {
                    spans.push(Span::styled(current_text, current_style));
                    current_style = style;
                    current_text = String::from(c);
                }
            }
            if !current_text.is_empty() {
                spans.push(Span::styled(current_text, current_style));
            }
            result.push(Line::from(spans));
        }
        result
    }

    fn draw_output(&self, frame: &mut Frame, area: Rect) {
        let w = area.width as usize;
        let h = area.height as usize;

        // Pre-wrap all output lines so scroll math is accurate
        let wrapped: Vec<Line> = self
            .output
            .iter()
            .flat_map(|line| Self::wrap_line(line, w))
            .collect();

        let total = wrapped.len();
        let end = total.saturating_sub(self.scroll_offset);
        let start = end.saturating_sub(h);

        let visible: Vec<Line> = wrapped[start..end].to_vec();
        let paragraph = Paragraph::new(visible);
        frame.render_widget(paragraph, area);
    }

    fn draw_separator(&self, frame: &mut Frame, area: Rect) {
        let dim = Style::default().fg(Color::Rgb(50, 50, 50));
        let w = area.width as usize;

        let (label, label_style) = if self.is_plan_mode() {
            (
                " PLAN ",
                Style::default().fg(Color::Yellow).bold(),
            )
        } else {
            (
                " EXECUTE ",
                Style::default().fg(Color::Green).dim(),
            )
        };

        let hint = " shift+tab to toggle ";
        let label_len = label.len();
        let hint_len = hint.len();
        let left_sep = 2;
        let right_sep = w.saturating_sub(left_sep + label_len + hint_len + 2);

        let line = Line::from(vec![
            Span::styled("─".repeat(left_sep), dim),
            Span::styled(label, label_style),
            Span::styled("─".repeat(right_sep), dim),
            Span::styled(hint, Style::default().fg(Color::Rgb(60, 60, 70)).italic()),
            Span::styled("─", dim),
        ]);
        frame.render_widget(Paragraph::new(line), area);
    }

    fn draw_input(&mut self, frame: &mut Frame, area: Rect) {
        let input_bg = Color::Rgb(25, 25, 30);

        // Fill entire input area with background
        let bg_block = Block::default().style(Style::default().bg(input_bg));
        frame.render_widget(bg_block, area);

        // Content sits between top and bottom padding rows
        let content_area = Rect {
            x: area.x,
            y: area.y + 1,
            width: area.width,
            height: area.height.saturating_sub(2),
        };

        if self.mode == Mode::Processing {
            self.spinner_frame = (self.spinner_frame + 1) % SPINNER_FRAMES.len();
            let mut parts = vec![Span::styled(
                format!("  {} ", SPINNER_FRAMES[self.spinner_frame]),
                Style::default().fg(Color::Cyan),
            )];
            parts.push(Span::styled(
                short_model_name(&self.model),
                Style::default().dim(),
            ));
            if self.usage.input_tokens > 0 {
                parts.push(Span::styled(
                    format!(
                        " · {} in {} out",
                        format_tokens(self.usage.input_tokens),
                        format_tokens(self.usage.output_tokens),
                    ),
                    Style::default().dim(),
                ));
            }
            if self.is_plan_mode() {
                parts.push(Span::styled(
                    " PLAN",
                    Style::default().fg(Color::Yellow).bold(),
                ));
            }
            frame.render_widget(
                Paragraph::new(Line::from(parts)).style(Style::default().bg(input_bg)),
                content_area,
            );
            return;
        }

        // Input mode — render multiline input
        let input_lines: Vec<&str> = self.input.split('\n').collect();
        let mut lines: Vec<Line> = Vec::new();

        for (i, text) in input_lines.iter().enumerate() {
            let prefix = if i == 0 { "  > " } else { "  : " };
            let mut spans = vec![
                Span::styled(prefix, Style::default().fg(Color::Cyan)),
                Span::raw(text.to_string()),
            ];
            // Right-align status on first line when single-line
            if i == 0 && input_lines.len() == 1 {
                let status = self.build_status_string();
                if !status.is_empty() {
                    let used = 4 + text.chars().count();
                    let width = content_area.width as usize;
                    if used + status.len() + 2 < width {
                        spans.push(Span::raw(" ".repeat(width - used - status.len())));
                        spans.push(Span::styled(status, Style::default().dim()));
                    }
                }
            }
            lines.push(Line::from(spans));
        }

        frame.render_widget(
            Paragraph::new(lines).style(Style::default().bg(input_bg)),
            content_area,
        );

        // Position cursor in multiline input
        let text_before_cursor = &self.input[..self.cursor];
        let cursor_line = text_before_cursor.chars().filter(|c| *c == '\n').count() as u16;
        let last_newline = text_before_cursor.rfind('\n');
        let col = match last_newline {
            Some(pos) => text_before_cursor[pos + 1..].chars().count() as u16,
            None => text_before_cursor.chars().count() as u16,
        };
        frame.set_cursor_position((
            content_area.x + 4 + col,
            content_area.y + cursor_line,
        ));
    }

    fn build_status_string(&self) -> String {
        let mut parts = vec![short_model_name(&self.model)];
        if self.usage.input_tokens > 0 {
            parts.push(format!(
                "{} in {} out",
                format_tokens(self.usage.input_tokens),
                format_tokens(self.usage.output_tokens),
            ));
        }
        if self.is_plan_mode() {
            parts.push("PLAN".into());
        }
        parts.join(" · ")
    }
}

// --- Helper functions ---

/// Format tool call as a clean header line (PI style).
/// bash → "$ ls -la", read → "read src/main.rs", edit → "edit src/main.rs"
fn tool_header(name: &str, input_json: &str) -> String {
    let Ok(v) = serde_json::from_str::<serde_json::Value>(input_json) else {
        return format!("{} {}", name, truncate_line(input_json, 60));
    };
    match name {
        "bash" => {
            let cmd = v["command"].as_str().unwrap_or("...");
            format!("$ {}", truncate_line(cmd, 80))
        }
        "read" => {
            let path = v["file_path"].as_str().map(shorten_path).unwrap_or_default();
            format!("read {}", path)
        }
        "edit" => {
            let path = v["file_path"].as_str().map(shorten_path).unwrap_or_default();
            format!("edit {}", path)
        }
        "write" => {
            let path = v["file_path"].as_str().map(shorten_path).unwrap_or_default();
            format!("write {}", path)
        }
        "dispatch" => {
            let count = v["tasks"].as_array().map(|a| a.len()).unwrap_or(0);
            format!("dispatch {} tasks", count)
        }
        _ => {
            format!("{} {}", name, truncate_line(input_json, 60))
        }
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
        let mut end = max;
        while end > 0 && !first_line.is_char_boundary(end) {
            end -= 1;
        }
        format!("{}…", &first_line[..end])
    }
}

fn format_tokens(tokens: u32) -> String {
    if tokens >= 1000 {
        format!("{:.1}k", tokens as f64 / 1000.0)
    } else {
        tokens.to_string()
    }
}

fn short_model_name(model: &str) -> String {
    model
        .strip_prefix("claude-")
        .unwrap_or(model)
        .to_string()
}
