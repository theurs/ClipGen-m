# GitHub Models CLI Utility (ghllm)

[Read this in Russian | Читать на русском](README_RU.md)

An intelligent console utility for interacting with **GitHub Models** (Azure AI) via the Windows command line.

As a core component of the **ClipGen-m** ecosystem, this utility serves as a high-performance alternative to the Mistral client, providing free access to flagship models like **GPT-4o**, **o1**, and **GPT-4.1**. It is specifically engineered to handle the strict rate limits of the GitHub Free Tier through automated key rotation and failover logic.

## ✨ Features

*   **Multimodal Intelligence**: Full support for text and Vision processing. Analyze screenshots, diagrams, and photos of documents seamlessly.
*   **Dynamic Model Routing**: The utility automatically selects the best model for the task:
    *   `gpt-4o-mini`: Optimized for lightning-fast responses and lightweight tasks.
    *   `gpt-4o`: The default for Vision tasks and complex contextual analysis.
    *   `gpt-4.1` (Preview) / `o1`: Reserved for tasks requiring deep reasoning, complex logic, or advanced coding.
*   **Automatic Key Rotation (Failover)**:
    *   The GitHub Free Tier imposes strict API rate limits. This utility mitigates this by automatically cycling through a pool of tokens.
    *   If a token hits a `429 Too Many Requests` error, the program instantly switches to the next available key in your list to ensure uninterrupted service.
*   **Windows-Optimized**: Native handling of character encodings (CP866/1251 to UTF-8) for perfect Cyrillic support in the Windows terminal.
*   **JSON Mode**: Forces structured output, making it ideal for automation scripts and integration.
*   **Unified Ecosystem**: Shares the same configuration directory as the rest of the ClipGen-m suite while maintaining its own dedicated settings file.

## 🛠 Setup and Installation

Requires [Go](https://go.dev/dl/) for building from source.

1.  **Download Dependencies**:
    ```bash
    go get golang.org/x/text/encoding/charmap
    ```

2.  **Build the Binary**:
    Navigate to the source directory and run:
    ```bash
    go build -ldflags="-s -w" -o ghllm.exe main.go
    ```

The `ghllm.exe` binary is now ready for use.

## ⚙️ Configuration

You will need **GitHub Personal Access Tokens** (PAT) to use this utility.
1. Generate them for free at [GitHub Marketplace Models](https://github.com/marketplace/models).
2. *Pro Tip*: Create multiple tokens (even from different accounts) to significantly increase your daily request ceiling.

**Add a Key:**
```powershell
ghllm.exe -save-key ghp_YourGitHubTokenHere...
```
This command initializes the configuration file at `%AppData%\clipgen-m\github.conf`. Run this command for every token you want to add. The more keys you add, the more resilient your setup becomes against rate limits.

## 🚀 Usage Examples

### 1. Simple Text Query
Standard input can be piped directly into the utility. Uses `gpt-4o-mini` by default.
```powershell
echo "Summarize the theory of relativity in three sentences" | ghllm.exe
```

### 2. Complex Coding (Smart Mode)
For architecture design or complex debugging, use the `code` (or `smart`) mode to trigger higher-tier models.
```powershell
echo "Write a Go microservice using Clean Architecture" | ghllm.exe -m code
```

### 3. Vision / Image Analysis
Pass a file path to trigger the `gpt-4o` Vision model.
```cmd
echo "What trends can you see in this chart?" | ghllm.exe -f "C:\Data\monthly_report.png"
```

### 4. Text Extraction (OCR)
While not a dedicated OCR engine, GPT-4o excels at reading text from images.
```powershell
ghllm.exe -m ocr -f "C:\Docs\scanned_invoice.jpg"
```
*In `-m ocr` mode, the utility injects a system instruction to strictly transcribe the text.*

### 5. Multi-File Analysis
You can pass multiple files (code or text) to provide broader context for reviews.
```powershell
echo "Find potential security vulnerabilities in these files" | ghllm.exe -f "server.go" -f "auth.go"
```

## 📚 Command-Line Reference

| Flag | Description | Example |
| :--- | :--- | :--- |
| `-f` | Path to file. Supports text and images (`.png`, `.jpg`, `.webp`). | `-f "log.txt"` |
| `-s` | System Prompt (Role instruction). | `-s "Act as a Senior Dev"` |
| `-j` | JSON Mode. Guarantees a valid JSON object response. | `-j` |
| `-m` | Mode: `auto` (default), `code`/`smart` (heavy models), `vision`, `ocr`. | `-m code` |
| `-t` | Temperature (0.0 - 2.0). Adjusted internally for Azure compatibility. | `-t 1.0` |
| `-v` | Verbose. Outputs detailed debug logs and API status to stderr. | `-v` |
| `-save-key`| Saves the provided GitHub PAT to the config file and exits. | `-save-key ghp_...` |

## 🧠 Under the Hood

### Smart Routing
1.  **Image Detected** → Routes to `gpt-4o` (Vision capability).
2.  **Code/Smart Mode Requested** → Routes to `gpt-4o`, `gpt-4.1`, or `o1` (High intelligence).
3.  **General Text** → Routes to `gpt-4o-mini` (Low latency).

### Failover & Key Rotation Logic
This is the "secret sauce" for high availability on the GitHub Free Tier:
1.  **401 Unauthorized**: The key is flagged as invalid for the session, and the next key is selected.
2.  **429 Too Many Requests**: 
    *   This is common on the free tier. 
    *   The utility **instantly** retries the request using the next key in your pool.
    *   This effectively aggregates the rate limits of all your tokens into a single "super-limit."
3.  **Azure Content Filter**: If a request is blocked by safety filters, the process stops, as retrying with another key will yield the same result.

## 📁 Logs and Storage

All data is stored in `%AppData%\clipgen-m\`:

*   **github.conf**: JSON file containing your pool of GitHub PATs.
*   **github_err.log**: Detailed log of API errors and successful retries.

*Note: While Phi-4 (Audio) support is present in the codebase, it is currently disabled as the public GitHub Models API does not yet accept binary audio payloads via this specific endpoint.*
