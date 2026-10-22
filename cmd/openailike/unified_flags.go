package main

import (
	"os"
	"strconv"
	"strings"
)

// --- Константы ---

const (
	ConfigDirName  = "clipgen-m"
	ConfigFileName = "openai.conf"
	LogFileName    = "openai_err.log"
	MaxLogSize     = 5 * 1024 * 1024 // 5 MB

	// DefaultBaseURL — стандартный OpenAI-совместимый endpoint.
	// Для локальных серверов (Ollama, LM Studio, vLLM) укажите свой адрес
	// в openai.conf (base_url) или передайте --base-url.
	DefaultBaseURL = "https://api.openai.com/v1"

	// DefaultSystemPrompt — системный промпт по умолчанию.
	DefaultSystemPrompt = "Ты полезный ИИ-ассистент. Отвечай кратко и по делу."

	// DefaultModel — модель по умолчанию, если сервер не вернул список моделей.
	DefaultModel = "gpt-4o-mini"

	// DefaultAudioModel — модель транскрипции аудио по умолчанию.
	DefaultAudioModel = "whisper-1"
)

// DefaultModels — модели по умолчанию для каждого режима.
// Пустой список означает авто-обнаружение через GET /models.
var DefaultModels = map[string][]string{
	"general": {},
	"code":    {},
	"vision":  {},
	"ocr":     {},
	"audio":   {DefaultAudioModel},
}

// UnifiedFlags структура для хранения унифицированных флагов
type UnifiedFlags struct {
	Files      []string
	System     string
	Json       bool
	Mode       string
	Temp       float64
	Verbose    bool
	SaveKey    string
	ChatID     string
	BaseURL    string
	Model      string
	ListModels bool
}

// parseArgs унифицированный разбор аргументов, поддерживает как -flag и --flag
func parseArgs() *UnifiedFlags {
	flags := &UnifiedFlags{
		Mode: "auto",
		Temp: 0.6,
	}

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
			case "base-url", "url":
				i++
				if i < len(args) {
					flags.BaseURL = args[i]
				}
			case "model", "m2":
				i++
				if i < len(args) {
					flags.Model = args[i]
				}
			case "list-models", "models":
				flags.ListModels = true
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
			case "base-url", "url":
				i++
				if i < len(args) {
					flags.BaseURL = args[i]
				}
			case "model":
				i++
				if i < len(args) {
					flags.Model = args[i]
				}
			case "list-models", "models":
				flags.ListModels = true
			}
		}
	}

	return flags
}

func attemptsResetted(allKeys []string, used map[string]bool) bool {
	return len(used) >= len(allKeys)
}
