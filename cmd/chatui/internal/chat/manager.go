// file: internal/chat/manager.go
package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChatSession описывает корневую структуру JSON файла
type ChatSession struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// Message структура сообщения
type Message struct {
	Role      string      `json:"role"`
	Content   interface{} `json:"content"`
	Timestamp string      `json:"timestamp"`
}

func GetChatsDir() string {
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "clipgen-m", "mistral_chats")
}

func ListChats() []string {
	dir := GetChatsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	var chats []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			chats = append(chats, name)
		}
	}
	return chats
}

func extractTextFromContent(content interface{}) string {
	if str, ok := content.(string); ok {
		return str
	}
	if list, ok := content.([]interface{}); ok {
		var sb strings.Builder
		for _, item := range list {
			if obj, ok := item.(map[string]interface{}); ok {
				if txt, ok := obj["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		return sb.String()
	}
	return ""
}

func LoadHistory(chatID string) string {
	path := filepath.Join(GetChatsDir(), chatID+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var session ChatSession
	if err := json.Unmarshal(data, &session); err != nil {
		var messages []Message
		if err2 := json.Unmarshal(data, &messages); err2 == nil {
			return formatMessages(messages)
		}
		return fmt.Sprintf("Ошибка чтения формата истории: %v\r\n", err)
	}

	return formatMessages(session.Messages)
}

func formatMessages(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		roleName := "AI"
		if msg.Role == "user" {
			roleName = "Вы"
		} else if msg.Role == "system" {
			roleName = "Система"
		} else if msg.Role == "assistant" {
			roleName = "AI"
		}

		timeStr := ""
		if msg.Timestamp != "" {
			t, err := time.Parse(time.RFC3339Nano, msg.Timestamp)
			if err == nil {
				// ИЗМЕНЕНИЕ ЗДЕСЬ: Полная дата и время
				timeStr = fmt.Sprintf(" [%s]", t.Format("02.01.2006 15:04"))
			}
		}

		cleanContent := extractTextFromContent(msg.Content)
		sb.WriteString(roleName + timeStr + ":\r\n" + cleanContent + "\r\n\r\n")
	}
	return sb.String()
}
