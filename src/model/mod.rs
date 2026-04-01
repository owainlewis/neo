pub mod anthropic;
pub mod types;

use crate::model::types::*;
use futures::Stream;
use std::pin::Pin;

#[async_trait::async_trait]
pub trait Provider: Send + Sync {
    async fn stream(
        &self,
        system: &str,
        messages: &[Message],
        tools: &[ToolDefinition],
    ) -> Pin<Box<dyn Stream<Item = StreamEvent> + Send>>;

    fn name(&self) -> &str;
    fn context_window(&self) -> usize;
}
