# Gemini CLI Utility (geminillm)

[Read this in Russian | Читать на русском](README_RU.md)

A robust and flexible command-line interface for interacting with **Google Gemini** and **Gemma 3** models via the Google AI API. Fully integrated into the **ClipGen-m** ecosystem, this utility serves as a drop-in replacement for the Mistral CLI, allowing for a seamless transition between AI providers.

## ✨ Features

*   **Native Multimodality**: Out-of-the-box support for text, images, audio, and **PDFs (with native OCR)**. 
    *   *Note*: Some audio formats may have limited support in the Gemini API. The utility automatically detects unsupported formats (like `.amr`) and converts them to high-compatibility formats via `ffmpeg` before uploading.
*   **Dual-Tool Integration (Gemini)**: Leverages the power of **Google Search** (for real-time information) and **Code Execution** (running Python in a secure sandbox for high-precision mathematical and logical tasks) simultaneously.
*   **Gemma 3 Support**: Includes automated system prompt emulation for Gemma models, which do not natively support system instructions via the standard `generateContent` API.
*   **Mistral-Compatible Chat History**: Uses the same unified storage structure (`mistral_chats`) as the Mistral CLI. This allows you to switch between Mistral and Gemini mid-conversation without losing your chat context.
*   **Smart Key Rotation**: Automatically shuffles your list of API keys on every launch to balance quota usage and avoid rate limits.
*   **Windows-Optimized**: Specifically handles console encodings (CP866 to UTF-8) to ensure smooth piped input/output in CMD and PowerShell.

## 🛠 Build Instructions

Requires [Go 1.23+](https://go.dev/dl/).

```bash
# Navigate to the project directory
cd cmd/geminillm

# Compile the binary
go build -o geminillm.exe main.go
```

## ⚙️ Configuration

Settings are stored in `%AppData%\clipgen-m\gemini.conf`.

**Add an API Key:**
Generate your free API key at [Google AI Studio](https://aistudio.google.com/).
```powershell
geminillm.exe -save-key AIzaSy...YOUR_KEY_HERE
```
You can add multiple keys. The utility will randomly select a key for each request to maximize availability.

## 🚀 Usage Examples

### 1. Real-time Search Query
Gemini models will autonomously decide when to use Google Search to verify facts.
```powershell
echo "Who won the most recent Super Bowl?" | geminillm.exe
```

### 2. PDF Analysis & OCR
Process `.png`, `.jpg`, `.webp`, or `.pdf` files directly.
```powershell
geminillm.exe -f "C:\Docs\invoice.pdf" -s "Extract the total due as a JSON object" -j
```

### 3. Stateful Chat (Shared History)
Continue a conversation started with Mistral or another ClipGen-m module.
```powershell
echo "Hi, my name is Alex" | geminillm.exe -chat session_1
echo "What is my name?" | geminillm.exe -chat session_1
```

### 4. High-Precision Math
Using the `code_execution` tool, Gemini can write and run Python scripts to solve complex math.
```powershell
echo "Calculate the factorial of 50 and multiply it by the square root of 12345" | geminillm.exe
```

## 📚 Command-Line Reference

| Flag | Description | Example |
| :--- | :--- | :--- |
| `-f` | File path (Image, Audio, PDF). Multiple files supported. | `-f "chart.png" -f "report.pdf"` |
| `-s` | System Prompt (Role/Instruction). | `-s "Act as a technical editor"` |
| `-j` | JSON Mode (Gemini models only). | `-j` |
| `-m` | Manual Mode: `auto`, `general`, `code`, `ocr`, `vision`. | `-m ocr` |
| `-t` | Temperature (0.0 - 2.0). | `-t 0.7` |
| `-chat` | Unique Chat ID for session persistence. | `-chat dev_session_01` |
| `-v` | Verbose Mode. Logs process details to stderr and file. | `-v` |
| `-save-key` | Saves the API key to configuration and exits. | `-save-key AIza...` |

## 🧠 Model Routing Logic

### Default Model Hierarchy
1.  **General/Vision**: `gemini-2.0-flash`, `gemini-1.5-flash`, `gemma-3-27b-it`.
2.  **Code**: `gemini-2.0-flash`, `gemma-3-27b-it`.
3.  **OCR (PDF)**: `gemini-2.0-flash` (Optimized for long-document context).

### Gemini vs. Gemma Implementation
*   **Gemini**: Supports native `system_instruction`, JSON-mode, and integrated tools (Search/Code Execution).
*   **Gemma**: The system prompt is automatically prepended to the user message (Prefixing) to maintain role consistency. Tools and JSON-mode are disabled for Gemma to ensure stability.

## 📁 Storage & Logs
*   **Config**: `%AppData%\clipgen-m\gemini.conf`
*   **Error Logs**: `%AppData%\clipgen-m\gemini_err.log`
*   **Chat History**: `%AppData%\clipgen-m\mistral_chats\` (Shared with other modules).

---
**Part of the ClipGen-m Project**
