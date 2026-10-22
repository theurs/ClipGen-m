package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// requestAudio отправляет файл на транскрипцию POST {base}/audio/transcriptions
func requestAudio(apiKey, baseURL, model string, file FileData) (string, error) {
	url := baseURL + "/audio/transcriptions"

	audioBytes, err := base64.StdEncoding.DecodeString(file.Base64Content)
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", model); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", file.Name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audioBytes); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	respBytes, err := doHttp(apiKey, url, writer.FormDataContentType(), body.Bytes())
	if err != nil {
		return "", err
	}

	var resp AudioResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("json parse error: %v | Body: %s", err, respBodySafe(respBytes))
	}
	if resp.Error != nil {
		return "", fmt.Errorf("api error: %s", resp.Error.Message)
	}
	if resp.Text == "" {
		return "", fmt.Errorf("пустой ответ от API")
	}
	return resp.Text, nil
}

// fetchModels запрашивает список доступных моделей GET {base}/models
func fetchModels(baseURL, apiKey string) ([]string, error) {
	url := baseURL + "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("User-Agent", "ClipGen-m/OpenAILike")

	client := &http.Client{Timeout: 30 * time.Second}
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

	var parsed ModelsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("json parse error: %v", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("api error: %s", parsed.Error.Message)
	}

	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

// doHttp выполняет HTTP-запрос с заголовком авторизации (если ключ задан)
func doHttp(apiKey, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "ClipGen-m/OpenAILike")

	client := &http.Client{Timeout: 300 * time.Second} // увеличенный таймаут
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

// respBodySafe обрезает тело ответа, чтобы не засорить лог
func respBodySafe(b []byte) string {
	const limit = 2000
	if len(b) > limit {
		return string(b[:limit]) + "..."
	}
	return string(b)
}
