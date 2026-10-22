# ClipGen-M — AI-Powered Clipboard Manager

[Read this in Russian | Читать на русском](README_RU.md)

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows-blue)]()

## Overview

ClipGen-M is a powerful suite of utilities designed to interface with various Large Language Models (LLMs), including Google Gemini, Groq, and any OpenAI-compatible backend. This project provides a unified command-line interface and a system-tray background service to bring AI capabilities directly to your Windows workflow.

## Key Features

- **Unified Interface**: All utilities share a consistent set of command-line flags for a seamless experience.
- **Multimedia Support**: Works out of the box with images, audio, PDFs, and plain text.
- **Stateful Chats**: Built-in support for chat history and context retention.
- **Automated Audio Conversion**: Seamlessly handles rare formats (like AMR) via ffmpeg integration.
- **System Tray Integration**: Runs quietly in the background with a Windows system tray icon for quick access.
- **AI-Enhanced Clipboard**: Automatically processes clipboard data using your preferred LLM.
- **Customizable Hotkeys**: Boost productivity with global hotkeys tailored to your workflow.
- **Direct Text Processing**: Process selected text on-the-fly without the need to manually copy it first.

## Project Structure

- `cmd/clipgen-m` – The main background application (System Tray).
- `cmd/geminillm` – CLI utility for Google Gemini.
- `cmd/groqllm` – CLI utility for Groq.
- `cmd/openailike` – CLI utility for any OpenAI-compatible backend (OpenAI, Ollama, LM Studio, vLLM, OpenRouter).
- `cmd/lua-executor` – Helper utility for precise calculations (Lua).

## Main Module: ClipGen-m (Clipboard Manager)

The core `clipgen-m` module is a Windows background service that lives in your system tray. It transforms your clipboard into an intelligent tool, allowing you to pass data to various LLMs instantly.

### Core Capabilities:

- **Clipboard Processing**: Automatically handle text, images, and files directly from your clipboard.
- **Rich Data Support**: Handles raw text, image files, generic file attachments, and file paths.
- **Selection Processing**: Process text currently selected in any application without overriding your clipboard.
- **Clipboard OCR**: Extract text from images currently stored in the clipboard.
- **Layout Switcher**: A "Punto Switcher" style feature to quickly fix text typed in the wrong keyboard layout (e.g., Russian vs. English).
- **Multi-LLM Integration**: Switch between Gemini, Groq, and any OpenAI-compatible backend on the fly.

### Supported Formats:

- **Text**: Any clipboard content or active text selection.
- **Images**: PNG, JPG, JPEG, BMP, WEBP, and more.
- **Files**: Directly processes files copied to the clipboard.
- **File Paths**: Automatically resolves and processes files from paths copied as text.

## Unified Command-Line Flags

All LLM utilities support a standardized set of command-line flags (using either `-` or `--` prefixes):

- `-f` / `--file` – Attach files (Images, Audio, PDF, or Text).
- `-s` / `--system` / `--system-prompt` – Set the system instruction.
- `-j` / `--json` – Enable JSON output mode.
- `-m` / `--mode` – Model operation mode (`auto`, `general`, `code`, `ocr`, `audio`, `vision`).
- `-t` / `--temp` / `--temperature` – Adjust model creativity/randomness.
- `-v` / `--verbose` – Enable detailed logging output.
- `--save-key` – Securely save your API key.
- `-chat` / `--chat-id` – Specify a unique chat session ID.

## Hotkeys and Actions

ClipGen-m uses customizable global hotkeys to trigger AI actions. The following defaults are included:

### Standard Actions:

- **Fix Layout (Punto)**: `Pause` – Toggle text between RU and EN layouts.
- **Fix Text**: `Ctrl+F1` – Grammar, punctuation, and style correction.
- **Process Request**: `Ctrl+F2` – Run a custom AI command on the selected text.
- **Translate & View**: `Ctrl+F3` – Translate text and open the result in an editor.
- **Translate & Replace**: `Ctrl+F4` – Translate text and replace the selection in-place.
- **OCR (Image to Text)**: `Ctrl+F6` – Extract text from a copied image.
- **Smart Analysis**: `Ctrl+F8` – Interactive mode for complex commands over text.

### App Management:

- **Toggle Service**: `Ctrl+F12` – Enable or disable global hotkey processing.

### Customization:

Hotkeys are defined in `config.yaml` under the `actions` section. Each action supports:
- `name`: Identifier for the menu.
- `hotkey`: Key combination.
- `prompt`: The system prompt sent to the LLM.
- `input_type`: Data type (`auto`, `text`, `image`, `files`, `layout_switch`).
- `output_mode`: Handling of the result (`replace` the text or open in `editor`).

## Interface and System Tray

The application runs as a lightweight process in the Windows System Tray, ensuring AI tools are always just a click away.

### UI Elements:

- **Tray Icon**: Indicates the current application state.
- **Context Menu**: Right-click for quick access to settings and logs.
- **Status Indicators**:
  - **Green**: Active and listening for hotkeys.
  - **Yellow**: Processing an AI request.
  - **Red**: Currently disabled.

### Tray Menu Functions:

- **Status Toggle**: "Active" checkbox to pause/resume global hotkeys.
- **Configuration Access**: Quick links to edit all YAML and config files.
- **Log Viewer**: Instant access to error logs for debugging.
- **App Control**: Restart or Exit the application.

## Configuration

Settings are stored in a YAML file located at `%APPDATA%\clipgen-m\config.yaml`. This file is automatically generated on the first run.

### Sample `config.yaml` Structure:

```yaml
# --- SYSTEM SETTINGS ---
editor_path: "notepad.exe"                    # Editor for output
llm_path: "geminillm.exe"                   # Default LLM utility (any CLI from cmd/)
app_toggle_hotkey: "Ctrl+F12"                # Toggle app on/off
system_prompt: |                             # Default instructions
  You are ClipGen-m, a Windows AI utility. 
  Use tabs for table columns (Excel-friendly). 
  Provide direct answers without conversational filler.

actions:                                      
  - name: "Fix Layout (Punto)"
    hotkey: "Pause"
    input_type: "layout_switch"
    output_mode: "replace"
  - name: "Fix Grammar (F1)"
    hotkey: "Ctrl+F1"
    prompt: "Fix grammar and style. Return ONLY the corrected text: {{.clipboard}}"
    input_type: "text"
    output_mode: "replace"
```

## Installation and Building

Each utility includes a `build.bat` file for easy compilation:

To build a specific module:
```cmd
cd cmd\[utility_name]
call build.bat
```

## Usage Examples

Utilities accept input via `stdin` and support various data types depending on the specific model's API.

- `geminillm.exe`: Supports images, audio, text, and PDF (via OCR).
- `openailike.exe`: Works with any OpenAI-compatible backend; the API address is set in `openai.conf`.

**CLI Examples:**
```cmd
echo "Summarize this" | geminillm.exe --system "Professional assistant" --temperature 0.3
echo "Translate to French" | groqllm.exe --mode general
echo "Hello" | openailike.exe --base-url http://localhost:11434/v1
```

## File Locations

- **Configs & Logs**: `%APPDATA%\clipgen-m\`

## Requirements

- Windows 10/11
- Go 1.25+
- (Optional) FFmpeg for advanced audio format support.

## Contributing

Please refer to [CONTRIBUTING.md](CONTRIBUTING.md) for details on how to get involved.

## License

This project is licensed under the [MIT License](LICENSE).

---
**Authors**: See [AUTHORS](AUTHORS) for the full list of contributors.
