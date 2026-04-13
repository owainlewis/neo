pub mod bash;
pub mod dispatch;
pub mod edit;
pub mod glob;
pub mod grep;
pub mod read;
pub mod write;

use neo_core::Tool;

/// The default tool set for the coding bundle: bash, read, edit, write, grep, glob.
/// Does *not* include dispatch — the binary adds that separately with a
/// configured `SubagentSpawner`, preventing circular tool injection.
pub fn coding_tools() -> Vec<Box<dyn Tool>> {
    vec![
        Box::new(bash::BashTool),
        Box::new(read::ReadTool),
        Box::new(edit::EditTool),
        Box::new(write::WriteTool),
        Box::new(grep::GrepTool),
        Box::new(glob::GlobTool),
    ]
}
