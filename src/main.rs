mod agent;
mod config;
mod model;
mod prompt;
mod tools;
mod ui;

use agent::AgentState;
use config::Config;
use model::anthropic::AnthropicProvider;
use model::Provider;
use tools::Registry;
use ui::Renderer;

#[tokio::main]
async fn main() {
    let config = Config::load();

    let provider = AnthropicProvider::new(config.api_key, config.model, config.max_tokens);
    let registry = Registry::new(Some(Box::new(|tool_name, _id, input| {
        ui::prompt_approval(tool_name, input)
    })));
    let mut state = AgentState::new(config.max_turns);
    let mut renderer = Renderer::new();

    renderer.banner(provider.name());

    let mut rl = rustyline::DefaultEditor::new().expect("Failed to create editor");

    loop {
        match rl.readline(&ui::prompt_string()) {
            Ok(line) => {
                let input = line.trim();
                if input.is_empty() {
                    continue;
                }
                let _ = rl.add_history_entry(input);

                // Handle slash commands
                if input.starts_with('/') {
                    match handle_command(input, &mut state, &provider, &mut renderer) {
                        CommandResult::Continue => continue,
                        CommandResult::Exit => break,
                    }
                }

                state.add_user_message(input);

                let mut event_handler = |event: &agent::AgentEvent| {
                    renderer.handle_event(event);
                };

                agent::run_turn(&mut state, &provider, &registry, &mut event_handler).await;
            }
            Err(rustyline::error::ReadlineError::Interrupted) => {
                renderer.goodbye();
                break;
            }
            Err(rustyline::error::ReadlineError::Eof) => {
                renderer.goodbye();
                break;
            }
            Err(e) => {
                eprintln!("Error: {}", e);
                break;
            }
        }
    }
}

enum CommandResult {
    Continue,
    Exit,
}

fn handle_command(
    input: &str,
    state: &mut AgentState,
    provider: &AnthropicProvider,
    renderer: &mut Renderer,
) -> CommandResult {
    let parts: Vec<&str> = input.splitn(2, ' ').collect();
    let cmd = parts[0];

    match cmd {
        "/clear" => {
            state.clear();
            renderer.info("Context cleared.");
            CommandResult::Continue
        }
        "/model" => {
            renderer.info(&format!("Current model: {}", provider.name()));
            CommandResult::Continue
        }
        "/help" => {
            println!();
            println!("  {}Commands:{}", ui::BOLD, ui::RESET);
            println!("  /clear   Clear conversation history");
            println!("  /model   Show current model");
            println!("  /help    Show this help");
            println!("  /exit    Quit");
            println!();
            CommandResult::Continue
        }
        "/exit" | "/quit" | "/q" => {
            renderer.goodbye();
            CommandResult::Exit
        }
        _ => {
            renderer.warn(&format!("Unknown command: {}. Type /help for options.", cmd));
            CommandResult::Continue
        }
    }
}
