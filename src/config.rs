use std::collections::HashMap;
use std::path::PathBuf;

const DEFAULT_MODEL: &str = "claude-opus-4-6";
const DEFAULT_MAX_TURNS: usize = 100;
const DEFAULT_MAX_TOKENS: u32 = 16384;

pub struct Config {
    pub model: String,
    pub api_key: String,
    pub max_turns: usize,
    pub max_tokens: u32,
}

impl Config {
    /// Load config with priority: env vars > .env file > config file > defaults
    pub fn load() -> Self {
        let dotenv = load_dotenv();
        let file_config = load_config_file();

        let api_key = resolve("ANTHROPIC_API_KEY", &dotenv, &file_config, None)
            .unwrap_or_else(|| {
                eprintln!("Error: ANTHROPIC_API_KEY not set.");
                eprintln!("Set it in ~/.neo/config.toml, .env, or export it.");
                std::process::exit(1);
            });

        let model = resolve("model", &dotenv, &file_config, Some(DEFAULT_MODEL));
        let max_turns = resolve("max_turns", &dotenv, &file_config, None)
            .and_then(|v| v.parse().ok())
            .unwrap_or(DEFAULT_MAX_TURNS);
        let max_tokens = resolve("max_tokens", &dotenv, &file_config, None)
            .and_then(|v| v.parse().ok())
            .unwrap_or(DEFAULT_MAX_TOKENS);

        Config {
            model: model.unwrap(),
            api_key,
            max_turns,
            max_tokens,
        }
    }
}

/// Resolve a value: env var (uppercased) > .env > config file > default
fn resolve(
    key: &str,
    dotenv: &HashMap<String, String>,
    file_config: &HashMap<String, String>,
    default: Option<&str>,
) -> Option<String> {
    // Env vars use uppercase (e.g. ANTHROPIC_API_KEY, NEO_MODEL)
    let env_key = if key == "model" {
        "NEO_MODEL".to_string()
    } else if key.contains('_') && key.chars().all(|c| c.is_uppercase() || c == '_') {
        key.to_string()
    } else {
        format!("NEO_{}", key.to_uppercase())
    };

    std::env::var(&env_key)
        .ok()
        .or_else(|| dotenv.get(&env_key).cloned())
        .or_else(|| dotenv.get(key).cloned())
        .or_else(|| file_config.get(key).cloned())
        .or_else(|| default.map(String::from))
}

fn config_path() -> PathBuf {
    dirs_home().join(".neo").join("config.toml")
}

fn dirs_home() -> PathBuf {
    std::env::var("HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from("."))
}

/// Parse a minimal TOML file (flat key = "value" pairs only)
fn load_config_file() -> HashMap<String, String> {
    let path = config_path();
    let Ok(content) = std::fs::read_to_string(&path) else {
        return Default::default();
    };
    parse_flat_toml(&content)
}

fn load_dotenv() -> HashMap<String, String> {
    let Ok(content) = std::fs::read_to_string(".env") else {
        return Default::default();
    };
    content
        .lines()
        .filter_map(|line| {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                return None;
            }
            let (key, value) = line.split_once('=')?;
            let value = value.trim().trim_matches('"').trim_matches('\'');
            Some((key.trim().to_string(), value.to_string()))
        })
        .collect()
}

fn parse_flat_toml(content: &str) -> HashMap<String, String> {
    content
        .lines()
        .filter_map(|line| {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') || line.starts_with('[') {
                return None;
            }
            let (key, value) = line.split_once('=')?;
            let value = value.trim().trim_matches('"').trim_matches('\'');
            Some((key.trim().to_string(), value.to_string()))
        })
        .collect()
}
