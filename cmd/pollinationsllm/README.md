# Pollinations CLI

[Read this in Russian | Читать на русском](README_RU.md)

Pollinations CLI is a robust command-line interface for the Pollinations API. It features advanced tool-calling capabilities, including a Lua-powered engine for complex scripting and mathematical operations, as well as real-time web search integration via Tavily.

## Key Features

- **Autonomous Tool Calling**: Built-in support for dynamic tool execution to handle complex tasks:
  - **Lua Scripting/Math**: Execute sophisticated mathematical formulas, algorithms, and logic via an isolated Lua environment.
  - **Live Web Search**: Integrated Tavily API support for fetching up-to-date information from the web.
  - **Pollinations Search**: Native integration with the `gemini-search` model for deep information retrieval.
- **Versatile Workloads**: Dedicated modes for `general`, `code`, `vision`, `audio`, and `ocr`.
- **Persistent Chat**: Stateful conversation support with local history management.
- **Rich Media Support**: Seamlessly process images, audio, and text files.
- **Smart Audio Processing**: Recommends system-wide `ffmpeg` for pre-converting audio to high-compatibility formats (MP3, WAV).
- **API Key Load Balancing**: Supports multiple Tavily keys with randomized rotation to distribute API load effectively.

## Installation

1. Download the `plnllm.exe` binary.
2. (Optional) Configure your API keys (see the "Key Management" section below).

## Usage

### Basic Prompting

```bash
# Standard query (Tools are enabled by default)
echo "Hey! How's it going?" | plnllm

# Process a text file
plnllm -f data.txt

# Process an image
plnllm -f screenshot.jpg

# Multi-file input
plnllm -f notes.txt -f chart.png
```

### Operation Modes

```bash
# General mode (Default)
echo "Explain the butterfly effect" | plnllm

# Code mode
echo "Explain this Python decorator" | plnllm -m code

# Vision mode (Image analysis)
plnllm -f photo.jpg -m vision

# Audio mode (Transcriptions and analysis)
plnllm -f memo.mp3 -m audio

# OCR mode (Text extraction from PDFs or images)
plnllm -f document.pdf -m ocr
```

### Command-Line Arguments

- `-f <file>`: Attach a file (can be used multiple times).
- `-s "prompt"`: Override the default system prompt.
- `-j`: Force JSON output format.
- `-m <mode>`: Operation mode (`auto`, `general`, `code`, `ocr`, `audio`, `vision`).
- `-t <value>`: Set temperature (0.0 - 2.0).
- `-v`: Enable verbose logging to `stderr`.
- `-save-key <KEY>`: Save your Pollinations API key and exit.
- `-add-tavily-key <KEY>`: Append a Tavily API key and exit.
- `-chat <ID>`: Specify a Chat ID to maintain conversation context.
- `-clear-chat <ID>`: Wipe history for a specific chat session.
- `-no-tools`: Disable the autonomous tool-calling engine.

### Using Built-in Tools

The CLI automatically determines when to trigger tools based on your request:

```bash
# Scripting: Mathematical operations
echo "What is 25 * 36 + 17 * 42?" | plnllm

# Scripting: Algorithmic tasks
echo "Calculate the factorial of 15 using Lua" | plnllm

# Web Search: Current events
echo "What are the latest updates on the Go programming language?" | plnllm

# Web Search: Market data
echo "What is the current stock price for Apple?" | plnllm

# Hybrid Query: Search + Math
echo "Find the current exchange rate for BTC to USD and calculate the value of 0.5 BTC" | plnllm
```

### Chat Mode

```bash
# Start a new conversation
echo "Let's talk about space" | plnllm -chat space_convo

# Continue the conversation (Context is preserved)
echo "How far is Mars from Earth?" | plnllm -chat space_convo

# Reset a chat session
plnllm -clear-chat space_convo

# Reset chat via inline command
echo "/clear" | plnllm -chat space_convo
```

## Configuration

### File Paths

- **Main Config**: `%APPDATA%\clipgen-m\pollinations.conf`
- **Tavily Config**: `%APPDATA%\clipgen-m\tavily.conf`
- **Chat History**: `%APPDATA%\clipgen-m\mistral_chats\`
- **Error Logs**: `%APPDATA%\clipgen-m\pollinations_err.log`

### `pollinations.conf` Format

The configuration uses a standard JSON format:

```json
{
  "api_keys": ["your_pollinations_api_key"],
  "base_url": "https://gen.pollinations.ai/v1",
  "system_prompt": "You are an AI assistant integrated into ClipGen-m. Be concise. No conversational filler. Return plain text without markdown.",
  "temperature": 0.7,
  "max_tokens": 8000,
  "models": {
    "general": ["gemini"],
    "code": ["gemini"],
    "vision": ["gemini"],
    "audio": ["gemini"],
    "ocr": ["gemini"]
  },
  "chat_history_max_messages": 30,
  "chat_history_max_chars": 50000
}
```

## Key Management

### Managing API Keys

```bash
# Save your primary Pollinations API key
plnllm -save-key YOUR_KEY_HERE

# Add a Tavily API key (used for web search)
plnllm -add-tavily-key tvly-abcdefghijklmnopqrstuvwxyz
```

You can add multiple Tavily keys; the application will rotate through them randomly to optimize rate limits.

## Tool Engine Details

### Lua Scripting Engine
- Provides high-precision mathematical execution.
- Supports standard Lua libraries for complex data manipulation.
- Runs in a secure, isolated environment.

### Web Search (Tavily & Pollinations)
- Performs real-time web crawling.
- Returns concise summaries and top-ranked results.
- Uses a two-stage fallback system: attempts `gemini-search` on Pollinations first, then utilizes Tavily for broader coverage.

## Troubleshooting

1. **"No input provided"**: Ensure you are piping data via `stdin` or using the `-f` flag.
2. **"No API keys"**: You must configure at least one key via `-add-tavily-key`.
3. **Lua Errors**: Ensure the `lua-executor` binary is in the correct path or integrated into the main binary.
4. **Rate Limiting**: If you encounter 429 errors, consider adding more Tavily keys for better rotation.

## Development

Built with Go. To compile from source:

```bash
go build -o plnllm.exe main.go
```

## License

This project is licensed under the MIT License.

---
**Part of the ClipGen-m Project**
