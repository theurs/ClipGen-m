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
	LogFileName    = "mistral_err.log"
	MaxLogSize     = 10 * 1024 * 1024 // 10 MB

	// Значения по умолчанию для генерации нового конфига
	DefaultBaseURL      = "https://api.mistral.ai"
	DefaultTemperature  = 0.7
	DefaultMaxTokens    = 8000
	DefaultSystemPrompt = "Вы — ИИ-ассистент, интегрированный в инструмент командной строки Windows под названием ClipGen-m. Ваш вывод часто копируется непосредственно в буфер обмена пользователя или вставляется в редакторы кода.\n\nРУКОВОДСТВО:\n1. Будьте лаконичны и прямолинейны.\n2. При генерации кода предоставляйте только блок кода, если не требуется объяснение.\n3. Если ввод — это лог ошибки, кратко объясните причину.\n4. Используйте обычный текст вместо markdown.\n5. Не используйте разговорные фразы типа 'Вот код'."
)

// Списки моделей по умолчанию (используются, если в конфиге пусто)
var DefaultModels = map[string][]string{
	"general": {"mistral-small-latest", "mistral-medium-latest", "mistral-large-latest"},
	"vision":  {"mistral-small-latest", "mistral-medium-latest", "mistral-large-latest"},
	"code":    {"devstral-2512", "codestral-latest", "labs-devstral-small-2512"},
	"audio":   {"voxtral-mini-latest", "voxtral-small-latest"},
	"ocr":     {"mistral-ocr-latest"},
}

// --- Структуры данных API ---

type Config struct {
	ApiKeys      []string            `json:"api_keys"`
	BaseURL      string              `json:"base_url"`
	SystemPrompt string              `json:"system_prompt"`
	Temperature  float64             `json:"temperature"`
	MaxTokens    int                 `json:"max_tokens"`
	Models       map[string][]string `json:"models"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageUrl *struct {
		Url string `json:"url"`
	} `json:"image_url,omitempty"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio,omitempty"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
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

type OCRRequest struct {
	Model    string `json:"model"`
	Document struct {
		Type        string `json:"type"`
		DocumentUrl string `json:"document_url,omitempty"`
	} `json:"document"`
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
	// Дефолт ставим пустым, чтобы понять, задал ли пользователь флаг вручную
	flag.StringVar(&flagSystem, "s", "", "Системный промпт (переопределяет конфиг)")
	flag.BoolVar(&flagJson, "j", false, "Принудительный JSON ответ")
	flag.StringVar(&flagMode, "m", "auto", "Режим: auto, general, code, ocr, audio, vision")
	// Дефолт -1.0, чтобы отличить от 0.0 (валидной температуры)
	flag.Float64Var(&flagTemp, "t", -1.0, "Температура генерации (переопределяет конфиг)")
	flag.BoolVar(&flagVerbose, "v", false, "Вывод логов в stderr")
	flag.StringVar(&flagSaveKey, "save-key", "", "Сохранить ключ и выйти")
}

// --- Main ---

func main() {
	flag.Parse()

	// 1. Работа с конфигом
	configPath, err := getConfigPath()
	if err != nil {
		fatal("Ошибка получения пути конфига: %v", err)
	}

	if flagSaveKey != "" {
		if err := addKeyToConfig(configPath, flagSaveKey); err != nil {
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
		fatal("Нет API ключей. Запустите: mistral.exe -save-key ВАШ_КЛЮЧ")
	}

	// 2. Определение параметров запроса (Конфиг vs Флаги)

	// Температура: Флаг > Конфиг > Дефолт
	finalTemp := config.Temperature
	if flagTemp != -1.0 {
		finalTemp = flagTemp
	}

	// Системный промпт: Флаг > Конфиг > Дефолт
	finalSystem := config.SystemPrompt
	if flagSystem != "" {
		finalSystem = flagSystem
	}

	// URL API
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	// 3. Чтение входных данных
	userPrompt := readStdin()
	filesData, hasImages, hasAudio, hasPdf := processFiles(flagFiles)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет входных данных")
	}

	// 4. Определение режима
	mode := determineMode(flagMode, userPrompt, hasImages, hasAudio, hasPdf)

	if userPrompt == "" {
		switch mode {
		case "audio":
			// Для аудио самое логичное действие по умолчанию — транскрипция
			userPrompt = "Запишите этот аудиофайл дословно. Выведите только текст."
		case "vision":
			// Для картинок — описание
			userPrompt = "Опиши это изображение подробно."
		case "code":
			// Для файлов кода — объяснение
			userPrompt = "Объясни логику и назначение этого кода."
		case "general":
			// Если просто текстовый файл
			userPrompt = "Сократи этот текст."
		}
	}

	// Предупреждение о смешивании форматов
	if hasImages && hasAudio {
		logVerbose("ВНИМАНИЕ: Вы отправляете и изображения, и аудио. Текущие модели Mistral могут не поддерживать оба формата одновременно.")
	}

	// Выбор списка моделей из конфига
	modelsList := selectModelList(mode, config)

	if flagJson {
		userPrompt += "\nIMPORTANT: Output strictly in JSON format."
	}

	// 5. Цикл запросов
	var lastErr error
	usedKeys := make(map[string]bool)

	for _, modelName := range modelsList {
		keyAttempts := 0
		maxKeyAttempts := len(config.ApiKeys)

		for keyAttempts < maxKeyAttempts {
			apiKey := getRandomKey(config.ApiKeys, usedKeys)
			if apiKey == "" {
				break
			}

			logVerbose("Попытка: Модель [%s], Режим [%s], Temp [%.2f]", modelName, mode, finalTemp)

			var result string
			var errReq error

			switch mode {
			case "ocr":
				if len(filesData) > 0 {
					result, errReq = requestOCR(apiKey, baseURL, modelName, filesData[0])
				} else {
					errReq = fmt.Errorf("ocr mode requires a file")
				}
			default:
				result, errReq = requestChat(apiKey, baseURL, modelName, finalSystem, userPrompt, filesData, finalTemp, config.MaxTokens, flagJson)
			}

			if errReq == nil {
				// Успех
				printOutput(result, flagJson)
				return
			}

			// Обработка ошибок
			lastErr = errReq
			logVerbose("Ошибка: %v", errReq)

			errMsg := errReq.Error()

			if strings.Contains(errMsg, "401") {
				// Ошибка авторизации: меняем ключ сразу
				usedKeys[apiKey] = true
				logVerbose("Ключ невалиден, пробуем другой...")
				keyAttempts++
				continue

			} else if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "500") || strings.Contains(errMsg, "503") {
				// Ошибка лимитов или сервера
				logVerbose("Сервер перегружен или лимит. Ждем 2 сек перед повтором...")
				time.Sleep(2 * time.Second)

				if keyAttempts < maxKeyAttempts-1 {
					keyAttempts++
					logVerbose("Пробуем другой ключ...")
					continue
				} else {
					logVerbose("Все ключи исчерпаны для этой модели, пробуем следующую модель...")
					break
				}
			} else {
				// Критическая ошибка (например 400 Bad Request)
				// Если ошибка 400 и связана с моделью (invalid model), имеет смысл попробовать следующую модель
				if strings.Contains(errMsg, "model") || strings.Contains(errMsg, "found") {
					logVerbose("Модель %s недоступна или не существует, переходим к следующей...", modelName)
					break
				}
				fatal("Критическая ошибка API: %v", errReq)
			}
		}
	}

	fatal("Не удалось получить ответ после всех попыток. Последняя ошибка: %v", lastErr)
}

// --- Логика запросов ---

func requestChat(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, maxTokens int, jsonMode bool) (string, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

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
			} else if strings.HasPrefix(f.MimeType, "audio/") {
				format := "mp3"
				if strings.Contains(f.MimeType, "wav") {
					format = "wav"
				}
				parts = append(parts, ContentPart{
					Type: "input_audio",
					InputAudio: &struct {
						Data   string `json:"data"`
						Format string `json:"format"`
					}{
						Data:   f.Base64Content,
						Format: format,
					},
				})
			} else if strings.HasPrefix(f.MimeType, "text/") || f.MimeType == "application/json" {
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
		MaxTokens:   maxTokens,
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

func requestOCR(apiKey, baseURL, model string, file FileData) (string, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/ocr"

	reqBody := OCRRequest{
		Model: model,
	}

	reqBody.Document.Type = "document_url"
	base64Full := fmt.Sprintf("data:%s;base64,%s", file.MimeType, file.Base64Content)
	reqBody.Document.DocumentUrl = base64Full

	jsonData, _ := json.Marshal(reqBody)
	respBytes, err := doHttp(apiKey, url, "application/json", jsonData)
	if err != nil {
		return "", err
	}

	var resp OCRResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", err
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

	client := &http.Client{Timeout: 300 * time.Second}
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
			// Если файла нет, возвращаем дефолтный конфиг
			return &Config{
				ApiKeys:      []string{},
				BaseURL:      DefaultBaseURL,
				SystemPrompt: DefaultSystemPrompt,
				Temperature:  DefaultTemperature,
				MaxTokens:    DefaultMaxTokens,
				Models:       DefaultModels,
			}, nil
		}
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}

	// Валидация и заполнение отсутствующих полей (миграция старых конфигов)
	dirty := false

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
		dirty = true
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
		dirty = true
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultMaxTokens
		dirty = true
	}
	// Для float (Temperature) сложно проверить "пустоту" (0.0 может быть валидным),
	// поэтому оставим как есть, если конфиг уже был. Но если models нет, заполним.

	if cfg.Models == nil {
		cfg.Models = make(map[string][]string)
		dirty = true
	}

	// Проверяем каждую категорию моделей
	for k, v := range DefaultModels {
		if list, exists := cfg.Models[k]; !exists || len(list) == 0 {
			cfg.Models[k] = v
			dirty = true
		}
	}

	// Если мы обновили структуру конфига, сохраним его, чтобы пользователь видел новые поля
	if dirty && len(cfg.ApiKeys) > 0 {
		saveConfig(path, &cfg)
	}

	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(cfg)
}

func addKeyToConfig(path, key string) error {
	cfg, _ := loadConfig(path)
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
	return saveConfig(path, cfg)
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

func selectModelList(mode string, cfg *Config) []string {
	// Ищем список в конфиге
	if list, ok := cfg.Models[mode]; ok && len(list) > 0 {
		return list
	}
	// Если нет в конфиге, ищем в дефолтах
	if list, ok := DefaultModels[mode]; ok {
		return list
	}
	// Фолбэк на general
	if list, ok := cfg.Models["general"]; ok && len(list) > 0 {
		return list
	}
	return DefaultModels["general"]
}

// --- Хелперы ввода/вывода и Логирование ---

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
