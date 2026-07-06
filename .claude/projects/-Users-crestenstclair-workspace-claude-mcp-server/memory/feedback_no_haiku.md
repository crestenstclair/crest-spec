---
name: feedback-no-haiku
description: Haiku is too weak for crest-spec code generation — produces empty output or missing code blocks. Model hierarchy is sonnet (default) + opus (complex).
metadata:
  type: feedback
---

Never use haiku for crest-spec code generation. It fails every time — empty output or no code blocks.

**Why:** First real end-to-end run (2026-06-07) assigned haiku to "simple" resources (value objects). All 13 resources in wave 0 were rejected after 4 attempts each. Even the model_overrides prompt suggests haiku, which is wrong.

**How to apply:** Default model is sonnet. Opus for complex resources. Remove any references to haiku as viable for code generation in prompts, docs, and orchestrator instructions. The model_overrides guidance in orchestrator instructions should say sonnet (default) + opus (complex), not mention haiku.
