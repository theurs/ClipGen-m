# Groq CLI Utility (groqllm)

[Read this in Russian | Читать на русском](README_RU.md)

An ultra-fast, lightweight console utility designed to interface with the Groq API (LPU Inference Engine) directly via the Windows command line. Built as a core component of the **ClipGen-m** ecosystem, it features full pipe support, multimodal capabilities, and advanced audio processing.

## ✨ Features

*   **Sub-second Latency**: Leverages Groq’s LPU (Language Processing Unit) chips to deliver generation speeds of up to 1000 tokens per second.
*   **Integrated Web Search**: Native support for `groq/compound` models that can query the live web for current events, weather, and real-time facts.
*   **Advanced Audio (Whisper Turbo)**:
    *   **Hallucination Filtering**: Automatically scrubs known Whisper artifacts (e.g., "Subtitles by..." or recurring spam phrases).
    *   **Contextual Language Detection**: For files shorter than 30 seconds, the utility enforces target language consistency to prevent the model from defaulting to English on short clips.
    *   **SRT Generation**: Instantly creates industry-standard subtitle files with accurate timestamps.
*   **Multimodal (Vision)**: Analyze and describe images using the latest Llama 4 (Scout/Maverick) models.
*   **Fault-Tolerant Architecture**:
    *   **Dynamic Key Rotation**: Immediately cycles to the next available API key if a rate limit (429) or authorization error (401) occurs.
    *   **Cascade Model Fallback**: If the primary model is down, the request is automatically routed to the next most powerful alternative (e.g., Kimi -> Llama 3).
*   **Windows-First Design**: Seamlessly handles character encoding transitions (CP866/CP1251 to UTF-8) for a smooth terminal experience.

## 🛠 Setup and Installation

Requires [Go](https://go.dev/dl/). 
*Recommended: Install **ffmpeg/ffprobe** and add it to your system PATH for more accurate audio duration detection.*

1.  **Download Dependencies**:
    ```bash
    go get golang.org/x/text/encoding/charmap
    ```

2.  **Build from Source**:
    Navigate to the `cmd/groqllm` directory:
    ```bash
    go build -o groqllm.exe main.go
    ```

## ⚙️ Configuration

The utility shares a centralized configuration folder with ClipGen-m.

**Add your API Key:**
```powershell
groqllm.exe -save-key gsk_Your_Groq_API_Key
```
Settings are stored in `%AppData%\clipgen-m\groq.conf`. You can add multiple keys to bypass rate limits—the utility will rotate through them automatically.

## 🚀 Usage Examples

### 1. Web Search
The utility triggers the Compound model automatically if it detects intent keywords like "find," "search," or "google."
```powershell
echo "Find the latest news on the Go 1.24 release" | groqllm.exe
```

### 2. Audio Transcription
Extract text from a voice memo or audio file instantly.
```powershell
groqllm.exe -f "C:\Voice\memo.ogg"
```

### 3. Subtitle Generation (SRT)
Generate a subtitle file with timestamps for a video or podcast.
```powershell
groqllm.exe -f "video.mp4" -srt > video.srt
```
*The `-srt` flag enables segmentation and time-formatting modes.*

### 4. Vision (Image Analysis)
Analyze a screenshot or photo and get an AI-driven breakdown.
```powershell
echo "Describe the UI in this screenshot and suggest UX improvements" | groqllm.exe -f "ui_screen.png"
```

### 5. JSON Output (Code/Data)
```powershell
echo "Generate a JSON schema for a retail product" | groqllm.exe -j
```

## 📚 Command-Line Reference

| Flag | Description | Example |
| :--- | :--- | :--- |
| `-f` | File path (Audio, Image, or Text). Can be used multiple times. | `-f "note.txt" -f "pic.jpg"` |
| `-s` | System Prompt. Overrides the default "helpful assistant" prompt. | `-s "You are a translator"` |
| `-j` | JSON Mode. Forces the response into a valid JSON object. | `-j` |
| `-srt`| **Audio Only**. Outputs results in SRT subtitle format with timestamps. | `-f "clip.mp4" -srt` |
| `-m` | Manual Mode: `auto`, `audio`, `vision`, `search`, `chat`. | `-m search` |
| `-t` | Temperature (0.0 - 1.0). Default is `0.6`. (Ignored for audio). | `-t 0.2` |
| `-v` | Verbose. Prints detailed execution logs to stderr. | `-v` |
| `-save-key`| Saves the API key to the config file and exits. | `-save-key gsk_...` |

## 🧠 Under the Hood

### Smart Routing Logic
1.  **Audio Detected?** -> Triggers **Audio** mode (Whisper).
2.  **Image Detected?** -> Triggers **Vision** mode (Llama 4).
3.  **Search Keywords Found?** -> Triggers **Search** mode (Groq Compound).
4.  **Default** -> **Chat** mode.

### Whisper Audio Pipeline
1.  **Duration Check**: Runs `ffprobe`. If the file is under 30 seconds (or if ffprobe is missing), the utility forces `ru` (or your target language) to prevent "short-clip language drift."
2.  **Transcription**: Uses the blazing-fast `whisper-large-v3-turbo` model.
3.  **Post-Processing**: Scrubs known hallucinations and AI watermarks common in Whisper outputs.

### Fallback Chain (High Availability)
If the requested model is unreachable (500/503), the utility cascades through a failover list:
1.  **Chat**: `moonshotai/kimi-k2` -> `gpt-oss-120b` -> `llama-4-maverick` -> `gpt-oss-20b` -> `llama-3.3`.
2.  **Vision**: `llama-4-scout` -> `llama-3.2-90b`.
3.  **Search**: `compound-mini` -> `compound`.

## 📁 Logs and Storage

*   **Config Path**: `%AppData%\clipgen-m\groq.conf`
*   **Error Logs**: `%AppData%\clipgen-m\groq_err.log`

*Security Note: API keys are masked in logs (only the last 4 characters are visible), making it safe to share log files for debugging.*
