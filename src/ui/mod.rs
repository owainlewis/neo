use neo_core::{AgentEvent, Usage};
use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::prelude::*;
use ratatui::widgets::*;

const SPINNER_FRAMES: &[&str] = &["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const MAX_RESULT_LINES: usize = 4;

// --- Approval channel types ---

pub struct ApprovalRequest {
    pub tool_name: String,
    pub summary: String,
    pub responder: tokio::sync::oneshot::Sender<bool>,
}

// --- App state ---

#[derive(PartialEq)]
enum Mode {
    Input,
    Processing,
    Approval,
}

pub struct App {
    output: Vec<Line<'static>>,
    scroll_offset: usize,

    input: String,
    cursor: usize,
    history: Vec<String>,
    history_idx: Option<usize>,

    mode: Mode,
    pending_approval: Option<ApprovalRequest>,
    spinner_frame: usize,

    streaming: bool,
    streaming_buffer: String,
    streaming_start_idx: Option<usize>,

    pub model: String,
    pub usage: Usage,
    pub plan_mode: bool,
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
            pending_approval: None,
            spinner_frame: 0,
            streaming: false,
            streaming_buffer: String::new(),
            streaming_start_idx: None,
            model,
            usage: Usage::default(),
            plan_mode: false,
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

    pub fn set_processing(&mut self) {
        self.mode = Mode::Processing;
    }

    pub fn set_approval(&mut self, req: ApprovalRequest) {
        self.pending_approval = Some(req);
        self.mode = Mode::Approval;
    }

    pub fn echo_input(&mut self, text: &str) {
        self.output.push(Line::from(""));
        for (i, line) in text.split('\n').enumerate() {
            let prefix = if i == 0 { "  > " } else { "  : " };
            self.output.push(Line::from(vec![
                Span::styled(prefix, Style::default().fg(Color::White).bold()),
                Span::styled(line.to_string(), Style::default().fg(Color::White).bold()),
            ]));
        }
        self.output.push(Line::from(""));
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
                for line in self.streaming_buffer.split('\n') {
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
                let summary = tool_input_summary(&name, &input);

                // Tool header: ● ToolName(summary)
                let (bullet, bullet_color) = if is_error {
                    ("✗", Color::Red)
                } else {
                    ("●", Color::Green)
                };

                self.output.push(Line::from(""));

                let mut header = vec![
                    Span::styled(
                        format!("  {} ", bullet),
                        Style::default().fg(bullet_color),
                    ),
                    Span::styled(
                        capitalize(&name),
                        Style::default().fg(bullet_color).bold(),
                    ),
                ];
                if !summary.is_empty() {
                    header.push(Span::styled(
                        format!("({})", summary),
                        Style::default().dim(),
                    ));
                }
                if duration_ms > 1000 {
                    header.push(Span::styled(
                        format!(" {:.1}s", duration_ms as f64 / 1000.0),
                        Style::default().dim(),
                    ));
                }
                self.output.push(Line::from(header));

                // Tool result lines with tree indent
                let result_lines: Vec<&str> = result.lines().collect();
                let show = result_lines.len().min(MAX_RESULT_LINES);
                let hidden = result_lines.len().saturating_sub(MAX_RESULT_LINES);

                for (i, line) in result_lines[..show].iter().enumerate() {
                    let connector = if i == show - 1 && hidden == 0 {
                        "└ "
                    } else {
                        "│ "
                    };
                    self.output.push(Line::from(vec![
                        Span::styled(
                            format!("    {} ", connector),
                            Style::default().fg(Color::Rgb(60, 60, 70)),
                        ),
                        Span::styled(
                            truncate_line(line, 120),
                            Style::default().fg(Color::Rgb(150, 150, 165)),
                        ),
                    ]));
                }

                if hidden > 0 {
                    self.output.push(Line::from(vec![
                        Span::styled(
                            "    └ ".to_string(),
                            Style::default().fg(Color::DarkGray),
                        ),
                        Span::styled(
                            format!("… +{} lines", hidden),
                            Style::default().dim().italic(),
                        ),
                    ]));
                }

                self.scroll_offset = 0;
            }
            AgentEvent::Done { usage } => {
                self.end_streaming();
                self.usage = usage;
                self.mode = Mode::Input;
                self.output.push(Line::from(""));
                self.scroll_offset = 0;
            }
            AgentEvent::Error(e) => {
                self.end_streaming();
                self.output.push(Line::from(""));
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

        if self.mode == Mode::Approval {
            match key.code {
                KeyCode::Char('y') | KeyCode::Char('Y') => {
                    if let Some(req) = self.pending_approval.take() {
                        let _ = req.responder.send(true);
                    }
                    self.mode = Mode::Processing;
                }
                KeyCode::Char('n') | KeyCode::Char('N') => {
                    if let Some(req) = self.pending_approval.take() {
                        let _ = req.responder.send(false);
                    }
                    self.output.push(Line::from(Span::styled(
                        "    denied",
                        Style::default().fg(Color::Red).dim(),
                    )));
                    self.mode = Mode::Processing;
                }
                _ => {}
            }
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

    fn draw_output(&self, frame: &mut Frame, area: Rect) {
        let h = area.height as usize;
        let total = self.output.len();
        let end = total.saturating_sub(self.scroll_offset);
        let start = end.saturating_sub(h);

        let visible: Vec<Line> = self.output[start..end].to_vec();
        let paragraph = Paragraph::new(visible).wrap(Wrap { trim: false });
        frame.render_widget(paragraph, area);
    }

    fn draw_separator(&self, frame: &mut Frame, area: Rect) {
        let sep = "─".repeat(area.width as usize);
        let line = Paragraph::new(Line::from(Span::styled(
            sep,
            Style::default().fg(Color::Rgb(50, 50, 50)),
        )));
        frame.render_widget(line, area);
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

        if self.mode == Mode::Approval {
            if let Some(ref req) = self.pending_approval {
                let line = Line::from(vec![
                    Span::styled("  ? ", Style::default().fg(Color::Yellow).bold()),
                    Span::styled(
                        capitalize(&req.tool_name),
                        Style::default().fg(Color::Yellow).bold(),
                    ),
                    Span::styled(
                        format!("({}) ", req.summary),
                        Style::default().fg(Color::Yellow).dim(),
                    ),
                    Span::styled("[y/n]", Style::default().fg(Color::Yellow)),
                ]);
                frame.render_widget(
                    Paragraph::new(line).style(Style::default().bg(input_bg)),
                    content_area,
                );
                return;
            }
        }

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
            if self.plan_mode {
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
        if self.plan_mode {
            parts.push("PLAN".into());
        }
        parts.join(" · ")
    }
}

// --- Helper functions ---

pub fn tool_input_summary(name: &str, input_json: &str) -> String {
    let Ok(v) = serde_json::from_str::<serde_json::Value>(input_json) else {
        return truncate_line(input_json, 60);
    };
    match name {
        "bash" => v["command"]
            .as_str()
            .map(|c| truncate_line(c, 60))
            .unwrap_or_default(),
        "read" | "edit" | "write" => v["file_path"]
            .as_str()
            .map(shorten_path)
            .unwrap_or_default(),
        "dispatch" => {
            let count = v["tasks"].as_array().map(|a| a.len()).unwrap_or(0);
            format!("{} tasks", count)
        }
        _ => truncate_line(input_json, 60),
    }
}

fn capitalize(s: &str) -> String {
    let mut chars = s.chars();
    match chars.next() {
        None => String::new(),
        Some(c) => c.to_uppercase().collect::<String>() + chars.as_str(),
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
