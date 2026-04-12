use opus_core::{AgentEvent, Usage};
use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::prelude::*;
use ratatui::widgets::*;

const SPINNER_FRAMES: &[&str] = &["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

// --- Approval channel types ---

pub struct ApprovalRequest {
    pub tool_name: String,
    pub summary: String,
    pub responder: std::sync::mpsc::Sender<bool>,
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

    // Streaming state: accumulates text deltas and re-renders the
    // streaming section on each delta.
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

        // Banner
        app.output.push(Line::from(""));
        app.output.push(Line::from(vec![
            Span::styled("  ›_ ", Style::default().bold()),
            Span::styled("opus", Style::default().bold()),
            Span::raw(" v0.1.0"),
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
        self.output.push(Line::from(vec![
            Span::styled("  › ", Style::default().fg(Color::Cyan)),
            Span::raw(text.to_string()),
        ]));
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
                    self.output.push(Line::from(""));
                    self.streaming = true;
                    self.streaming_buffer.clear();
                    self.streaming_start_idx = Some(self.output.len());
                }
                self.streaming_buffer.push_str(&delta);

                let start = self.streaming_start_idx.unwrap();
                self.output.truncate(start);
                for line in self.streaming_buffer.split('\n') {
                    self.output.push(Line::from(format!("  {}", line)));
                }
                self.scroll_offset = 0;
            }
            AgentEvent::Text(text) => {
                self.end_streaming();
                self.output.push(Line::from(""));
                for line in text.lines() {
                    self.output.push(Line::from(format!("  {}", line)));
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
                let display_input = tool_input_summary(&name, &input);

                self.output.push(Line::from(""));
                self.output.push(Line::from(vec![
                    Span::styled(format!("  {} ", name), Style::default().bold()),
                    Span::styled(display_input, Style::default().dim()),
                ]));

                let (glyph, color) = if is_error {
                    ("✗", Color::Red)
                } else {
                    ("✓", Color::Green)
                };

                let mut spans = vec![
                    Span::styled(format!("  {} ", glyph), Style::default().fg(color)),
                    Span::styled(truncate_line(&result, 200), Style::default().dim()),
                ];

                if duration_ms > 1000 {
                    spans.push(Span::styled(
                        format!(" ({:.1}s)", duration_ms as f64 / 1000.0),
                        Style::default().dim(),
                    ));
                }

                self.output.push(Line::from(spans));
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
                self.output.push(Line::from(Span::styled(
                    format!("  error: {}", e),
                    Style::default().fg(Color::Red).bold(),
                )));
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
                self.output.push(Line::from(Span::styled(
                    format!("  {}", msg),
                    Style::default().fg(Color::Yellow),
                )));
                self.mode = Mode::Input;
                self.scroll_offset = 0;
            }
        }
    }

    // --- Key handling ---

    /// Returns Some(input) if the user submitted a line.
    pub fn handle_key(&mut self, key: KeyEvent) -> Option<String> {
        // Ctrl+C / Ctrl+D always quit
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            match key.code {
                KeyCode::Char('c') | KeyCode::Char('d') => {
                    self.should_quit = true;
                    return None;
                }
                _ => {}
            }
        }

        // Approval mode: only y/n
        if self.mode == Mode::Approval {
            match key.code {
                KeyCode::Char('y') | KeyCode::Char('Y') => {
                    if let Some(req) = self.pending_approval.take() {
                        let _ = req.responder.send(true);
                    }
                    self.output.push(Line::from(Span::styled(
                        "  Approved",
                        Style::default().fg(Color::Green).dim(),
                    )));
                    self.mode = Mode::Processing;
                }
                KeyCode::Char('n') | KeyCode::Char('N') => {
                    if let Some(req) = self.pending_approval.take() {
                        let _ = req.responder.send(false);
                    }
                    self.output.push(Line::from(Span::styled(
                        "  Denied",
                        Style::default().fg(Color::Red).dim(),
                    )));
                    self.mode = Mode::Processing;
                }
                _ => {}
            }
            return None;
        }

        // Processing mode: only scroll
        if self.mode == Mode::Processing {
            match key.code {
                KeyCode::PageUp => self.scroll_up(10),
                KeyCode::PageDown => self.scroll_down(10),
                _ => {}
            }
            return None;
        }

        // Input mode
        match key.code {
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

    pub fn draw(&mut self, frame: &mut Frame) {
        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Min(1),    // output
                Constraint::Length(1), // status bar
                Constraint::Length(1), // input
            ])
            .split(frame.area());

        self.draw_output(frame, chunks[0]);
        self.draw_status(frame, chunks[1]);
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

    fn draw_status(&mut self, frame: &mut Frame, area: Rect) {
        let mut parts: Vec<Span> = vec![
            Span::styled(" opus", Style::default().bold().fg(Color::White)),
            Span::styled(" · ", Style::default().fg(Color::DarkGray)),
            Span::styled(
                short_model_name(&self.model),
                Style::default().fg(Color::White),
            ),
        ];

        if self.usage.input_tokens > 0 || self.usage.output_tokens > 0 {
            parts.push(Span::styled(" · ", Style::default().fg(Color::DarkGray)));
            parts.push(Span::raw(format!(
                "{} in · {} out",
                format_tokens(self.usage.input_tokens),
                format_tokens(self.usage.output_tokens)
            )));
        }

        if self.plan_mode {
            parts.push(Span::styled(" · ", Style::default().fg(Color::DarkGray)));
            parts.push(Span::styled(
                "PLAN",
                Style::default().fg(Color::Yellow).bold(),
            ));
        }

        if self.mode == Mode::Processing {
            self.spinner_frame = (self.spinner_frame + 1) % SPINNER_FRAMES.len();
            parts.push(Span::raw(" "));
            parts.push(Span::styled(
                SPINNER_FRAMES[self.spinner_frame],
                Style::default().fg(Color::Cyan),
            ));
        }

        let bar = Paragraph::new(Line::from(parts))
            .style(Style::default().bg(Color::Rgb(30, 30, 30)));
        frame.render_widget(bar, area);
    }

    fn draw_input(&self, frame: &mut Frame, area: Rect) {
        if self.mode == Mode::Approval {
            if let Some(ref req) = self.pending_approval {
                let line = Line::from(vec![
                    Span::styled(" ", Style::default()),
                    Span::styled(&req.tool_name, Style::default().bold().fg(Color::Yellow)),
                    Span::styled(
                        format!(" {} ", req.summary),
                        Style::default().dim().fg(Color::Yellow),
                    ),
                    Span::styled(
                        "Allow? [y/n]",
                        Style::default().bold().fg(Color::Yellow),
                    ),
                ]);
                frame.render_widget(Paragraph::new(line), area);
                return;
            }
        }

        let line = Line::from(vec![
            Span::styled(" › ", Style::default().fg(Color::Cyan)),
            Span::raw(self.input.clone()),
        ]);
        frame.render_widget(Paragraph::new(line), area);

        if self.mode == Mode::Input {
            let display_pos = self.input[..self.cursor].chars().count() as u16;
            frame.set_cursor_position((area.x + 3 + display_pos, area.y));
        }
    }
}

// --- Helper functions ---

pub fn tool_input_summary(name: &str, input_json: &str) -> String {
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
            .map(shorten_path)
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
