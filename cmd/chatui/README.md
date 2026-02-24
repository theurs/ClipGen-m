# ClipGen-m ChatUI

[Read this in Russian | Читать на русском](README_RU.md)

## Description

ClipGen-m ChatUI is a dedicated graphical front-end designed for seamless interaction with various Large Language Models (LLMs). It provides a user-friendly interface to manage conversations, fine-tune model parameters, and leverage AI capabilities without touching the command line.

## Key Features

- **Multi-Provider Support**: Switch between Mistral, Gemini, GitHub Copilot, and Groq within a single interface.
- **Session Management**: Easily create, save, and organize multiple chat threads and histories.
- **Media Integration**: Attach files, documents, and images directly to your messages for multimodal analysis.
- **Granular Model Control**: Adjust parameters such as temperature, system prompts, and operational modes on a per-chat basis.
- **Provider Flexibility**: Assign a specific LLM provider to each individual chat session to suit different tasks.

## Supported LLM Providers

- **Mistral** (Default)
- **Google Gemini**
- **GitHub Copilot / Chat**
- **Groq**

## Unified Command Interface

To ensure ecosystem consistency, all underlying LLM utilities share a standardized set of command-line flags (supporting both `-` and `--` prefixes):

- `-f` / `--file` — Attach files or images.
- `-s` / `--system-prompt` — Define the model's behavior/instructions.
- `-j` / `--json` — Enable structured JSON output mode.
- `-m` / `--mode` — Set operational mode (e.g., general, code, vision, ocr).
- `-t` / `--temperature` — Adjust model creativity/randomness.
- `-v` / `--verbose` — Enable detailed debug logging.
- `--save-key` — Securely store your API key.
- `-chat` / `--chat-id` — Specify a unique session ID for history persistence.

## Building from Source

The application is built using Go. To compile the executable:

```bash
go build -o ClipGen-m-chatui.exe
```

## Quick Start

1. **Launch**: Open `ClipGen-m-chatui.exe`.
2. **Session**: Select an existing chat from the sidebar or create a new one.
3. **Configure**: Open settings to choose your preferred LLM provider and adjust model parameters.
4. **Interact**: Type your message and hit **Send** (or use **Ctrl+Enter** for quick sending).

## Dependencies

- [walk](https://github.com/lxn/walk) — A Windows GUI library for the Go programming language.

---
**Part of the ClipGen-m Project**
