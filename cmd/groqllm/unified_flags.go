package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime"
	"mime/multipart"
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
	ConfigFileName = "groq.conf"
	LogFileName    = "groq_err.log"
	MaxLogSize     = 5 * 1024 * 1024 // 5 MB
	BaseURL        = "https://api.groq.com/openai/v1"
)

// Списки моделей
var (
	ModelsChat = []string{
		"moonshotai/kimi-k2-instruct",
		"openai/gpt-oss-120b",
		"meta-llama/llama-4-maverick-17b-128e-instruct",
		"openai/gpt-oss-20b",
		"llama-3.3-70b-versatile",
	}

	ModelsVision = []string{
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"llama-3.2-90b-vision-preview",
	}

	ModelsSearch = []string{
		"groq/compound-mini",
		"groq/compound",
	}

	ModelsAudio = []string{
		"whisper-large-v3-turbo",
		"whisper-large-v3",
	}
)

// --- Структуры API ---

type Config struct {
	ApiKeys []string `json:"api_keys"`
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
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	Stream         bool          `json:"stream"`
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

// AudioResponse расширен для поддержки сегментов
type AudioSegment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type AudioResponse struct {
	Text     string         `json:"text"`
	Segments []AudioSegment `json:"segments,omitempty"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Глобальные переменные ---

// UnifiedFlags структура для хранения унифицированных флагов
type UnifiedFlags struct {
	Files   []string
	System  string
	Json    bool
	Mode    string
	Temp    float64
	Verbose bool
	SaveKey string
	Srt     bool // Новый флаг для субтитров
	ChatID  string // Добавляем поддержку чата для унификации
}

// parseArgs унифицированный парсер аргументов, поддерживающий как одинарные, так и двойные дефисы
func parseArgs() *UnifiedFlags {
	flags := &UnifiedFlags{
		Mode:   "auto",
		Temp:   0.6,
		System: "Ты полезный помощник. Отвечай на русском языке.",
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
			case "srt":
				flags.Srt = true
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
			case "srt":
				flags.Srt = true
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

type FileData struct {
	Name          string
	Path          string
	MimeType      string
	Base64Content string
}

func mainUnified() {
	flags := parseArgs()
	rand.Seed(time.Now().UnixNano())

	// Устанавливаем глобальную переменную для совместимости с logVerbose
	flagVerbose = flags.Verbose

	configPath, err := getConfigPath()
	if err != nil {
		fatal("Ошибка пути конфига: %v", err)
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
		fatal("Ошибка конфига: %v", err)
	}
	if len(config.ApiKeys) == 0 {
		fatal("Нет API ключей. Используйте: groqllm.exe --save-key ВАШ_КЛЮЧ")
	}

	userPrompt := readStdin()
	filesData, hasImages, hasAudio := processFiles(flags.Files)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет данных для обработки (пустой ввод)")
	}

	mode := determineMode(flags.Mode, userPrompt, hasImages, hasAudio)
	modelsList := selectModelList(mode)

	if flags.Json && mode != "audio" {
		userPrompt += "\nОТВЕТЬ ТОЛЬКО В ФОРМАТЕ JSON."
	}

	// --- Логика перебора ---
	var lastErr error
	bannedKeys := make(map[string]bool)

	for _, modelName := range modelsList {
		usedKeys := make(map[string]bool)
		for k := range bannedKeys {
			usedKeys[k] = true
		}

		keyAttempts := 0
		maxKeyAttempts := len(config.ApiKeys) * 2

		for keyAttempts < maxKeyAttempts {
			apiKey := getRandomKey(config.ApiKeys, usedKeys)

			if apiKey == "" {
				if attemptsResetted(config.ApiKeys, usedKeys) {
					break
				}
				usedKeys = make(map[string]bool)
				for k := range bannedKeys {
					usedKeys[k] = true
				}
				apiKey = getRandomKey(config.ApiKeys, usedKeys)
			}
			if apiKey == "" {
				break
			}

			logVerbose("Модель [%s], Key [%s...], Режим [%s]", modelName, maskKey(apiKey), mode)

			var result string
			var errReq error

			switch mode {
			case "audio":
				if len(filesData) > 0 {
					// Передаем флаг srt в запрос
					result, errReq = requestAudio(apiKey, modelName, filesData[0], flags.Srt)
				} else {
					errReq = fmt.Errorf("режим audio требует файл")
				}
			default:
				result, errReq = requestChat(apiKey, modelName, flags.System, userPrompt, filesData, flags.Temp, flags.Json)
			}

			if errReq == nil {
				printOutput(result, flags.Json)
				return
			}

			lastErr = errReq
			logVerbose("Ошибка (Key %s): %v", maskKey(apiKey), errReq)

			errMsg := strings.ToLower(errReq.Error())

			if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "unauthorized") {
				bannedKeys[apiKey] = true
				usedKeys[apiKey] = true
				keyAttempts++
				logVerbose("Ключ невалиден (401). Баним навсегда.")

			} else if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "rate limit") {
				logVerbose("Rate limit (429). Ждем 1 сек и меняем ключ...")
				time.Sleep(1 * time.Second)
				usedKeys[apiKey] = true
				keyAttempts++

			} else if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "503") || strings.Contains(errMsg, "service unavailable") {
				logVerbose("Модель %s лежит (500/503). Переход к следующей модели...", modelName)
				time.Sleep(1 * time.Second)
				break

			} else {
				logVerbose("Критическая ошибка: %v. Пробуем следующую модель...", errReq)
				break
			}
		}
	}

	fatal("Не удалось получить ответ после перебора всех моделей. Последняя ошибка: %v", lastErr)
}

func attemptsResetted(allKeys []string, used map[string]bool) bool {
	return len(used) >= len(allKeys)
}

// --- Логика запросов ---

func requestAudio(apiKey, model string, file FileData, needSrt bool) (string, error) {
	url := BaseURL + "/audio/transcriptions"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 1. Файл
	fileBytes, err := base64.StdEncoding.DecodeString(file.Base64Content)
	if err != nil {
		return "", fmt.Errorf("base64 decode err: %v", err)
	}

	part, err := writer.CreateFormFile("file", file.Name)
	if err != nil {
		return "", err
	}
	part.Write(fileBytes)

	// 2. Модель
	writer.WriteField("model", model)

	// 3. Язык
	forceRu := shouldForceRussian(file.Path)
	if forceRu {
		logVerbose("Файл короткий или нет ffprobe: принудительно ставим язык RU")
		writer.WriteField("language", "ru")
	}

	// 4. Формат и Таймстампы
	if needSrt {
		writer.WriteField("response_format", "verbose_json")
		writer.WriteField("timestamp_granularities[]", "segment")
	} else {
		writer.WriteField("response_format", "json")
	}

	err = writer.Close()
	if err != nil {
		return "", err
	}

	respBytes, err := doHttp(apiKey, url, writer.FormDataContentType(), body.Bytes())
	if err != nil {
		return "", err
	}

	var resp AudioResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("json parse error: %v", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("api error: %s", resp.Error.Message)
	}

	// Если запросили SRT
	if needSrt {
		if len(resp.Segments) == 0 {
			// Если вдруг verbose_json не вернул сегменты, отдаем просто текст
			return removeDimaTorzok(resp.Text), nil
		}
		return generateSRT(resp.Segments), nil
	}

	// Обычный текст
	return removeDimaTorzok(resp.Text), nil
}

func requestChat(apiKey, model, systemPrompt, userText string, files []FileData, temp float64, jsonMode bool) (string, error) {
	url := BaseURL + "/chat/completions"

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
			} else {
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
		Stream:      false,
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
		code := resp.Error.Code
		msg := resp.Error.Message
		return "", fmt.Errorf("api error [%s]: %s", code, msg)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от API")
	}

	return resp.Choices[0].Message.Content, nil
}

// --- Утилиты HTTP ---

func doHttp(apiKey, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{Timeout: 300 * time.Second} // Увеличенный таймаут
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

// --- Хелперы для Whisper и Audio ---

func removeDimaTorzok(text string) string {
	linesToRemove := []string{
		"Субтитры сделал DimaTorzok.",
		"Субтитры сделал DimaTorzok",
		"Субтитры добавил DimaTorzok.",
		"Субтитры создавал DimaTorzok.",
		"Субтитры создавал DimaTorzok",
		"Субтитры добавил DimaTorzok",
		"Субтитры делал DimaTorzok",
		"DimaTorzok.",
		"DimaTorzok",
		"Продолжение следует...",
		"Подпишись на канал",
	}

	for _, trash := range linesToRemove {
		text = strings.ReplaceAll(text, trash, "")
	}

	text = strings.TrimSpace(text)
	return text
}

// Конвертация секунд в формат SRT: 00:00:05,123
func secondsToSRTTime(seconds float64) string {
	hours := int(seconds / 3600)
	remainder := math.Mod(seconds, 3600)
	minutes := int(remainder / 60)
	secs := math.Mod(remainder, 60)
	intSecs := int(secs)
	millis := int((secs - float64(intSecs)) * 1000)

	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, intSecs, millis)
}

// Генерация SRT контента
func generateSRT(segments []AudioSegment) string {
	var sb strings.Builder
	for i, seg := range segments {
		// Очищаем текст сегмента
		cleanText := removeDimaTorzok(seg.Text)
		if cleanText == "" {
			continue // Пропускаем пустые сегменты после очистки
		}

		// Номер субтитра
		sb.WriteString(fmt.Sprintf("%d\n", i+1))
		// Таймкоды
		sb.WriteString(fmt.Sprintf("%s --> %s\n", secondsToSRTTime(seg.Start), secondsToSRTTime(seg.End)))
		// Текст
		sb.WriteString(cleanText + "\n\n")
	}
	return sb.String()
}

func shouldForceRussian(filePath string) bool {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)

	outputBytes, err := cmd.Output()
	if err != nil {
		logVerbose("ffprobe ошибка: %v", err)
		return true
	}

	outputStr := strings.TrimSpace(string(outputBytes))
	durationSec, err := strconv.ParseFloat(outputStr, 64)
	if err != nil {
		return true
	}

	logVerbose("Длительность аудио: %.2f сек", durationSec)
	return durationSec < 30.0
}

// --- Файловая система и Конфиг ---

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

func maskKey(k string) string {
	if len(k) < 10 {
		return "???"
	}
	return "..." + k[len(k)-4:]
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
		ext := strings.ToLower(filepath.Ext(p))

		if mimeType == "" {
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
			case ".m4a":
				mimeType = "audio/mp4"
			case ".ogg":
				mimeType = "audio/ogg"
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

// --- Роутер ---

func determineMode(flagMode, prompt string, hasImg, hasAudio bool) string {
	if flagMode != "auto" {
		return flagMode
	}
	if hasAudio {
		return "audio"
	}
	if hasImg {
		return "vision"
	}

	promptLower := strings.ToLower(prompt)
	if strings.Contains(promptLower, "гугли") ||
		strings.Contains(promptLower, "найди") ||
		strings.Contains(promptLower, "search") ||
		strings.Contains(promptLower, "поищи") {
		return "search"
	}

	return "chat"
}

func selectModelList(mode string) []string {
	switch mode {
	case "audio":
		return ModelsAudio
	case "vision":
		return ModelsVision
	case "search":
		return ModelsSearch
	default:
		return ModelsChat
	}
}

// --- Хелперы ввода/вывода ---

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

func appendLog(level, format string, v ...interface{}) {
	dir, _ := getAppDataDir()
	logPath := filepath.Join(dir, LogFileName)

	fi, err := os.Stat(logPath)
	if err == nil && fi.Size() > MaxLogSize {
		_ = os.Remove(logPath + ".old")
		_ = os.Rename(logPath, logPath+".old")
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

var flagVerbose bool // Глобальная переменная для совместимости с logVerbose

func logVerbose(format string, v ...interface{}) {
	appendLog("INFO", format, v...)
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "[GroqCLI] "+format+"\n", v...)
	}
}

func fatal(format string, v ...interface{}) {
	appendLog("FATAL", format, v...)
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", v...)
	os.Exit(1)
}