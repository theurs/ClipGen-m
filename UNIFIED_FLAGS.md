# Unified CLI Flags for LLM Utilities

[Read this in Russian | Читать на русском](UNIFIED_FLAGS_RU.md)

## Overview

All LLM utilities within the ecosystem (`mistral`, `geminillm`, `ghllm`, `groqllm`) have been updated to support a standardized set of command-line flags. This ensures a consistent user experience regardless of which AI provider you are using.

## Supported Flags

All utilities now support the following unified flags. For flexibility, both single-hyphen (`-`) and double-hyphen (`--`) prefixes are accepted for every parameter:

- **`-f` / `--file`** – Specify files for processing. Multiple files can be attached by repeating the flag. Supported formats vary by utility:
    - **Images & Text**: Supported by all utilities.
    - **Audio**: Full support in `mistral`, `ghllm`, and `groqllm`. `geminillm` handles incompatible formats (like `.amr`) via automated `ffmpeg` conversion.
    - **PDF**: Supported via native OCR in `mistral`.
- **`-s` / `--system` / `--system-prompt`** – Set a custom system instruction (overrides the default configuration).
- **`-j` / `--json`** – Force the model to output a valid JSON object.
- **`-m` / `--mode`** – Set the operational mode (`auto`, `general`, `code`, `ocr`, `audio`, `vision`).
- **`-t` / `--temp` / `--temperature`** – Adjust the model's sampling temperature.
- **`-v` / `--verbose`** – Enable detailed execution logs in `stderr`.
- **`--save-key`** – Securely save your API key to the config and exit.
- **`-chat` / `--chat-id`** – Specify a unique session ID to persist and load chat history.

*Note: Individual utilities may still support additional flags specific to their unique features.*

## Usage Examples

```bash
# Single-hyphen syntax
mistral.exe -s "You are a professional editor" -t 0.7 -m general -j

# Double-hyphen syntax
ghllm.exe --system "Act as a software architect" --temperature 0.8 --mode code --json

# Combined syntax
groqllm.exe -s "Analyze the attached data" --temperature 0.5 -m general

# Multimodal example with geminillm
geminillm.exe -f "chart.png" --system "Describe this image" --mode vision
```

## Backward Compatibility

While the new standardized flags are recommended, all utilities maintain backward compatibility with their original flag sets to prevent breaking existing scripts and automation.

## Unified Chat History

Every utility now supports the `-chat` / `--chat-id` flag. This uses a unified history format compatible with the original Mistral implementation, allowing you to switch between different AI providers while maintaining the same conversation thread.

---
**Part of the ClipGen-m Project**
