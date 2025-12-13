package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// --- Константы и настройки ---

const (
	ConfigDirName  = "clipgen-m"
	ConfigFileName = "mistral.conf"
	LogFileName    = "err.log"
	BaseURL        = "https://api.mistral.ai"
)

// Списки моделей для фоллбека (от лучшей к простой)
var (
	ModelsGeneral = []string{"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest"}
	ModelsCode    = []string{"devstral-2512", "codestral-latest", "labs-devstral-small-2512"}
	ModelsVision  = []string{"pixtral-12b-2409", "mistral-large-latest"}
	ModelAudio    = "voxtral-mini-latest"
	ModelOCR      = "mistral-ocr-latest"
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
	} `json:"error,omitempty"`
}

// OCRRequest - исправленная структура под curl пример
type OCRRequest struct {
	Model    string `json:"model"`
	Document struct {
		Type        string `json:"type"`                   // "document_url"
		DocumentUrl string `json:"document_url,omitempty"` // data:application/pdf;base64,...
	} `json:"document"`
	IncludeImageBase64 bool `json:"include_image_base64,omitempty"`
}

type OCRResponse struct {
	Pages []struct {
		Markdown string `json:"markdown"`
	} `json:"pages"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Флаги ---

type arrayFlags []string

func (i *arrayFlags) String() string { return strings.Join(*i, ",") }
func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

var (
	flagFiles   arrayFlags
	flagSystem  string
	flagJson    bool
	flagMode    string
	flagTemp    float64
	flagVerbose bool
	flagSaveKey string
)

func init() {
	flag.Var(&flagFiles, "f", "Путь к файлу (можно несколько)")
	flag.StringVar(&flagSystem, "s", "You are a helpful assistant.", "Системный промпт")
	flag.BoolVar(&flagJson, "j", false, "Принудительный JSON ответ")
	flag.StringVar(&flagMode, "m", "auto", "Режим: auto, code, ocr, audio")
	flag.Float64Var(&flagTemp, "t", 0.7, "Температура генерации")
	flag.BoolVar(&flagVerbose, "v", false, "Вывод логов в stderr")
	flag.StringVar(&flagSaveKey, "save-key", "", "Сохранить ключ и выйти")
}

// --- Main ---

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	// 1. Работа с конфигом
	configPath, err := getConfigPath()
	if err != nil {
		fatal("Ошибка получения пути конфига: %v", err)
	}

	// Режим сохранения ключа
	if flagSaveKey != "" {
		if err := addKeyToConfig(configPath, flagSaveKey); err != nil {
			fatal("Ошибка сохранения ключа: %v", err)
		}
		fmt.Printf("Ключ сохранен в %s\n", configPath)
		return
	}

	// Загрузка конфига
	config, err := loadConfig(configPath)
	if err != nil {
		fatal("Ошибка загрузки конфига: %v", err)
	}

	if len(config.ApiKeys) == 0 {
		fatal("Нет API ключей. Запустите: mistral.exe -save-key ВАШ_КЛЮЧ")
	}

	// 2. Чтение входных данных (STDIN + Файлы)
	userPrompt := readStdin()
	filesData, hasImages, hasAudio, hasPdf := processFiles(flagFiles)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет входных данных (ни текста в stdin, ни файлов)")
	}

	// 3. Определение режима и моделей
	mode := determineMode(flagMode, userPrompt, hasImages, hasAudio, hasPdf)
	modelsList := selectModelList(mode)

	// Доработка промпта для JSON
	if flagJson {
		userPrompt += "\nIMPORTANT: Output strictly in JSON format."
	}

	// 4. Цикл запросов (Retry / Fallback / Key Rotation)
	var lastErr error
	usedKeys := make(map[string]bool)

	for _, modelName := range modelsList {
		keyAttempts := 0
		maxKeyAttempts := len(config.ApiKeys)

		// Пытаемся перебирать ключи для текущей модели
		for keyAttempts < maxKeyAttempts {
			apiKey := getRandomKey(config.ApiKeys, usedKeys)
			if apiKey == "" {
				break
			}

			logVerbose("Попытка: Модель [%s], Режим [%s]", modelName, mode)

			var result string
			var errReq error

			switch mode {
			case "audio":
				// Для аудио берем первый подходящий файл
				if len(filesData) > 0 {
					result, errReq = requestAudio(apiKey, modelName, filesData[0].Path)
				} else {
					errReq = fmt.Errorf("audio mode requires an audio file")
				}
			case "ocr":
				// Для OCR берем первый файл
				if len(filesData) > 0 {
					result, errReq = requestOCR(apiKey, modelName, filesData[0])
				} else {
					errReq = fmt.Errorf("ocr mode requires a file")
				}
			default:
				// Text, Code, Vision
				result, errReq = requestChat(apiKey, modelName, flagSystem, userPrompt, filesData, flagTemp, flagJson)
			}

			if errReq == nil {
				// УСПЕХ
				printOutput(result, flagJson)
				return
			}

			// Обработка ошибок
			lastErr = errReq
			logVerbose("Ошибка: %v", errReq)

			errMsg := errReq.Error()
			if strings.Contains(errMsg, "401") {
				// Ключ плохой - помечаем и пробуем следующий
				usedKeys[apiKey] = true
				logVerbose("Ключ невалиден, пробуем другой...")
				keyAttempts++
				continue
			} else if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "500") || strings.Contains(errMsg, "503") {
				// Rate Limit или Server Error
				// Сначала пробуем сменить ключ (может лимит на аккаунт)
				if keyAttempts < maxKeyAttempts-1 {
					keyAttempts++
					logVerbose("Rate limit/Server Error, пробуем другой ключ...")
					continue
				} else {
					// Ключи кончились, выходим из цикла ключей -> переходим к следующей модели
					logVerbose("Все ключи исчерпаны для этой модели, пробуем следующую модель...")
					break
				}
			} else {
				// Неисправимая ошибка (например 400 Bad Request)
				fatal("Критическая ошибка API: %v", errReq)
			}
		}
	}

	fatal("Не удалось получить ответ после всех попыток. Последняя ошибка: %v", lastErr)
}

// --- Логика запросов ---

func requestChat(apiKey, model, systemPrompt, userText string, files []FileData, temp float64, jsonMode bool) (string, error) {
	url := BaseURL + "/v1/chat/completions"

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	var content interface{}
	if len(files) == 0 {
		content = userText
	} else {
		parts := []ContentPart{}
		if userText != "" {
			parts = append(parts, ContentPart{Type: "text", Text: userText})
		}
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
			} else if strings.HasPrefix(f.MimeType, "text/") || f.MimeType == "application/json" {
				// Текстовые файлы читаем и вставляем в промпт
				textBytes, _ := base64.StdEncoding.DecodeString(f.Base64Content)
				parts = append(parts, ContentPart{
					Type: "text",
					Text: fmt.Sprintf("\n--- File: %s ---\n%s\n", f.Name, string(textBytes)),
				})
			}
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
	respBytes, err := doHttp(apiKey, url, "application/json", jsonData)
	if err != nil {
		return "", err
	}

	var resp ChatResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, string(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("api error %s: %s", resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}

	return resp.Choices[0].Message.Content, nil
}

func requestAudio(apiKey, model, filePath string) (string, error) {
	url := BaseURL + "/v1/audio/transcriptions"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return "", err
	}

	writer.WriteField("model", model)
	writer.Close()

	respBytes, err := doHttp(apiKey, url, writer.FormDataContentType(), body.Bytes())
	if err != nil {
		return "", err
	}

	var result struct {
		Text  string `json:"text"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("api error: %s", result.Error.Message)
	}

	return result.Text, nil
}

// requestOCR - обновленная функция с правильным JSON payload
func requestOCR(apiKey, model string, file FileData) (string, error) {
	url := BaseURL + "/v1/ocr"

	reqBody := OCRRequest{
		Model: model,
	}

	// Mistral OCR использует тип "document_url" и ожидает поле "document_url"
	reqBody.Document.Type = "document_url"

	// Формируем data URI
	base64Full := fmt.Sprintf("data:%s;base64,%s", file.MimeType, file.Base64Content)
	reqBody.Document.DocumentUrl = base64Full

	// Опционально, чтобы соответствовать примеру (хотя для текста не обязательно)
	// reqBody.IncludeImageBase64 = true

	jsonData, _ := json.Marshal(reqBody)
	respBytes, err := doHttp(apiKey, url, "application/json", jsonData)
	if err != nil {
		return "", err
	}

	var resp OCRResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		// Если json битый, попробуем вернуть тело ошибки
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, string(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("ocr api error: %s", resp.Error.Message)
	}

	var sb strings.Builder
	for _, p := range resp.Pages {
		sb.WriteString(p.Markdown)
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}

// --- Утилиты HTTP ---

func doHttp(apiKey, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	// Увеличенный таймаут для больших файлов (OCR и Vision могут думать долго)
	client := &http.Client{Timeout: 180 * time.Second}
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

// --- Файловая система и Конфиг ---

type FileData struct {
	Name          string
	Path          string
	MimeType      string
	Base64Content string
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
		return nil, err
	}
	return &cfg, nil
}

func addKeyToConfig(path, key string) error {
	cfg, _ := loadConfig(path)
	// Добавляем только если такого ключа еще нет
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

func processFiles(paths []string) ([]FileData, bool, bool, bool) {
	var result []FileData
	hasImg, hasAudio, hasPdf := false, false, false

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
			case ".pdf":
				mimeType = "application/pdf"
			case ".mp3":
				mimeType = "audio/mpeg"
			case ".wav":
				mimeType = "audio/wav"
			case ".m4a":
				mimeType = "audio/mp4"
			case ".txt", ".go", ".js", ".json", ".md", ".py":
				mimeType = "text/plain"
			default:
				mimeType = "application/octet-stream"
			}
		}

		if strings.HasPrefix(mimeType, "image/") {
			hasImg = true
		}
		if strings.HasPrefix(mimeType, "audio/") {
			hasAudio = true
		}
		if mimeType == "application/pdf" {
			hasPdf = true
		}

		result = append(result, FileData{
			Name:          filepath.Base(p),
			Path:          p,
			MimeType:      mimeType,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return result, hasImg, hasAudio, hasPdf
}

// --- Логика выбора режима ---

func determineMode(flagMode, prompt string, hasImg, hasAudio, hasPdf bool) string {
	if flagMode != "auto" {
		return flagMode
	}
	if hasAudio {
		return "audio"
	}
	if hasPdf {
		return "ocr"
	}
	if hasImg {
		return "vision"
	}

	promptLower := strings.ToLower(prompt)
	if strings.Contains(promptLower, "код") || strings.Contains(promptLower, "code") ||
		strings.Contains(promptLower, "function") || strings.Contains(promptLower, "script") ||
		strings.Contains(promptLower, "json") {
		return "code"
	}

	return "general"
}

func selectModelList(mode string) []string {
	switch mode {
	case "code":
		return ModelsCode
	case "audio":
		return []string{ModelAudio}
	case "ocr":
		return []string{ModelOCR}
	case "vision":
		return ModelsVision
	default:
		return ModelsGeneral
	}
}

// --- Хелперы ввода/вывода и логирования ---

func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "" // Данных в пайпе нет
	}

	inputBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}

	// 1. Если это валидный UTF-8, оставляем как есть
	if utf8.Valid(inputBytes) {
		return strings.TrimSpace(string(inputBytes))
	}

	// 2. Если не UTF-8, пробуем CP866 (консоль Windows)
	decoded, err := charmap.CodePage866.NewDecoder().Bytes(inputBytes)
	if err == nil {
		return strings.TrimSpace(string(decoded))
	}

	// 3. Пробуем Windows-1251
	decoded1251, err := charmap.Windows1251.NewDecoder().Bytes(inputBytes)
	if err == nil {
		return strings.TrimSpace(string(decoded1251))
	}

	// Возвращаем как есть, если ничего не подошло
	return strings.TrimSpace(string(inputBytes))
}

func printOutput(text string, jsonMode bool) {
	if jsonMode {
		// Очистка markdown блоков для чистого JSON
		text = strings.TrimSpace(text)
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	fmt.Println(text)
}

func appendLog(level, format string, v ...interface{}) {
	logPath := getLogFilePath()
	if logPath == "" {
		return
	}

	msg := fmt.Sprintf(format, v...)
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, msg)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(logLine)
	}
}

func logVerbose(format string, v ...interface{}) {
	appendLog("INFO", format, v...)
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "[MistralCLI] "+format+"\n", v...)
	}
}

func fatal(format string, v ...interface{}) {
	appendLog("FATAL", format, v...)
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", v...)
	os.Exit(1)
}
