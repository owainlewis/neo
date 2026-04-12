mod approval;
mod config;
mod ui;

use approval::ApprovalHook;
use config::Config;
use neo_coding::{coding_system_prompt, coding_tools, DispatchTool, PlanModeHook};
use neo_core::{
    AgentEvent, AgentState, AnthropicProvider, DefaultSpawner, HookChain, Provider, Registry,
};
use ui::{App, ApprovalRequest};

use crossterm::event::{self, Event};
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

struct CliArgs {
    system_prompt_path: Option<String>,
}

fn parse_args() -> CliArgs {
    let mut args = std::env::args().skip(1);
    let mut system_prompt_path = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--system-prompt" | "-p" => {
                system_prompt_path = args.next();
            }
            "--help" | "-h" => {
                println!(
                    "neo — a rust coding agent\n\n\
                     USAGE:\n    neo [--system-prompt <path>]\n\n\
                     OPTIONS:\n    \
                     -p, --system-prompt <path>   Use a custom system prompt (pure agent mode)\n    \
                     -h, --help                   Show this help"
                );
                std::process::exit(0);
            }
            _ => {}
        }
    }
    CliArgs { system_prompt_path }
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
    let (approval_tx, approval_rx) = std::sync::mpsc::channel::<ApprovalRequest>();

    // System prompt
    let cwd = std::env::current_dir()
        .map(|p| p.to_string_lossy().to_string())
        .unwrap_or_else(|_| ".".to_string());
    let system_prompt = load_system_prompt(&cli, &cwd);

    // Provider (shared between parent agent and subagent spawner)
    let provider: Arc<dyn Provider> =
        Arc::new(AnthropicProvider::new(config.api_key, config.model, config.max_tokens));

    // Subagent spawner: children get basic tools (no dispatch — prevents
    // recursive spawning). They run with NoHooks (no approval prompts).
    let subagent_registry = Arc::new(Registry::new(coding_tools()));
    let spawner = Arc::new(DefaultSpawner::new(
        provider.clone(),
        subagent_registry,
        system_prompt.clone(),
    ));

    // Parent agent: coding tools + dispatch for parallel workers
    let mut parent_tools = coding_tools();
    parent_tools.push(Box::new(DispatchTool::new(spawner)));
    let registry = Registry::new(parent_tools);
    let write_tool_names = registry.write_tool_names();

    // Hook chain
    let plan_mode_hook = Arc::new(PlanModeHook::new());
    let plan_enabled = plan_mode_hook.enabled();
    let approval_hook = Arc::new(ApprovalHook {
        approval_tx,
        write_tool_names,
    });
    let hooks = HookChain::new()
        .add(plan_mode_hook.clone())
        .add(approval_hook);

    // Spawn agent task
    let agent_event_tx = event_tx.clone();
    let agent_provider = provider.clone();
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
                    plan_enabled.store(true, Ordering::Relaxed);
                    let _ = agent_event_tx.send(AgentEvent::Info(
                        "Plan mode — read-only tools only. Use /execute to switch back.".into(),
                    ));
                    continue;
                }
                "/execute" => {
                    plan_enabled.store(false, Ordering::Relaxed);
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
                        "/clear     Clear conversation\n\
                         /model     Show current model\n\
                         /plan      Plan mode (read-only)\n\
                         /execute   Execute mode (all tools)\n\
                         /help      Show this help\n\
                         /exit      Quit"
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
            neo_core::run_turn(
                &mut state,
                &*agent_provider,
                &registry,
                &hooks,
                &mut |ev| {
                    let _ = tx.send(ev.clone());
                },
            )
            .await;
        }
    });

    // Initialize terminal
    let mut terminal = ratatui::init();
    let mut app = App::new(model_name);

    // Main event loop
    loop {
        let _ = terminal.draw(|f| app.draw(f));

        if let Ok(req) = approval_rx.try_recv() {
            app.set_approval(req);
        }

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

                    if trimmed == "/plan" {
                        app.plan_mode = true;
                    }
                    if trimmed == "/execute" {
                        app.plan_mode = false;
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
