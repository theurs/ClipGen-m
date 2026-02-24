# LLM Provider Support in ChatUI

[Read this in Russian | Читать на русском](LLM_PROVIDERS_RU.md)

## Implementation Details

The following architectural changes were made to support multiple AI backends:

1.  **New LLM Package**: Introduced the `internal/llm` package, providing a unified interface for various LLM providers.
2.  **Configuration Expansion**: Enhanced the `ChatSettings` struct in `internal/config/config.go` with a new `LLMProvider` field.
3.  **UI Updates**: Refactored the settings dialog in `internal/ui/settings_dialog.go` to include a provider selection dropdown (combo box).
4.  **Routing Logic**: Updated the core LLM invocation logic in `internal/ui/mainwindow.go` to dynamically route requests to the user-selected provider.

## Supported Providers

- `mistral` — Mistral AI (Default)
- `geminillm` — Google Gemini
- `ghllm` — GitHub Copilot / Chat
- `groqllm` — Groq

## Unified CLI Flag Support

All integrated LLM utilities utilize a standardized set of command-line flags. Both single-hyphen (`-`) and double-hyphen (`--`) prefixes are supported for maximum compatibility:

- `-f` / `--file` — Attach files (images, audio, text).
- `-s` / `--system-prompt` — Set the system instruction.
- `-j` / `--json` — Enable JSON response mode.
- `-m` / `--mode` — Set model operation mode.
- `-t` / `--temperature` — Adjust generation randomness.
- `-v` / `--verbose` — Enable detailed stderr logging.
- `--save-key` — Persist the API key to configuration.
- `-chat` / `--chat-id` — Set a unique session ID for context history.

## Configuration Persistence

Provider settings and model parameters are saved to the configuration file automatically. These settings are persisted on a per-chat basis, allowing for different providers and prompts in each conversation.

## Troubleshooting Note

On some Windows systems, the application may fail to launch due to a known upstream issue with the `walk` library and tooltip rendering. This is a library-level GUI conflict and is unrelated to the AI integration or provider logic. If you encounter crashes on startup, ensure your Windows environment and graphics drivers are up to date.

---
**Part of the ClipGen-m Project**
