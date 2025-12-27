package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type GeminiRequest struct {
	Contents         []Content         `json:"contents"`
	GenerationConfig *GenerationConfig `json:"generation_config,omitempty"`
}

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inline_data,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type GenerationConfig struct {
	Temperature float64 `json:"temperature,omitempty"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: video_diag.exe <video_file_path>")
		fmt.Println("Example: video_diag.exe \"C:\\Users\\user\\Downloads\\1.mp4\"")
		return
	}

	videoPath := os.Args[1]

	// Load API key from environment or .env file
	apiKey := loadAPIKey()
	if apiKey == "" {
		fmt.Println("Error: Could not find API key. Please set GEMINI_TEST_KEY in .env file")
		return
	}

	model := "gemini-2.5-flash"

	// Read the video file
	data, err := os.ReadFile(videoPath)
	if err != nil {
		fmt.Printf("Error reading video file: %v\n", err)
		return
	}

	// Detect MIME type
	extMimeType := mime.TypeByExtension(filepath.Ext(videoPath))
	contentMimeType := http.DetectContentType(data)
	
	fmt.Printf("Video file: %s\n", videoPath)
	fmt.Printf("File size: %d bytes (%.2f MB)\n", len(data), float64(len(data))/1024/1024)
	fmt.Printf("Extension-based MIME type: %s\n", extMimeType)
	fmt.Printf("Content-based MIME type: %s\n", contentMimeType)
	
	// Check if file is too large for Gemini API (limit is typically 10MB)
	if len(data) > 10*1024*1024 {
		fmt.Printf("WARNING: File size exceeds 10MB limit for Gemini API\n")
	}
	
	// Use the detected MIME type or fallback to a video type
	mimeType := extMimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = contentMimeType
	}
	
	// If still not a video type, try to determine based on extension
	if !isVideoMimeType(mimeType) {
		switch filepath.Ext(videoPath) {
		case ".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".webm", ".m4v":
			mimeType = "video/mp4" // Use a standard video type
		}
	}
	
	fmt.Printf("Using MIME type for API: %s\n", mimeType)
	
	// Encode as base64
	encoded := base64.StdEncoding.EncodeToString(data)
	fmt.Printf("Base64 length: %d\n", len(encoded))
	
	// Create request
	req := GeminiRequest{
		Contents: []Content{
			{
				Role: "user",
				Parts: []Part{
					{Text: "Опиши, что происходит в этом видео"},
					{
						InlineData: &InlineData{
							MimeType: mimeType,
							Data:     encoded,
						},
					},
				},
			},
		},
		GenerationConfig: &GenerationConfig{
			Temperature: 0.7,
		},
	}

	// Make API call
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	
	jsonData, err := json.Marshal(req)
	if err != nil {
		fmt.Printf("Error marshaling JSON: %v\n", err)
		return
	}

	client := &http.Client{}
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	fmt.Println("\nMaking API request...")
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Printf("Error making request: %v\n", err)
		return
	}
	
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	fmt.Printf("Status Code: %d\n", resp.StatusCode)
	
	var apiResp GeminiResponse
	if err := json.Unmarshal(respBody, &apiResp); err == nil {
		if apiResp.Error != nil {
			fmt.Printf("API Error: %s - %s\n", apiResp.Error.Code, apiResp.Error.Message)
		} else if len(apiResp.Candidates) > 0 && len(apiResp.Candidates[0].Content.Parts) > 0 {
			fmt.Printf("Success: %s\n", apiResp.Candidates[0].Content.Parts[0].Text)
		} else {
			fmt.Printf("No content in response\n")
		}
	} else {
		fmt.Printf("Raw response: %s\n", string(respBody))
	}
}

func isVideoMimeType(mimeType string) bool {
	return mimeType != "" && (mimeType == "video/mp4" || mimeType == "video/mpeg" ||
		mimeType == "video/quicktime" || mimeType == "video/x-msvideo" ||
		mimeType == "video/x-matroska" || mimeType == "video/webm" ||
		mimeType == "video/avi" || mimeType == "video/x-ms-wmv")
}

// loadAPIKey attempts to load the API key from environment variables or .env file
func loadAPIKey() string {
	// First, try to get from environment variable
	if key := os.Getenv("GEMINI_TEST_KEY"); key != "" {
		return key
	}

	// Then, try to read from .env file
	envPath := ".env"
	if _, err := os.Stat(envPath); err == nil {
		file, err := os.Open(envPath)
		if err == nil {
			defer file.Close()

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "#") || line == "" {
					continue // Skip comments and empty lines
				}

				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					keyName := strings.TrimSpace(parts[0])
					keyValue := strings.TrimSpace(parts[1])

					// Remove quotes if present
					keyValue = strings.Trim(keyValue, "\"'")

					if keyName == "GEMINI_TEST_KEY" {
						return keyValue
					}
				}
			}
		}
	}

	return "" // Return empty string if not found
}