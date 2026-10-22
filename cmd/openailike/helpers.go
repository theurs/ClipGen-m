package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Структуры истории (единый формат для всех LLM-утилит) ---

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

// --- Файлы, режимы и модели ---

// processFiles читает файлы и определяет их типы
func processFiles(paths []string) (res []FileData, hasImg, hasAudio, hasPdf bool) {
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
			case ".gif":
				mimeType = "image/gif"
			case ".bmp":
				mimeType = "image/bmp"
			case ".mp3":
				mimeType = "audio/mpeg"
			case ".wav":
				mimeType = "audio/wav"
			case ".m4a":
				mimeType = "audio/mp4"
			case ".ogg":
				mimeType = "audio/ogg"
			case ".pdf":
				mimeType = "application/pdf"
			case ".txt", ".go", ".js", ".json", ".md", ".py", ".csv", ".xml", ".yaml", ".yml", ".log", ".ini", ".toml":
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

		res = append(res, FileData{
			Name:          filepath.Base(p),
			Path:          p,
			MimeType:      mimeType,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return
}

// determineMode определяет режим работы
func determineMode(flagMode, prompt string, hasImg, hasAudio, hasPdf bool) string {
	if flagMode != "auto" {
		return flagMode
	}
	if hasAudio {
		return "audio"
	}
	if hasImg {
		return "vision"
	}
	if hasPdf {
		return "ocr"
	}

	promptLower := strings.ToLower(prompt)
	if strings.Contains(promptLower, "код") ||
		strings.Contains(promptLower, "code") ||
		strings.Contains(promptLower, "функци") ||
		strings.Contains(promptLower, "function") ||
		strings.Contains(promptLower, "баг") ||
		strings.Contains(promptLower, "bug") {
		return "code"
	}

	return "general"
}

// selectModelList возвращает список моделей для режима.
// Если в конфиге модели не заданы — пытаемся получить их с сервера.
func selectModelList(mode string, cfg *Config, baseURL string, apiKeys []string) []string {
	if cfg != nil {
		if models, ok := cfg.Models[mode]; ok && len(models) > 0 {
			return models
		}
	}

	// Авто-обнаружение моделей на сервере (GET /models)
	if models, err := fetchModels(baseURL, firstKey(apiKeys)); err == nil && len(models) > 0 {
		logVerbose("Модели получены с сервера: %v", models)
		if len(models) > 3 {
			models = models[:3]
		}
		return models
	} else if err != nil {
		logVerbose("Не удалось получить список моделей с сервера: %v", err)
	}

	// Фолбэк на модель по умолчанию
	if mode == "audio" {
		return []string{DefaultAudioModel}
	}
	return []string{DefaultModel}
}

// --- История чатов (единый формат) ---

// legacyChatsDirName — прежнее имя каталога истории чатов
// (использовалось, когда основным LLM-провайдером был Mistral).
const legacyChatsDirName = "mistral_chats"

// chatsDir возвращает каталог, в котором хранится история чатов.
// Для совместимости со старыми установками используется прежний каталог,
// пока новый еще не создан (история не теряется).
func chatsDir() string {
	dir, _ := os.UserConfigDir()
	appDir := filepath.Join(dir, ConfigDirName)
	newDir := filepath.Join(appDir, "llm_chats")
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		legacyDir := filepath.Join(appDir, legacyChatsDirName)
		if _, err := os.Stat(legacyDir); err == nil {
			return legacyDir
		}
	}
	return newDir
}

func loadChatHistory(id string) (*ChatHistory, error) {
	path := filepath.Join(chatsDir(), id+".json")
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

	maxMessages := 30
	if cfg != nil && cfg.ChatHistoryMaxMessages > 0 {
		maxMessages = cfg.ChatHistoryMaxMessages
	}
	if len(h.Messages) > maxMessages*2 {
		h.Messages = h.Messages[len(h.Messages)-maxMessages*2:]
	}

	path := filepath.Join(chatsDir(), id+".json")
	os.MkdirAll(filepath.Dir(path), 0755)
	f, _ := os.Create(path)
	json.NewEncoder(f).Encode(h)
}

// historyContentToText приводит сохраненное сообщение к строке
func historyContentToText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", v)
	}
}
