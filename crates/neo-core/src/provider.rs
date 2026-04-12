use crate::types::{ProviderEvent, StreamRequest};
use futures::stream::BoxStream;

/// A model provider. The only method is `stream` — non-streaming providers
/// can be implemented by buffering internally and emitting all events at once.
///
/// Stream contract: items never panic. Errors are terminal `ProviderEvent::Error`
/// events. After `Done` or `Error`, the stream yields no further items.
pub trait Provider: Send + Sync {
    fn name(&self) -> &str;

    /// Begin a streaming inference call. The returned stream is `'static` — the
    /// provider clones or owns everything it needs internally.
    fn stream(&self, request: StreamRequest) -> BoxStream<'static, ProviderEvent>;
}
