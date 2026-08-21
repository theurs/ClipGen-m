package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// --- Константы и настройки ---

const (
	ConfigDirName  = "clipgen-m"
	ConfigFileName = "cerebras.conf"
	LogFileName    = "cerebras_err.log"
	MaxLogSize     = 10 * 1024 * 1024 // 10 MB

	BaseURL = "https://api.cerebras.ai/v1/chat/completions"
)

// Списки моделей
var (
	ModelsChat = []string{
		"gemma-4-31b",
		"gpt-oss-120b",
		"zai-glm-4.7",
	}

	ModelsCode = []string{
		"gemma-4-31b",
		"gpt-oss-120b",
		"zai-glm-4.7",
	}

	// Вижн (картинки) - только Gemma 4 на данный момент является мультимодальной на Cerebras
	ModelsVision = []string{
		"gemma-4-31b",
	}
)

// --- Структуры данных API ---

type Config struct {
	ApiKeys []string `json:"api_keys"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string или []ContentPart
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageUrl *struct {
		Url string `json:"url"`
	} `json:"image_url,omitempty"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// --- Структуры Истории ---

type ChatMessageHistory struct {
	Role      string      `json:"role"`
	Content   interface{} `json:"content"`
	Timestamp time.Time   `json:"timestamp"`
}

type ChatHistory struct {
	ID       string               `json:"id"`
	Messages []ChatMessageHistory `json:"messages"`
}

type FileData struct {
	Name          string
	Path          string
	MimeType      string
	Base64Content string
}

type UnifiedFlags struct {
	Files   []string
	System  string
	Json    bool
	Mode    string
	Temp    float64
	Verbose bool
	SaveKey string
	ChatID  string
}

// parseArgs унифицированный парсер аргументов, поддерживающий как одинарные, так и двойные дефисы
func parseArgs() *UnifiedFlags {
	flags := &UnifiedFlags{
		Mode: "auto",
		Temp: 1.0,
	}

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if strings.HasPrefix(arg, "--") {
			key := strings.TrimPrefix(arg, "--")
			switch key {
			case "f", "file":
				i++
				if i < len(args) {
					flags.Files = append(flags.Files, args[i])
				}
			case "s", "system", "system-prompt":
				i++
				if i < len(args) {
					flags.System = args[i]
				}
			case "j", "json":
				flags.Json = true
			case "m", "mode":
				i++
				if i < len(args) {
					flags.Mode = args[i]
				}
			case "t", "temp", "temperature":
				i++
				if i < len(args) {
					if val, err := strconv.ParseFloat(args[i], 64); err == nil {
						flags.Temp = val
					}
				}
			case "v", "verbose":
				flags.Verbose = true
			case "save-key":
				i++
				if i < len(args) {
					flags.SaveKey = args[i]
				}
			case "chat", "chat-id":
				i++
				if i < len(args) {
					flags.ChatID = args[i]
				}
			}
		} else if strings.HasPrefix(arg, "-") {
			key := strings.TrimPrefix(arg, "-")
			switch key {
			case "f":
				i++
				if i < len(args) {
					flags.Files = append(flags.Files, args[i])
				}
			case "s":
				i++
				if i < len(args) {
					flags.System = args[i]
				}
			case "j":
				flags.Json = true
			case "m":
				i++
				if i < len(args) {
					flags.Mode = args[i]
				}
			case "t":
				i++
				if i < len(args) {
					if val, err := strconv.ParseFloat(args[i], 64); err == nil {
						flags.Temp = val
					}
				}
			case "v":
				flags.Verbose = true
			case "save-key":
				i++
				if i < len(args) {
					flags.SaveKey = args[i]
				}
			case "chat":
				i++
				if i < len(args) {
					flags.ChatID = args[i]
				}
			}
		}
	}

	return flags
}

func mainUnified() {
	flags := parseArgs()
	rand.Seed(time.Now().UnixNano())

	flagVerbose = flags.Verbose

	configPath, err := getConfigPath()
	if err != nil {
		fatal("Ошибка получения пути конфига: %v", err)
	}

	if flags.SaveKey != "" {
		if err := addKeyToConfig(configPath, flags.SaveKey); err != nil {
			fatal("Ошибка сохранения ключа: %v", err)
		}
		fmt.Printf("Ключ сохранен в %s\n", configPath)
		return
	}

	config, err := loadConfig(configPath)
	if err != nil {
		fatal("Ошибка загрузки конфига: %v", err)
	}

	if len(config.ApiKeys) == 0 {
		fatal("Нет API ключей. Запустите: cerebrasllm.exe --save-key ВАШ_КЛЮЧ")
	}

	userPrompt := readStdin()
	filesData, hasImages := processFiles(flags.Files)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет входных данных")
	}

	mode := determineMode(flags.Mode, userPrompt, hasImages)
	modelsList := selectModelList(mode)

	sysPrompt := flags.System
	if sysPrompt == "" {
		sysPrompt = fmt.Sprintf("Current date and time: %s\nYou are a helpful assistant.", time.Now().Format(time.RFC1123))
		if mode == "ocr" {
			sysPrompt += " Transcribe text from the image strictly. Do not describe the image, just output the text."
		}
	}
	if flags.Json {
		sysPrompt += " Output strictly in JSON format."
	}

	finalTemp := flags.Temp

	var lastErr error
	usedKeys := make(map[string]bool)

	for _, modelName := range modelsList {
		keyAttempts := 0
		maxKeyAttempts := len(config.ApiKeys) * 2

		for keyAttempts < maxKeyAttempts {
			apiKey := getRandomKey(config.ApiKeys, usedKeys)
			if apiKey == "" {
				if len(usedKeys) == len(config.ApiKeys) {
					usedKeys = make(map[string]bool)
					apiKey = getRandomKey(config.ApiKeys, usedKeys)
				} else {
					break
				}
			}

			logVerbose("Попытка: Модель [%s], Режим [%s], Ключ [...%s]", modelName, mode, suffix(apiKey))

			var chatHistory *ChatHistory
			if flags.ChatID != "" {
				chatHistory, _ = loadChatHistory(flags.ChatID)
			}

			result, errReq := requestChat(apiKey, modelName, sysPrompt, userPrompt, filesData, finalTemp, flags.Json, chatHistory)

			if errReq == nil {
				if flags.ChatID != "" && chatHistory != nil {
					saveHistory(flags.ChatID, chatHistory, userPrompt, result)
				}
				printOutput(result, flags.Json)
				return
			}

			lastErr = errReq
			logVerbose("Ошибка: %v", errReq)
			errMsg := errReq.Error()

			if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "Bad credentials") {
				usedKeys[apiKey] = true
				logVerbose("Ключ невалиден, пробуем другой...")
				keyAttempts++
				continue
			} else if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "Too Many Requests") {
				logVerbose("Лимит запросов. Ждем 2 сек...")
				time.Sleep(2 * time.Second)
				keyAttempts++
				continue
			} else {
				time.Sleep(1 * time.Second)
				keyAttempts++
			}
		}
	}

	fatal("Не удалось получить ответ после всех попыток. Последняя ошибка: %v", lastErr)
}

func requestChat(apiKey, model, systemPrompt, userText string, files []FileData, temp float64, jsonMode bool, history *ChatHistory) (string, error) {
	messages := []ChatMessage{}
	if systemPrompt != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	if history != nil {
		for _, m := range history.Messages {
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			if role == "model" {
				role = "assistant"
			}

			var contentStr string
			if str, ok := m.Content.(string); ok {
				contentStr = str
			} else if parts, ok := m.Content.([]interface{}); ok {
				var sb strings.Builder
				for _, part := range parts {
					if pMap, ok := part.(map[string]interface{}); ok {
						if pType, ok := pMap["type"].(string); ok && pType == "text" {
							if txt, ok := pMap["text"].(string); ok {
								sb.WriteString(txt)
							}
						}
					}
				}
				contentStr = sb.String()
			}

			if contentStr != "" {
				messages = append(messages, ChatMessage{Role: role, Content: contentStr})
			}
		}
	}

	var content interface{}

	if len(files) == 0 {
		content = userText
	} else {
		parts := []ContentPart{}

		for _, f := range files {
			if strings.HasPrefix(f.MimeType, "image/") {
				parts = append(parts, ContentPart{
					Type: "image_url",
					ImageUrl: &struct {
						Url string `json:"url"`
					}{
						Url: fmt.Sprintf("data:%s;base64,%s", f.MimeType, f.Base64Content),
					},
				})
			} else {
				textBytes, _ := base64.StdEncoding.DecodeString(f.Base64Content)
				parts = append(parts, ContentPart{
					Type: "text",
					Text: fmt.Sprintf("\n--- File: %s ---\n%s\n", f.Name, string(textBytes)),
				})
			}
		}

		if userText != "" {
			parts = append(parts, ContentPart{Type: "text", Text: userText})
		}

		content = parts
	}

	messages = append(messages, ChatMessage{Role: "user", Content: content})

	reqBody := ChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
	}

	if jsonMode {
		reqBody.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}

	jsonData, _ := json.Marshal(reqBody)
	respBytes, err := doHttp(apiKey, BaseURL, "application/json", jsonData)
	if err != nil {
		return "", err
	}

	var resp ChatResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, string(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("API Error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}

	return resp.Choices[0].Message.Content, nil
}

func doHttp(apiKey, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func getAppDataDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(configDir, ConfigDirName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.MkdirAll(path, 0755)
	}
	return path, nil
}

func getConfigPath() (string, error) {
	dir, err := getAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigFileName), nil
}

func getLogFilePath() string {
	dir, err := getAppDataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, LogFileName)
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &Config{}, nil
		}
		return nil, err
	}
	return &cfg, nil
}

func addKeyToConfig(path, key string) error {
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &Config{}
	}

	exists := false
	for _, k := range cfg.ApiKeys {
		if k == key {
			exists = true
			break
		}
	}
	if !exists {
		cfg.ApiKeys = append(cfg.ApiKeys, key)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(cfg)
}

func getRandomKey(keys []string, exclude map[string]bool) string {
	validKeys := []string{}
	for _, k := range keys {
		if !exclude[k] {
			validKeys = append(validKeys, k)
		}
	}
	if len(validKeys) == 0 {
		return ""
	}
	return validKeys[rand.Intn(len(validKeys))]
}

func processFiles(paths []string) ([]FileData, bool) {
	var result []FileData
	hasImg := false

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			logVerbose("Ошибка чтения файла %s: %v", p, err)
			continue
		}

		mimeType := mime.TypeByExtension(filepath.Ext(p))
		if mimeType == "" {
			ext := strings.ToLower(filepath.Ext(p))
			switch ext {
			case ".png":
				mimeType = "image/png"
			case ".jpg", ".jpeg":
				mimeType = "image/jpeg"
			case ".webp":
				mimeType = "image/webp"
			case ".txt", ".go", ".js", ".json", ".md", ".py":
				mimeType = "text/plain"
			default:
				mimeType = "application/octet-stream"
			}
		}

		if strings.HasPrefix(mimeType, "image/") {
			hasImg = true
		}

		result = append(result, FileData{
			Name:          filepath.Base(p),
			Path:          p,
			MimeType:      mimeType,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return result, hasImg
}

func determineMode(flagMode, prompt string, hasImg bool) string {
	if flagMode != "auto" {
		return flagMode
	}
	if hasImg {
		return "vision"
	}

	promptLower := strings.ToLower(prompt)
	if strings.Contains(promptLower, "код") || strings.Contains(promptLower, "code") ||
		strings.Contains(promptLower, "json") || strings.Contains(promptLower, "script") {
		return "code"
	}

	return "general"
}

func selectModelList(mode string) []string {
	switch mode {
	case "code":
		return ModelsCode
	case "vision":
		return ModelsVision
	case "ocr":
		return ModelsVision
	default:
		return ModelsChat
	}
}

func loadChatHistory(id string) (*ChatHistory, error) {
	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, ConfigDirName, "mistral_chats", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return &ChatHistory{ID: id}, nil
	}
	var h ChatHistory
	json.Unmarshal(data, &h)
	return &h, nil
}

func saveHistory(id string, h *ChatHistory, user, assistant string) {
	h.Messages = append(h.Messages, ChatMessageHistory{Role: "user", Content: user, Timestamp: time.Now()})
	h.Messages = append(h.Messages, ChatMessageHistory{Role: "assistant", Content: assistant, Timestamp: time.Now()})

	if len(h.Messages) > 60 {
		h.Messages = h.Messages[len(h.Messages)-60:]
	}

	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, ConfigDirName, "mistral_chats", id+".json")
	os.MkdirAll(filepath.Dir(path), 0755)
	f, _ := os.Create(path)
	defer f.Close()
	json.NewEncoder(f).Encode(h)
}

func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return ""
	}

	inputBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}

	if utf8.Valid(inputBytes) {
		return strings.TrimSpace(string(inputBytes))
	}

	decoded, err := charmap.CodePage866.NewDecoder().Bytes(inputBytes)
	if err == nil {
		return strings.TrimSpace(string(decoded))
	}

	decoded1251, err := charmap.Windows1251.NewDecoder().Bytes(inputBytes)
	if err == nil {
		return strings.TrimSpace(string(decoded1251))
	}

	return strings.TrimSpace(string(inputBytes))
}

func printOutput(text string, jsonMode bool) {
	if jsonMode {
		text = strings.TrimSpace(text)
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	fmt.Println(text)
}

func rotateLog(logPath string) {
	fi, err := os.Stat(logPath)
	if err != nil {
		return
	}

	if fi.Size() > MaxLogSize {
		backupPath := logPath + ".old"
		_ = os.Remove(backupPath)
		_ = os.Rename(logPath, backupPath)
	}
}

func appendLog(level, format string, v ...interface{}) {
	logPath := getLogFilePath()
	if logPath == "" {
		return
	}

	rotateLog(logPath)

	msg := fmt.Sprintf(format, v...)
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, msg)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(logLine)
	}
}

var flagVerbose bool

func logVerbose(format string, v ...interface{}) {
	appendLog("INFO", format, v...)
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "[CerebrasCLI] "+format+"\n", v...)
	}
}

func fatal(format string, v ...interface{}) {
	appendLog("FATAL", format, v...)
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", v...)
	os.Exit(1)
}

func suffix(k string) string {
	if len(k) > 4 {
		return k[len(k)-4:]
	}
	return k
}
