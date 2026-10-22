package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// --- Структуры API (формат OpenAI) ---

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

// ModelsResponse — ответ GET /models
type ModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// AudioResponse — ответ POST /audio/transcriptions
type AudioResponse struct {
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// requestChat отправляет запрос POST {base}/chat/completions
func requestChat(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, jsonMode bool, history *ChatHistory) (string, error) {
	url := baseURL + "/chat/completions"

	messages := []ChatMessage{}
	if systemPrompt != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	// Ранее сохраненные сообщения чата
	if history != nil {
		for _, m := range history.Messages {
			if m.Role != "user" && m.Role != "assistant" {
				continue
			}
			messages = append(messages, ChatMessage{Role: m.Role, Content: historyContentToText(m.Content)})
		}
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
				if isTextual(f.MimeType) {
					parts = append(parts, ContentPart{
						Type: "text",
						Text: fmt.Sprintf("\n--- File: %s ---\n%s\n", f.Name, string(textBytes)),
					})
				} else {
					// Бинарные файлы (PDF и прочие) передаем как data URI
					parts = append(parts, ContentPart{
						Type: "text",
						Text: fmt.Sprintf("\n--- File: %s (%s) ---\ndata:%s;base64,%s\n", f.Name, f.MimeType, f.MimeType, f.Base64Content),
					})
				}
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
	if err != nil && jsonMode && isResponseFormatUnsupported(err) {
		// Не все OpenAI-совместимые бэкенды умеют response_format — повторяем без него
		logVerbose("Бэкенд не поддержал response_format, повторяем без него")
		reqBody.ResponseFormat = nil
		jsonData, _ = json.Marshal(reqBody)
		respBytes, err = doHttp(apiKey, url, "application/json", jsonData)
	}
	if err != nil {
		return "", err
	}

	var resp ChatResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, respBodySafe(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("api error [%s]: %s", resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от API")
	}

	return resp.Choices[0].Message.Content, nil
}
