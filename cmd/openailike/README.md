# OpenAI-like CLI Utility (openailike)

[Read this in Russian | Читать на русском](README_RU.md)

A command-line interface for **any OpenAI-compatible backend**: OpenAI itself, Ollama, LM Studio, vLLM, OpenRouter, Together, Groq, LiteLLM, or your own proxy. The address of the API is set in the config, so the same binary works with cloud services and with local models.

## Key Features

*   **Universal Compatibility**: Works with anything that implements the `POST /v1/chat/completions` and `GET /v1/models` endpoints.
*   **Local Models**: No API key required for local servers (Ollama, LM Studio) — the `Authorization` header is simply omitted.
*   **Model Auto-Discovery**: If the config does not list models, they are requested from the server via `GET /v1/models`.
*   **Unified Flags**: The same command-line interface as `geminillm` and `groqllm` (see [UNIFIED_FLAGS.md](../../UNIFIED_FLAGS.md)).
*   **Key Rotation**: Multiple API keys with automatic failover on `401`/`429` errors.
*   **Multimodal**: Images are sent as `image_url` (data URI), text files are inlined, audio is transcribed via `/v1/audio/transcriptions`.
*   **Unified Chat History**: Chat history is stored in the shared `%AppData%\clipgen-m\llm_chats\` directory, so a conversation can be continued in another utility.

## Installation

```bash
cd cmd/openailike
call build.bat
```

## Configuration

The config file is `%AppData%\clipgen-m\openai.conf` and is created automatically on first run:

```json
{
  "api_keys": [
    "sk-..."
  ],
  "base_url": "https://api.openai.com/v1",
  "system_prompt": "You are a helpful assistant.",
  "temperature": 0.6,
  "models": {
    "general": [],
    "code": [],
    "vision": [],
    "ocr": [],
    "audio": ["whisper-1"]
  },
  "chat_history_max_messages": 30
}
```

*   `base_url` — address of the OpenAI-compatible API (without a trailing slash).
*   `api_keys` — list of keys. Leave it empty (`[""]`) for local servers without authorization.
*   `models` — models per mode. An empty list means auto-discovery via `GET /v1/models`.

### Popular backends

| Backend | `base_url` |
|---|---|
| OpenAI | `https://api.openai.com/v1` |
| Ollama | `http://localhost:11434/v1` |
| LM Studio | `http://localhost:1234/v1` |
| vLLM | `http://localhost:8000/v1` |
| OpenRouter | `https://openrouter.ai/api/v1` |
| Together | `https://api.together.xyz/v1` |
| Groq | `https://api.groq.com/openai/v1` |

### Saving a key

```cmd
openailike.exe --save-key sk-xxxxxxxxxxxxxxxx
```

## Usage

```bash
# Simple question
echo "How does Go work?" | openailike.exe

# With a system prompt and temperature
echo "Translate to English" | openailike.exe -s "You are a translator" -t 0.3

# JSON mode
echo "Give me 3 facts about Mars" | openailike.exe -j

# With an image
openailike.exe -f "chart.png" -s "Describe the chart"

# Stateful chat
echo "My name is Alex" | openailike.exe -chat session_1
echo "What is my name?" | openailike.exe -chat session_1

# List the models available on the server
openailike.exe --list-models

# Use a specific model
echo "Hi" | openailike.exe --model gpt-4o-mini

# Override the address for a single run
echo "Hi" | openailike.exe --base-url http://localhost:11434/v1
```

## Flags

| Flag | Description |
|---|---|
| `-f` / `--file` | Attach a file (can be repeated) |
| `-s` / `--system` / `--system-prompt` | System instruction |
| `-j` / `--json` | JSON response mode |
| `-m` / `--mode` | `auto`, `general`, `code`, `vision`, `ocr`, `audio` |
| `-t` / `--temp` / `--temperature` | Sampling temperature |
| `-v` / `--verbose` | Detailed logging to `stderr` |
| `--save-key` | Save an API key to the config and exit |
| `-chat` / `--chat-id` | Chat session ID (history) |
| `--base-url` / `--url` | Override the API address |
| `--model` | Use a specific model (overrides the config list) |
| `--list-models` | Print the models available on the server |

## Storage & Logs

*   **Config**: `%AppData%\clipgen-m\openai.conf`
*   **Error Logs**: `%AppData%\clipgen-m\openai_err.log`
*   **Chat History**: `%AppData%\clipgen-m\llm_chats\` (shared with other modules)

---

**Part of the ClipGen-m Project**
