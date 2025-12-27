package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// --- Константы ---

const (
	ConfigDirName  = "clipgen-m"
	ConfigFileName = "gemini.conf"
	LogFileName    = "gemini_err.log"
	MaxLogSize     = 10 * 1024 * 1024 // 10 MB

	DefaultBaseURL      = "https://generativelanguage.googleapis.com/v1beta"
	DefaultSystemPrompt = "Ты — ИИ-ассистент ClipGen-m. Будь лаконичен. Пиши простой текст без маркдауна."
)

// Списки моделей по умолчанию на базе актуальных моделей Google
var DefaultModels = map[string][]string{
	"general": {
		"gemini-3-flash-preview",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-2.5-flash-lite-preview-09-2025",
		"gemini-2.5-flash-preview-09-2025",
		"gemma-3-27b-it",
	},
	"vision": {
		"gemini-3-flash-preview",
		"gemini-2.5-flash",
		"gemini-2.5-flash-preview-09-2025",
		"gemma-3-27b-it",
	},
	"code": {
		"gemini-3-flash-preview",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemma-3-27b-it",
	},
	"ocr": {
		"gemini-3-flash-preview",
		"gemini-2.5-flash-lite-preview-09-2025",
		"gemini-2.5-flash-lite",
	},
}

// --- Структуры Google AI API ---

type Config struct {
	ApiKeys                []string            `json:"api_keys"`
	BaseURL                string              `json:"base_url"` // Добавлено это поле
	SystemPrompt           string              `json:"system_prompt"`
	Temperature            float64             `json:"temperature"`
	Models                 map[string][]string `json:"models"`
	ChatHistoryMaxMessages int                 `json:"chat_history_max_messages"`
}

type GeminiRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"system_instruction,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generation_config,omitempty"`
	Tools             []interface{}     `json:"tools,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inline_data,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64
}

type GenerationConfig struct {
	Temperature      float64 `json:"temperature,omitempty"`
	ResponseMimeType string  `json:"response_mime_type,omitempty"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// --- Структуры Истории (совместимы с Mistral) ---

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
	Name, MimeType, Base64Content string
}

// UnifiedFlags структура для хранения унифицированных флагов
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
		Temp: -1.0,
	}

	// Парсим аргументы вручную для поддержки и -flag и --flag
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		
		// Обработка аргументов с двойным дефисом
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
			// Обработка аргументов с одинарным дефисом
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

// mainUnified экспортная функция для использования в основном файле
func mainUnified() {
	flags := parseArgs()
	
	// Устанавливаем глобальную переменную для совместимости с logVerbose
	flagVerbose = flags.Verbose

	configPath, err := getConfigPath()
	if err != nil {
		fatal("Не удалось определить путь к конфигурации: %v", err)
	}

	if flags.SaveKey != "" {
		if err := addKeyToConfig(configPath, flags.SaveKey); err != nil {
			fatal("Ошибка сохранения ключа: %v", err)
		}
		return
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fatal("Ошибка загрузки конфигурации: %v", err)
	}

	if len(cfg.ApiKeys) == 0 {
		fatal("Список API ключей пуст в gemini.conf. Используйте --save-key для добавления.")
	}

	userPrompt := readStdin()
	filesData, hasImages, _, hasPdf := processFiles(flags.Files)
	if userPrompt == "" && len(filesData) == 0 {
		fatal("Отсутствуют входные данные (stdin или файлы).")
	}

	mode := determineMode(flags.Mode, userPrompt, hasImages, hasPdf)
	modelsList := selectModelList(mode, cfg)

	finalSystem := cfg.SystemPrompt
	if flags.System != "" {
		finalSystem = flags.System
	}
	if finalSystem == "" {
		finalSystem = DefaultSystemPrompt
	}

	finalTemp := cfg.Temperature
	if flags.Temp != -1.0 {
		finalTemp = flags.Temp
	}

	var lastErr error

	// Перемешивание ключей для равномерного распределения нагрузки
	shuffledKeys := make([]string, len(cfg.ApiKeys))
	copy(shuffledKeys, cfg.ApiKeys)
	rand.Shuffle(len(shuffledKeys), func(i, j int) {
		shuffledKeys[i], shuffledKeys[j] = shuffledKeys[j], shuffledKeys[i]
	})

	// Счетчик неудачных ключей подряд
	failedKeysCount := 0

	// Согласно правилу: Сначала перебираем модели для текущего ключа,
	// и только если все модели на этом ключе выдали 429, переходим к следующему ключу.
	for _, apiKey := range shuffledKeys {
		// Маскируем ключ для лога
		keySuffix := ""
		if len(apiKey) > 4 {
			keySuffix = apiKey[len(apiKey)-4:]
		}

		// Счетчик неудачных попыток для текущего ключа
		failedModelsForCurrentKey := 0

		for _, modelName := range modelsList {
			logVerbose("Запрос: ключ=...%s, модель=%s, режим=%s", keySuffix, modelName, mode)

			var chatHistory *ChatHistory
			if flags.ChatID != "" {
				chatHistory, _ = loadChatHistory(flags.ChatID)
			}

			result, errReq := requestGemini(apiKey, cfg.BaseURL, modelName, finalSystem, userPrompt, filesData, finalTemp, flags.Json, chatHistory)

			if errReq == nil {
				// Успех
				if flags.ChatID != "" && chatHistory != nil {
					saveHistory(flags.ChatID, chatHistory, userPrompt, result, cfg)
				}
				printOutput(result, flags.Json)
				return
			}

			// Обработка ошибок
			lastErr = errReq
			errMsg := errReq.Error()
			logVerbose("Ошибка (модель %s): %v", modelName, errMsg)

			// Если ошибка 401/403, ключ невалиден вообще.
			// Нет смысла пробовать другие модели на этом ключе — переходим к следующему ключу.
			if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "403") {
				logVerbose("Ключ ...%s невалиден. Переход к следующему ключу.", keySuffix)
				break
			}

			// Увеличиваем счетчик неудачных попыток для текущего ключа
			failedModelsForCurrentKey++

			// Если ошибка 429 (лимит) или любая другая (500, 400),
			// пробуем следующую модель в списке (понижаем версию) для этого же ключа.
			if strings.Contains(errMsg, "429") {
				logVerbose("Лимит модели %s достигнут на ключе ...%s. Пробуем следующую модель.", modelName, keySuffix)
				continue
			}

			// Для прочих ошибок тоже пробуем сменить модель
			continue
		}

		// Если все модели для текущего ключа провалились, увеличиваем счетчик неудачных ключей
		if failedModelsForCurrentKey == len(modelsList) {
			failedKeysCount++

			// Если 2 ключа подряд полностью провалились, прекращаем попытки
			if failedKeysCount >= 2 {
				logVerbose("2 ключа подряд полностью провалились. Прекращаем попытки.")
				break
			}
		} else {
			// Сбрасываем счетчик, если хотя бы один ключ был успешным
			failedKeysCount = 0
		}
	}

	fatal("Не удалось получить ответ от доступных моделей и ключей. Последняя ошибка: %v", lastErr)
}

// --- API Логика ---

func requestGemini(apiKey, baseURL, model, system, prompt string, files []FileData, temp float64, isJson bool, history *ChatHistory) (string, error) {
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", baseURL, model, apiKey)
	modelL := strings.ToLower(model)
	isGemma := strings.Contains(modelL, "gemma")

	req := GeminiRequest{
		GenerationConfig: &GenerationConfig{
			Temperature: temp,
		},
	}

	// 1. Системный промпт и инструменты
	if !isGemma {
		req.SystemInstruction = &Content{Parts: []Part{{Text: system}}}

		// Новые модели требуют "google_search"
		searchToolName := "google_search_retrieval"
		if strings.Contains(modelL, "gemini-2.5") || strings.Contains(modelL, "gemini-2.0") || strings.Contains(modelL, "gemini-3") {
			searchToolName = "google_search"
		}

		// Проверяем, есть ли аудио или видео файлы во входных данных
		hasMedia := false
		for _, file := range files {
			if strings.HasPrefix(file.MimeType, "audio/") || strings.HasPrefix(file.MimeType, "video/") {
				hasMedia = true
				break
			}
		}

		// Для аудио и видео файлов отключаем code_execution, т.к. он не поддерживает многие форматы
		if hasMedia {
			req.Tools = []interface{}{
				map[string]interface{}{searchToolName: map[string]interface{}{}},
				// code_execution инструмент отключен для аудио/видео файлов
			}
		} else {
			req.Tools = []interface{}{
				map[string]interface{}{searchToolName: map[string]interface{}{}},
				map[string]interface{}{"code_execution": map[string]interface{}{}},
			}
		}
	} else {
		prompt = fmt.Sprintf("SYSTEM INSTRUCTION: %s\n\nUSER REQUEST: %s", system, prompt)
	}

	if isJson && !isGemma {
		req.GenerationConfig.ResponseMimeType = "application/json"
	}

	// 2. Сборка контента
	if history != nil {
		for _, m := range history.Messages {
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			var parts []Part
			if str, ok := m.Content.(string); ok {
				parts = append(parts, Part{Text: str})
			}
			req.Contents = append(req.Contents, Content{Role: role, Parts: parts})
		}
	}

	curPart := Part{Text: prompt}
	curContent := Content{Role: "user", Parts: []Part{curPart}}
	for _, f := range files {
		curContent.Parts = append(curContent.Parts, Part{
			InlineData: &InlineData{MimeType: f.MimeType, Data: f.Base64Content},
		})
	}
	req.Contents = append(req.Contents, curContent)

	// 3. Запрос
	body, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}

		// Парсим JSON для получения краткого статуса
		if err := json.Unmarshal(respData, &apiErr); err == nil && apiErr.Error.Status != "" {
			status := apiErr.Error.Status
			msg := apiErr.Error.Message

			// Сокращаем только 429 ошибки (лимиты), остальные показываем полностью
			if status == "RESOURCE_EXHAUSTED" {
				return "", fmt.Errorf("HTTP 429 [RESOURCE_EXHAUSTED]: Limit exceeded")
			}
			// Для других ошибок, включая 400, показываем полное сообщение
			return "", fmt.Errorf("HTTP %d [%s]: %s", resp.StatusCode, status, msg)
		}

		// Фолбэк для неизвестных ошибок
		return "", fmt.Errorf("HTTP %d: error occurred", resp.StatusCode)
	}

	var gResp GeminiResponse
	if err := json.Unmarshal(respData, &gResp); err != nil {
		return "", fmt.Errorf("json parse error: %v", err)
	}

	if gResp.Error != nil {
		return "", fmt.Errorf("API: %s", gResp.Error.Message)
	}
	if len(gResp.Candidates) == 0 {
		return "", fmt.Errorf("empty response")
	}

	var finalResponse strings.Builder
	for _, p := range gResp.Candidates[0].Content.Parts {
		if p.Text != "" {
			finalResponse.WriteString(p.Text)
		}
	}

	return finalResponse.String(), nil
}

// --- Утилиты (Копия логики Mistral для совместимости) ---

func loadConfig(path string) (*Config, error) {
	// Флаг необходимости перезаписи файла (если мы что-то исправили или заполнили)
	dirty := false

	data, err := os.ReadFile(path)
	var cfg Config

	if err != nil {
		// Если файла нет вообще — создаем структуру с дефолтами
		cfg = Config{
			BaseURL:                DefaultBaseURL,
			SystemPrompt:           DefaultSystemPrompt,
			Temperature:            0.7,
			Models:                 DefaultModels,
			ChatHistoryMaxMessages: 30,
			ApiKeys:                []string{""}, // Добавляем пустой ключ для удобства
		}
		dirty = true
	} else {
		// Если файл есть — парсим его
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("ошибка парсинга конфигурации: %v", err)
		}

		// Проверяем наличие базовых полей
		if cfg.BaseURL == "" {
			cfg.BaseURL = DefaultBaseURL
			dirty = true
		}
		if cfg.SystemPrompt == "" {
			cfg.SystemPrompt = DefaultSystemPrompt
			dirty = true
		}
		if cfg.ChatHistoryMaxMessages == 0 {
			cfg.ChatHistoryMaxMessages = 30
			dirty = true
		}

		// Проверяем и восстанавливаем список моделей
		if cfg.Models == nil {
			cfg.Models = make(map[string][]string)
			dirty = true
		}
		for category, defaults := range DefaultModels {
			if list, exists := cfg.Models[category]; !exists || len(list) == 0 {
				cfg.Models[category] = defaults
				dirty = true
			}
		}

		// Если список ключей пуст — добавляем пустой шаблон
		if len(cfg.ApiKeys) == 0 {
			cfg.ApiKeys = []string{""}
			dirty = true
		}
	}

	// Если мы вносили исправления или создали конфиг с нуля — сохраняем изменения на диск
	if dirty {
		logVerbose("Конфигурация обновлена или восстановлена. Сохранение в %s", path)
		if err := saveConfig(path, &cfg); err != nil {
			return nil, fmt.Errorf("не удалось сохранить исправленную конфигурацию: %v", err)
		}
	}

	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	// Создаем или перезаписываем файл конфигурации
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Настраиваем энкодер с отступами для удобного ручного редактирования
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")

	// Записываем структуру в формате JSON
	return enc.Encode(cfg)
}

func addKeyToConfig(path, key string) error {
	// Загружаем существующий конфиг или получаем дефолтный
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}

	// Проверяем, нет ли уже такого ключа в списке, чтобы не дублировать
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

	// Открываем файл на запись (создаем или перезаписываем)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Настраиваем энкодер с отступами для читаемости JSON
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")

	// Сохраняем обновленную структуру в файл
	if err := enc.Encode(cfg); err != nil {
		return err
	}

	fmt.Printf("Ключ успешно добавлен в %s\n", path)
	return nil
}

func processFiles(paths []string) (res []FileData, hasImg, hasAudio, hasPdf bool) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		// First try to detect MIME type by content (more reliable)
		mt := mime.TypeByExtension(filepath.Ext(p))
		contentType := http.DetectContentType(data)

		// If mime.TypeByExtension doesn't return an audio type but content detection does,
		// use the content detection result
		if !strings.HasPrefix(mt, "audio/") && strings.HasPrefix(contentType, "audio/") {
			mt = contentType
		} else if mt == "" || mt == "application/octet-stream" {
			// Fallback to content detection if extension-based detection fails
			mt = contentType
		}

		// Additional check: if we have an OGG file that was detected as application/ogg,
		// treat it as audio since OGG files are typically audio/video containers
		if mt == "application/ogg" || mt == "application/opus" {
			mt = "audio/ogg"  // Normalize to audio type
		}

		// Check if this is an unsupported audio format that needs transcoding
		ext := strings.ToLower(filepath.Ext(p))
		needsTranscoding := (ext == ".amr") // Add other formats as needed

		if needsTranscoding {
			// Transcode using ffmpeg to a supported format (WAV)
			transcodedData, err := transcodeAudioWithFFmpeg(p)
			if err != nil {
				logVerbose("Warning: Could not transcode %s: %v. Attempting to use original file.", p, err)
				// Fall back to original file if transcoding fails
			} else {
				// Use transcoded data instead of original
				data = transcodedData
				mt = "audio/wav" // Set to the format we're transcoding to
			}
		}

		// Check if this is a video file
		if strings.HasPrefix(mt, "video/") {
			// For video files, normalize to a standard format
			switch ext {
			case ".mp4", ".m4v":
				mt = "video/mp4"
			case ".mov", ".qt":
				mt = "video/quicktime"
			case ".avi":
				mt = "video/x-msvideo"
			case ".wmv":
				mt = "video/x-ms-wmv"
			case ".webm":
				mt = "video/webm"
			case ".mkv":
				mt = "video/x-matroska"
			case ".m4a": // Sometimes m4a files can be handled as video
				mt = "video/mp4"
			default:
				// For other video formats, use mp4 as a standard format
				mt = "video/mp4"
			}
		}

		if strings.HasPrefix(mt, "image/") {
			hasImg = true
		}
		if strings.HasPrefix(mt, "audio/") {
			hasAudio = true
			// Normalize audio MIME types for better Gemini API compatibility
			// Use appropriate audio format based on file extension when possible
			ext := strings.ToLower(filepath.Ext(p))
			switch ext {
			case ".mp3":
				mt = "audio/mpeg"
			case ".wav":
				mt = "audio/wav"
			case ".m4a", ".mp4":
				mt = "audio/mp4"
			case ".ogg", ".opus":
				mt = "audio/ogg"
			default:
				// For other audio formats, use wav as a standard format that Gemini supports
				mt = "audio/wav"
			}
		}
		// Check for video files after audio processing
		if strings.HasPrefix(mt, "video/") {
			hasAudio = true // Treat video as audio for the purpose of disabling code execution
			// Video files also need special handling - disable code execution tool
		}
		if mt == "application/pdf" {
			hasPdf = true
		}
		res = append(res, FileData{
			Name: filepath.Base(p), MimeType: mt,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return
}

// transcodeAudioWithFFmpeg transcodes an audio file to WAV format using ffmpeg
func transcodeAudioWithFFmpeg(inputPath string) ([]byte, error) {
	// Create a temporary file for the output
	tempOutput, err := os.CreateTemp("", "transcoded_*.wav")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %v", err)
	}
	tempPath := tempOutput.Name()
	tempOutput.Close()
	defer os.Remove(tempPath) // Clean up temp file

	// Run ffmpeg command: ffmpeg -i input.ext -ar 16000 -ac 1 -b:a 64k output.wav
	cmd := exec.Command("ffmpeg", "-i", inputPath, "-ar", "16000", "-ac", "1", "-b:a", "64k", "-y", tempPath)

	// Capture stderr for error reporting
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v, stderr: %s", err, stderr.String())
	}

	// Read the transcoded file
	transcodedData, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read transcoded file: %v", err)
	}

	return transcodedData, nil
}

func determineMode(flag, prompt string, hasImg, hasPdf bool) string {
	if flag != "auto" {
		return flag
	}
	if hasPdf {
		return "ocr"
	}
	if hasImg {
		return "vision"
	}
	if strings.Contains(strings.ToLower(prompt), "code") {
		return "code"
	}
	return "general"
}

func selectModelList(mode string, cfg *Config) []string {
	if l, ok := cfg.Models[mode]; ok {
		return l
	}
	return DefaultModels["general"]
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

func saveHistory(id string, h *ChatHistory, user, assistant string, cfg *Config) {
	h.Messages = append(h.Messages, ChatMessageHistory{Role: "user", Content: user, Timestamp: time.Now()})
	h.Messages = append(h.Messages, ChatMessageHistory{Role: "assistant", Content: assistant, Timestamp: time.Now()})

	if len(h.Messages) > cfg.ChatHistoryMaxMessages*2 {
		h.Messages = h.Messages[len(h.Messages)-cfg.ChatHistoryMaxMessages*2:]
	}

	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, ConfigDirName, "mistral_chats", id+".json")
	os.MkdirAll(filepath.Dir(path), 0755)
	f, _ := os.Create(path)
	json.NewEncoder(f).Encode(h)
}

func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return ""
	}
	b, _ := io.ReadAll(os.Stdin)
	if utf8.Valid(b) {
		return strings.TrimSpace(string(b))
	}
	dec, _ := charmap.CodePage866.NewDecoder().Bytes(b)
	return strings.TrimSpace(string(dec))
}

func printOutput(text string, isJson bool) {
	if isJson {
		text = strings.TrimSpace(text)
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimSuffix(text, "```")
	}
	fmt.Println(strings.TrimSpace(text))
}

var flagVerbose bool // Глобальная переменная для совместимости с logVerbose

func logVerbose(f string, v ...interface{}) {
	msg := fmt.Sprintf(f, v...)
	if flagVerbose {
		fmt.Fprintln(os.Stderr, "[GeminiLLM]", msg)
	}
	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, ConfigDirName, LogFileName)
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if file != nil {
		defer file.Close()
		fmt.Fprintf(file, "[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}
}

func fatal(f string, v ...interface{}) {
	logVerbose("FATAL: "+f, v...)
	fmt.Fprintf(os.Stderr, "ERROR: "+f+"\n", v...)
	os.Exit(1)
}

func getConfigPath() (string, error) {
	dir, _ := os.UserConfigDir()
	p := filepath.Join(dir, ConfigDirName)
	os.MkdirAll(p, 0755)
	return filepath.Join(p, ConfigFileName), nil
}