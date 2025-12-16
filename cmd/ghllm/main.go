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
	ConfigFileName = "github.conf"
	LogFileName    = "github_err.log"
	MaxLogSize     = 10 * 1024 * 1024 // 10 MB

	// Единый эндпоинт для GitHub Models
	BaseURL = "https://models.inference.ai.azure.com/chat/completions"
)

// Списки моделей
var (
	ModelsGeneral = []string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o", "gpt-4o-mini"}

	ModelsCode = []string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o", "gpt-4o-mini"}

	// Вижн (картинки)
	ModelsVision = []string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o", "gpt-4o-mini"}

	// Аудио (Специфичная модель для GitHub)
	ModelAudio = "microsoft/Phi-4-multimodal-instruct"
)

// --- Структуры данных API ---

type Config struct {
	ApiKeys []string `json:"api_keys"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string или []ContentPart
}

// Универсальная структура для частей контента (Text, Image, Audio)
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// Для картинок
	ImageUrl *struct {
		Url string `json:"url"`
	} `json:"image_url,omitempty"`

	// Для аудио (специфика Phi-4 / Azure)
	AudioUrl *struct {
		Url string `json:"url"`
	} `json:"audio_url,omitempty"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"` // Важно для GH (лимиты)
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
		Code    string `json:"code"` // GH иногда возвращает int коды ошибок, но json.Unmarshal в string справится или упадет, лучше interface{} но оставим string пока
		Type    string `json:"type"`
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
	flag.StringVar(&flagSystem, "s", "", "Системный промпт")
	flag.BoolVar(&flagJson, "j", false, "Принудительный JSON ответ")
	flag.StringVar(&flagMode, "m", "auto", "Режим: auto, code, ocr, audio")
	flag.Float64Var(&flagTemp, "t", 1.0, "Температура (делится на 2 внутри)")
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
		fatal("Нет API ключей. Запустите: gh-cli -save-key ВАШ_КЛЮЧ")
	}

	// 2. Чтение входных данных
	userPrompt := readStdin()
	filesData, hasImages, hasAudio := processFiles(flagFiles)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет входных данных")
	}

	// 3. Определение режима
	mode := determineMode(flagMode, userPrompt, hasImages, hasAudio)

	if hasImages && hasAudio {
		logVerbose("ВНИМАНИЕ: Смешивание аудио и картинок. Используем режим AUDIO (Phi-4), картинки могут быть проигнорированы моделью.")
	}

	modelsList := selectModelList(mode)

	// Системный промпт (добавляем дату, как в питоне)
	sysPrompt := flagSystem
	if sysPrompt == "" && mode != "audio" { // Phi-4 обычно не требует system prompt для транскрибации
		sysPrompt = fmt.Sprintf("Current date and time: %s\nYou are a helpful assistant.", time.Now().Format(time.RFC1123))
		if mode == "ocr" {
			sysPrompt += " Transcribe text from the image strictly. Do not describe the image, just output the text."
		}
	}
	if flagJson {
		sysPrompt += " Output strictly in JSON format."
	}

	// Делим температуру на 2 (по ТЗ)
	finalTemp := flagTemp / 2.0

	// 4. Цикл запросов
	var lastErr error
	usedKeys := make(map[string]bool)

	// Перебираем модели, если первая не сработала (хотя обычно модель одна или список из двух)
	for _, modelName := range modelsList {
		keyAttempts := 0
		maxKeyAttempts := len(config.ApiKeys) * 2 // Даем шанс каждому ключу + ретраи

		for keyAttempts < maxKeyAttempts {
			apiKey := getRandomKey(config.ApiKeys, usedKeys)
			if apiKey == "" {
				// Если все ключи помечены как 401, сбрасываем список и пробуем еще раз (вдруг глюк)
				if len(usedKeys) == len(config.ApiKeys) {
					usedKeys = make(map[string]bool)
					apiKey = getRandomKey(config.ApiKeys, usedKeys)
				} else {
					break
				}
			}

			logVerbose("Попытка: Модель [%s], Режим [%s], Ключ [...%s]", modelName, mode, suffix(apiKey))

			var result string
			var errReq error

			// Единый метод запроса (так как в GH все через chat/completions)
			result, errReq = requestChat(apiKey, modelName, sysPrompt, userPrompt, filesData, finalTemp, flagJson, mode)

			if errReq == nil {
				// Успех
				printOutput(result, flagJson)
				return
			}

			// Обработка ошибок
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
				// Не помечаем ключ как плохой, просто пробуем снова (возможно с ним же или другим рандомным)
				keyAttempts++
				continue
			} else if strings.Contains(errMsg, "content management policy") {
				fatal("Запрос заблокирован Content Filter (Azure). Измените запрос.")
			} else {
				// Другие ошибки (сеть и т.д.)
				time.Sleep(1 * time.Second)
				keyAttempts++
			}
		}
	}

	fatal("Не удалось получить ответ после всех попыток. Последняя ошибка: %v", lastErr)
}

// --- Логика запросов ---

func requestChat(apiKey, model, systemPrompt, userText string, files []FileData, temp float64, jsonMode bool, mode string) (string, error) {

	messages := []ChatMessage{}
	if systemPrompt != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	var content interface{}

	// Если нет файлов и режим не аудио
	if len(files) == 0 {
		content = userText
	} else {
		parts := []ContentPart{}

		// Обработка файлов в зависимости от режима
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
				// Специфика Phi-4: audio_url
				// Согласно питону: {"type": "audio_url", "audio_url": {"url": "data:audio/wav;base64,..."}}
				parts = append(parts, ContentPart{
					Type: "audio_url",
					AudioUrl: &struct {
						Url string `json:"url"`
					}{
						Url: fmt.Sprintf("data:%s;base64,%s", f.MimeType, f.Base64Content),
					},
				})
			} else {
				// Текстовый файл
				textBytes, _ := base64.StdEncoding.DecodeString(f.Base64Content)
				parts = append(parts, ContentPart{
					Type: "text",
					Text: fmt.Sprintf("\n--- File: %s ---\n%s\n", f.Name, string(textBytes)),
				})
			}
		}

		// Добавляем текст пользователя
		promptText := userText
		if mode == "audio" && promptText == "" {
			promptText = "Transcribe the audio clip into text."
		} else if mode == "ocr" && promptText == "" {
			promptText = "Describe picture" // Как в python img2txt
		}

		if promptText != "" {
			parts = append(parts, ContentPart{Type: "text", Text: promptText})
		}

		content = parts
	}

	messages = append(messages, ChatMessage{Role: "user", Content: content})

	reqBody := ChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   4000, // Лимит из Python кода (чтобы влезть в 8к контекст)
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
		// Попытка вернуть сырой ответ для отладки
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, string(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("API Error [%s]: %s", resp.Error.Type, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}

	return resp.Choices[0].Message.Content, nil
}

// --- Утилиты HTTP ---

func doHttp(apiKey, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	// GitHub использует стандартный Bearer
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{Timeout: 180 * time.Second} // Увеличенный таймаут для аудио
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

func processFiles(paths []string) ([]FileData, bool, bool) {
	var result []FileData
	hasImg, hasAudio := false, false

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
			case ".mp3":
				mimeType = "audio/mpeg"
			case ".wav":
				mimeType = "audio/wav"
			case ".m4a", ".mp4":
				mimeType = "audio/mp4"
			case ".ogg":
				mimeType = "audio/ogg"
			case ".flac":
				mimeType = "audio/flac" // Phi-4 понимает flac
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

		result = append(result, FileData{
			Name:          filepath.Base(p),
			Path:          p,
			MimeType:      mimeType,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return result, hasImg, hasAudio
}

// --- Логика выбора режима ---

func determineMode(flagMode, prompt string, hasImg, hasAudio bool) string {
	if flagMode != "auto" {
		return flagMode
	}
	if hasAudio {
		return "audio"
	}
	if hasImg {
		// Если есть картинка, по дефолту vision, но если пользователь просил "ocr" флагом, это обработается выше
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
	case "audio":
		return []string{ModelAudio}
	case "ocr":
		return ModelsVision // Используем Vision для OCR
	case "vision":
		return ModelsVision
	default:
		return ModelsGeneral
	}
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
		fmt.Fprintf(os.Stderr, "[GH-CLI] "+format+"\n", v...)
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
