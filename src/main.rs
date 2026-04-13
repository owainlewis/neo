mod config;
mod guard;
mod ui;

use config::Config;
use guard::DangerGuard;
use neo_coding::{coding_system_prompt, coding_tools, CompactionHook, PlanModeHook};
use neo_core::{AgentEvent, AgentState, AnthropicProvider, HookChain, Provider, Registry};
use ui::App;

use crossterm::event::{self, Event};
use std::sync::Arc;
use std::time::Duration;

struct CliArgs {
    system_prompt_path: Option<String>,
    yolo: bool,
}

fn parse_args() -> CliArgs {
    let mut args = std::env::args().skip(1);
    let mut system_prompt_path = None;
    let mut yolo = false;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--system-prompt" | "-p" => {
                system_prompt_path = args.next();
            }
            "--yolo" => {
                yolo = true;
            }
            "--help" | "-h" => {
                println!(
                    "neo — a general-purpose AI agent\n\n\
                     USAGE:\n    neo [OPTIONS]\n\n\
                     OPTIONS:\n    \
                     -p, --system-prompt <path>   Use a custom system prompt\n    \
                     --yolo                       Disable danger guard\n    \
                     -h, --help                   Show this help\n\n\
                     CONFIG:\n    \
                     ~/.neo/config.toml           Model, tokens, guard patterns"
                );
                std::process::exit(0);
            }
            _ => {}
        }
    }
    CliArgs {
        system_prompt_path,
        yolo,
    }
}

fn load_system_prompt(cli: &CliArgs, cwd: &str) -> String {
    match &cli.system_prompt_path {
        Some(path) => std::fs::read_to_string(path).unwrap_or_else(|e| {
            eprintln!("Failed to read system prompt from {}: {}", path, e);
            std::process::exit(1);
        }),
        None => coding_system_prompt(cwd),
    }
}

#[tokio::main]
async fn main() {
    let cli = parse_args();
    let config = Config::load();
    let model_name = config.model.clone();
    let max_turns = config.max_turns;

    // Channels
    let (input_tx, mut input_rx) = tokio::sync::mpsc::unbounded_channel::<String>();
    let (event_tx, event_rx) = std::sync::mpsc::channel::<AgentEvent>();

    // System prompt
    let cwd = std::env::current_dir()
        .map(|p| p.to_string_lossy().to_string())
        .unwrap_or_else(|_| ".".to_string());
    let system_prompt = load_system_prompt(&cli, &cwd);

    // Provider
    let provider: Arc<dyn Provider> =
        Arc::new(AnthropicProvider::new(config.api_key, config.model, config.max_tokens));

    // Tool registry
    let registry = Registry::new(coding_tools());

    // Hook chain: plan mode + compaction + danger guard
    let plan_mode_hook = Arc::new(PlanModeHook::new());
    let plan_enabled = plan_mode_hook.enabled();
    let compaction_hook = Arc::new(CompactionHook::new(200_000)); // ~200k context window
    let compaction_for_events = compaction_hook.clone();
    let guard = if cli.yolo {
        DangerGuard::disabled()
    } else {
        DangerGuard::load()
    };
    let hooks = HookChain::new()
        .add(plan_mode_hook.clone())
        .add(compaction_hook)
        .add(Arc::new(guard));

    // Spawn agent task
    let agent_event_tx = event_tx.clone();
    let agent_provider = provider.clone();
    let agent_plan_enabled = plan_enabled.clone();
    tokio::spawn(async move {
        let mut state = AgentState::new(max_turns, system_prompt);

        while let Some(input) = input_rx.recv().await {
            let trimmed = input.trim();

            match trimmed {
                "/clear" => {
                    state.clear();
                    let _ = agent_event_tx.send(AgentEvent::Info("Context cleared.".into()));
                    continue;
                }
                "/plan" => {
                    agent_plan_enabled.store(true, std::sync::atomic::Ordering::Relaxed);
                    let _ = agent_event_tx.send(AgentEvent::Info(
                        "Plan mode — read-only tools only. Use /execute to switch back.".into(),
                    ));
                    continue;
                }
                "/execute" => {
                    agent_plan_enabled.store(false, std::sync::atomic::Ordering::Relaxed);
                    let _ =
                        agent_event_tx.send(AgentEvent::Info("Execute mode — all tools.".into()));
                    continue;
                }
                "/model" => {
                    let _ = agent_event_tx.send(AgentEvent::Info(format!(
                        "Model: {}",
                        agent_provider.name()
                    )));
                    continue;
                }
                "/help" => {
                    let _ = agent_event_tx.send(AgentEvent::Info(
                        "/clear       Clear conversation\n\
                         /model       Show current model\n\
                         /plan        Plan mode (read-only)\n\
                         /execute     Execute mode (all tools)\n\
                         /help        Show this help\n\
                         /exit        Quit\n\
                         shift+tab    Toggle plan/execute"
                            .into(),
                    ));
                    continue;
                }
                s if s.starts_with('/') => {
                    let _ = agent_event_tx.send(AgentEvent::Warning(format!(
                        "Unknown command: {}. Type /help for options.",
                        s
                    )));
                    continue;
                }
                _ => {}
            }

            state.add_user_message(trimmed);

            let tx = agent_event_tx.clone();
            let compaction = compaction_for_events.clone();
            neo_core::run_turn(
                &mut state,
                &*agent_provider,
                &registry,
                &hooks,
                &mut |ev| {
                    // Feed usage to compaction hook so it knows when to trigger
                    if let AgentEvent::Done { usage } = ev {
                        compaction.update_usage(usage);
                    }
                    let _ = tx.send(ev.clone());
                },
            )
            .await;
        }
    });

    // Initialize terminal
    let mut terminal = ratatui::init();
    let mut app = App::new(model_name);
    app.set_plan_enabled(plan_enabled.clone());

    // Main event loop
    loop {
        let _ = terminal.draw(|f| app.draw(f));

        while let Ok(ev) = event_rx.try_recv() {
            app.handle_agent_event(ev);
        }

        if event::poll(Duration::from_millis(50)).unwrap_or(false) {
            if let Ok(Event::Key(key)) = event::read() {
                if let Some(user_input) = app.handle_key(key) {
                    let trimmed = user_input.trim().to_string();

                    if matches!(trimmed.as_str(), "/quit" | "/exit" | "/q") {
                        break;
                    }

                    app.echo_input(&trimmed);
                    app.set_processing();
                    let _ = input_tx.send(user_input);
                }
            }
        }

        if app.should_quit {
            break;
        }
    }

    ratatui::restore();
}
