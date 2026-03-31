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
    let registry = Registry::new();
    let mut state = AgentState::new(config.max_turns);
    let mut renderer = Renderer::new();

    renderer.banner(provider.name());

    let mut rl = rustyline::DefaultEditor::new().expect("Failed to create editor");

    loop {
        let readline = rl.readline(&ui::prompt_string());
        match readline {
            Ok(line) => {
                let input = line.trim();
                if input.is_empty() {
                    continue;
                }
                let _ = rl.add_history_entry(input);

                state.add_user_message(input);

                let mut event_handler = |event: &agent::AgentEvent| {
                    renderer.handle_event(event);
                };

                agent::run_turn(&mut state, &provider, &registry, &mut event_handler).await;
            }
            Err(rustyline::error::ReadlineError::Interrupted) => continue,
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
