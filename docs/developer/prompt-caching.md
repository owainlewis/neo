# Prompt Caching

Neo can pass the system prompt as ordered `llm.SystemBlock` values.

The flattened `Request.System` string remains available for providers that do not support structured system prompts. Providers that do support caching should prefer `Request.SystemBlocks` when present.

## Current Block Layout

1. Static base instructions plus skill catalog. This block is marked cacheable when `features.prompt_caching` is enabled.
2. Dynamic AGENTS.md project context. This block is not marked cacheable.

The goal is to cache stable instructions without letting project instructions evict that prefix.
