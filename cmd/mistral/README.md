# Mistral CLI

[Read this in Russian | Читать на русском](README_RU.md)

Mistral CLI is a powerful command-line interface for interacting with the Mistral AI API. It features advanced autonomous tool-calling, Lua-based scripting for complex logic, and real-time web search integration via Tavily.

## Key Features

- **Autonomous Tool Calling**: Built-in engine that dynamically executes tasks:
  - **Scripting & Math**: Uses an isolated Lua environment to perform high-precision calculations, run algorithms, and execute custom logic.
  - **Live Web Search**: Integrated Tavily API support for fetching real-time data and news.
- **Specialized Workloads**: Optimized modes for `general` chat, `code` generation, `vision` (images), `audio`, and `ocr`.
- **Stateful Conversations**: Full support for persistent chat history with local session management.
- **Multimedia Processing**: Native handling of images, audio files, and plain text.
- **Smart Audio Support**: Seamlessly integrates with system-wide `ffmpeg` to ensure compatibility across various audio formats (converting to MP3/WAV automatically).
- **High Availability**:
  - **Model Failover**: Automatically switches to backup models if the primary model encounters an error.
  - **Key Load Balancing**: Supports multiple API keys with automatic failover for Mistral and randomized rotation for Tavily to optimize rate limits.

## Installation

1. Download the `mistral.exe` binary.
2. **Prerequisites**:
   - Modern Go environment (required for the Lua executor if running scripts locally).
3. (Optional) Configure your API keys (see the "Key Management" section).

## Usage

### Basic Prompting

```bash
# Standard query (Tools are enabled by default)
echo "How's the weather in London?" | mistral

# Process a text file
mistral -f summary.txt

# Analyze an image
mistral -f photo.jpg

# Multi-file context
mistral -f context.txt -f data.csv -f chart.png
```

### Operation Modes

```bash
# General mode (Default)
echo "Explain quantum entanglement" | mistral

# Coding assistant mode
echo "Refactor this Go function" | mistral -m code

# Vision mode (Image analysis)
mistral -f screenshot.png -m vision

# Audio mode (Transcription/Analysis)
mistral -f lecture.mp3 -m audio

# OCR mode (Extracting text from PDF/Images)
mistral -f scan.pdf -m ocr
```

### Command-Line Arguments

- `-f <file>`: Attach a file (can be used multiple times).
- `-s "prompt"`: Define a custom system prompt (overrides config).
- `-j`: Force JSON output format.
- `-m <mode>`: Set operation mode (`auto`, `general`, `code`, `ocr`, `audio`, `vision`).
- `-t <value>`: Set temperature (0.0 - 2.0).
- `-v`: Enable verbose logging to `stderr`.
- `-save-key <KEY>`: Save your Mistral API key and exit.
- `-add-tavily-key <KEY>`: Append a Tavily API key and exit.
- `-chat <ID>`: Specify a unique Chat ID for persistent context.
- `-clear-chat <ID>`: Wipe history for a specific chat.
- `-no-tools`: Disable the autonomous tool-calling engine.

### Using Built-in Tools

Mistral CLI automatically triggers tools based on the nature of your request:

```bash
# Math & Logic: Lua Execution
echo "Calculate the Fibonacci sequence up to the 10th number" | mistral

# Math & Logic: High-precision math
echo "What is (45 * 12) / (7 + 3.5)?" | mistral

# Web Search: Real-time information
echo "What are the trending topics in AI today?" | mistral

# Web Search: Market data
echo "Find the current stock price of NVIDIA" | mistral

# Hybrid Query: Search + Math
echo "Find the current price of Ethereum and tell me how much 2.5 ETH is worth in USD" | mistral
```

### Chat Mode

```bash
# Start a new conversation thread
echo "Let's discuss Go programming" | mistral -chat go_dev

# Continue the conversation (History is preserved)
echo "Give me an example of a worker pool" | mistral -chat go_dev

# Reset a chat session
mistral -clear-chat go_dev

# Inline reset command
echo "/clear" | mistral -chat go_dev
```

## Configuration

### File Locations

- **Main Config**: `%APPDATA%\clipgen-m\mistral.conf`
- **Tavily Config**: `%APPDATA%\clipgen-m\tavily.conf`
- **Chat History**: `%APPDATA%\clipgen-m\mistral_chats\`
- **Error Logs**: `%APPDATA%\clipgen-m\mistral_err.log`

### `mistral.conf` Example (JSON)

```json
{
  "api_keys": [
    "primary_mistral_key",
    "backup_mistral_key"
  ],
  "base_url": "https://api.mistral.ai",
  "system_prompt": "You are a professional assistant. Be concise and provide direct answers.",
  "temperature": 0.7,
  "max_tokens": 8000,
  "models": {
    "general": ["mistral-small-latest", "mistral-medium-latest"],
    "code": ["codestral-latest", "mistral-large-latest"],
    "vision": ["pixtral-12b-2409", "mistral-large-latest"],
    "audio": ["voxtral-mini-latest"],
    "ocr": ["mistral-ocr-latest"]
  },
  "chat_history_max_messages": 30,
  "chat_history_max_chars": 50000
}
```

## Key Management

### Adding Keys via CLI

```bash
# Save your Mistral API key
mistral -save-key sk-your-mistral-key-here

# Add a Tavily API key for search capabilities
mistral -add-tavily-key tvly-your-tavily-key-here
```

The application intelligently manages these keys:
- **Mistral**: Auto-switches keys if an authorization or rate-limit error occurs.
- **Tavily**: Uses randomized rotation across all available keys for load balancing.

## Tool Engine Details

### Lua Scripting Engine
- Handles high-precision math and complex algorithmic tasks.
- Accesses standard Lua libraries for data processing.
- Runs in a secure, sandboxed environment.

### Web Search (Tavily)
- Fetches real-time web results with summaries.
- Supports multi-key load balancing.
- Implements content-length capping to prevent context window overflow.

## Troubleshooting

1. **"No input provided"**: Ensure you are piping data via `stdin` or using the `-f` flag.
2. **"No API keys"**: Add your keys using the `-save-key` or `-add-tavily-key` flags.
3. **Lua Issues**: Ensure a modern Go compiler is installed for the Lua executor component.
4. **Ffmpeg Warnings**: For better audio support, ensure `ffmpeg` is added to your system's PATH.

## Development

The project is built with Go. To compile:

```bash
go build -o mistral.exe main.go
```

## License

This project is licensed under the MIT License.

---
**Part of the ClipGen-m Project**
