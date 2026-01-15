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
	ConfigFileName = "pollinations.conf"
	LogFileName    = "pollinations_err.log"
	MaxLogSize     = 10 * 1024 * 1024 // 10 MB

	DefaultBaseURL      = "https://gen.pollinations.ai/v1"
	DefaultSystemPrompt = "Вы — ИИ-ассистент, интегрированный в инструмент ClipGen-m. Ваш вывод часто копируется в буфер обмена. Будьте лаконичны. Если это лог ошибки — объясните причину. Не используйте вводные фразы типа 'Вот ваш текст'. Пиши простой текст без маркдауна."
	PrimaryModel        = "gemini"
)

var DefaultModels = map[string][]string{
	"general": {PrimaryModel},
	"vision":  {PrimaryModel},
	"code":    {PrimaryModel},
	"ocr":     {PrimaryModel},
	"audio":   {PrimaryModel},
}

// Глобальная переменная для управления подробным выводом в stderr
var flagVerbose bool

// --- Структуры данных ---

type Config struct {
	ApiKeys                []string            `json:"api_keys"`
	BaseURL                string              `json:"base_url"`
	SystemPrompt           string              `json:"system_prompt"`
	Temperature            float64             `json:"temperature"`
	MaxTokens              int                 `json:"max_tokens"`
	Models                 map[string][]string `json:"models"`
	ChatHistoryMaxMessages int                 `json:"chat_history_max_messages"`
	ChatHistoryMaxChars    int                 `json:"chat_history_max_chars"`
	ImageCharCost          int                 `json:"image_char_cost"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	Tools          []Tool        `json:"tools,omitempty"`
	ToolChoice     string        `json:"tool_choice,omitempty"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type ChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
}

type ImageUrl struct {
	Url string `json:"url"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageUrl *ImageUrl `json:"image_url,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type ChatHistory struct {
	ID       string               `json:"id"`
	Messages []ChatMessageHistory `json:"messages"`
}

type ChatMessageHistory struct {
	Role      string      `json:"role"`
	Content   interface{} `json:"content"`
	Size      int         `json:"size"`
	Timestamp time.Time   `json:"timestamp"`
}

type FileData struct {
	Name, MimeType, Base64Content string
}

type UnifiedFlags struct {
	Files        []string
	System       string
	Json         bool
	Mode         string
	Temp         float64
	Verbose      bool
	SaveKey      string
	AddTavilyKey string
	ChatID       string
	ClearChat    string
	NoTools      bool
}

// --- Инструменты (Client-side Tools) ---

func createTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "calculator",
				Description: "Выполняет точные математические расчеты через Lua скрипты",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"expression": map[string]interface{}{"type": "string", "description": "Математическое выражение"},
					},
					"required": []string{"expression"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "tavily_search",
				Description: "Поиск актуальной информации в интернете в режиме реального времени (новости, текущие события, факты, погода)",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{"type": "string", "description": "Поисковый запрос"},
					},
					"required": []string{"query"},
				},
			},
		},
	}
}

func executeCalculator(expression string) string {
	logVerbose("Tool: Calculator -> %s", expression)
	cmd := exec.Command("lua-executor.exe", expression)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return strings.TrimSpace(out.String())
}

// Новая функция для нативного поиска через модель gemini-search на Pollinations
func executePollinationsSearch(apiKey, query string) (string, error) {
	logVerbose("Tool: Pollinations Search (gemini-search) -> %s", query)

	url := strings.TrimSuffix(DefaultBaseURL, "/") + "/chat/completions"

	reqBody := map[string]interface{}{
		"model": "gemini-search",
		"messages": []map[string]string{
			{"role": "user", "content": query},
		},
		"temperature": 0.5,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	hReq, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	hReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		hReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(hReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("pollinations search failed with status %d", resp.StatusCode)
	}

	// Исправлено: чтение данных и обработка ошибки перед десериализацией JSON
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read search response: %v", err)
	}

	var cResp ChatResponse
	if err := json.Unmarshal(respData, &cResp); err != nil {
		return "", err
	}

	if len(cResp.Choices) == 0 || cResp.Choices[0].Message.Content == nil {
		return "", fmt.Errorf("empty search response")
	}

	return fmt.Sprintf("%v", cResp.Choices[0].Message.Content), nil
}

func executeTavilySearch(query string) string {
	logVerbose("Tool: Tavily -> %s", query)
	configDir, _ := os.UserConfigDir()
	data, err := os.ReadFile(filepath.Join(configDir, ConfigDirName, "tavily.conf"))
	if err != nil {
		return "Error: tavily.conf not found"
	}
	var tCfg struct {
		ApiKeys []string `json:"api_keys"`
	}
	if err := json.Unmarshal(data, &tCfg); err != nil || len(tCfg.ApiKeys) == 0 {
		return "Error: invalid tavily.conf"
	}

	apiKey := tCfg.ApiKeys[rand.Intn(len(tCfg.ApiKeys))]
	payload := map[string]interface{}{"api_key": apiKey, "query": query, "search_depth": "basic", "max_results": 3}
	body, _ := json.Marshal(payload)
	resp, err := http.Post("https://api.tavily.com/search", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Sprintf("Search error: %v", err)
	}
	defer resp.Body.Close()

	var resData struct {
		Answer  string                                 `json:"answer"`
		Results []struct{ Title, Url, Content string } `json:"results"`
	}
	json.NewDecoder(resp.Body).Decode(&resData)

	var sb strings.Builder
	if resData.Answer != "" {
		sb.WriteString("Summary: " + resData.Answer + "\n")
	}
	for i, r := range resData.Results {
		sb.WriteString(fmt.Sprintf("%d. [%s](%s): %s\n", i+1, r.Title, r.Url, r.Content))
	}
	if sb.Len() == 0 {
		return "No results found."
	}
	return sb.String()
}

// --- Логика и Утилиты ---

func logVerbose(f string, v ...interface{}) {
	msg := fmt.Sprintf(f, v...)
	if flagVerbose {
		fmt.Fprintln(os.Stderr, "[PollinationsLLM]", msg)
	}
	configDir, _ := os.UserConfigDir()
	path := filepath.Join(configDir, ConfigDirName, LogFileName)
	if info, err := os.Stat(path); err == nil && info.Size() > MaxLogSize {
		_ = os.Rename(path, path+".old")
	}
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

func printOutput(text string, isJson bool) {
	text = strings.TrimSpace(text)
	if isJson && strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 2 {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	fmt.Println(strings.TrimSpace(text))
}

// --- Управление Историей ---

func calculateMessageSize(content interface{}, imageCharCost int) int {
	size := 0
	switch v := content.(type) {
	case string:
		size = len(v)
	case []ContentPart:
		for _, p := range v {
			if p.Type == "text" {
				size += len(p.Text)
			}
			if p.Type == "image_url" {
				size += imageCharCost
			}
		}
	}
	return size
}

func getChatFilePath(id string) string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, ConfigDirName, "mistral_chats", id+".json")
}

func loadChatHistory(id string) (*ChatHistory, error) {
	path := getChatFilePath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return &ChatHistory{ID: id}, nil
	}
	var h ChatHistory
	_ = json.Unmarshal(data, &h)
	return &h, nil
}

func applyHistoryLimits(h *ChatHistory, maxMsg, maxChars int) {
	if len(h.Messages) > maxMsg*2 {
		h.Messages = h.Messages[len(h.Messages)-maxMsg*2:]
	}
	for {
		total := 0
		for _, m := range h.Messages {
			total += m.Size
		}
		if total <= maxChars || len(h.Messages) == 0 {
			break
		}
		h.Messages = h.Messages[1:]
	}
}

func updateAndSaveHistory(id string, h *ChatHistory, userCont interface{}, assistant string, cfg *Config) {
	if strings.TrimSpace(assistant) == "" {
		return
	}
	h.Messages = append(h.Messages, ChatMessageHistory{
		Role: "user", Content: userCont, Timestamp: time.Now(),
		Size: calculateMessageSize(userCont, cfg.ImageCharCost),
	})
	h.Messages = append(h.Messages, ChatMessageHistory{
		Role: "assistant", Content: assistant, Timestamp: time.Now(), Size: len(assistant),
	})
	applyHistoryLimits(h, cfg.ChatHistoryMaxMessages, cfg.ChatHistoryMaxChars)
	path := getChatFilePath(id)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, _ := os.Create(path)
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(h)
}

// --- Сетевой запрос с циклом Tool Calling ---

func requestPollinations(apiKey, baseURL, model, system string, userCont interface{}, temp float64, maxTokens int, isJson bool, history *ChatHistory, noTools bool) (string, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/chat/completions"

	// Инициализация списка сообщений с системным промптом
	var messages []ChatMessage
	messages = append(messages, ChatMessage{Role: "system", Content: system})

	// Добавление истории сообщений с проверкой на валидность контента ассистента
	if history != nil {
		for _, m := range history.Messages {
			if m.Role == "assistant" {
				if str, ok := m.Content.(string); ok && strings.TrimSpace(str) == "" {
					continue
				}
			}
			messages = append(messages, ChatMessage{Role: m.Role, Content: m.Content})
		}
	}
	messages = append(messages, ChatMessage{Role: "user", Content: userCont})

	var tools []Tool
	if !noTools {
		tools = createTools()
	}

	// Цикл Tool Calling (макс 5 итераций), аналогично логике mistral.exe
	for iter := 0; iter < 5; iter++ {
		req := ChatRequest{
			Model:       model,
			Messages:    messages,
			Temperature: temp,
			MaxTokens:   maxTokens,
		}

		// теги анонимной структуры должны точно совпадать с определением в ChatRequest
		if isJson {
			req.ResponseFormat = &struct {
				Type string `json:"type"`
			}{Type: "json_object"}
		}

		if !noTools {
			req.Tools = tools
			req.ToolChoice = "auto"
		}

		body, err := json.Marshal(req)
		if err != nil {
			return "", err
		}

		hReq, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
		if err != nil {
			return "", err
		}
		hReq.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			hReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: 300 * time.Second}
		resp, err := client.Do(hReq)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		// Чтение тела ответа с обработкой ошибки (исправление TooManyValues)
		rData, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response: %v", err)
		}

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rData))
		}

		var cResp ChatResponse
		if err := json.Unmarshal(rData, &cResp); err != nil {
			return "", err
		}
		if len(cResp.Choices) == 0 {
			return "", fmt.Errorf("empty response choices from API")
		}

		msg := cResp.Choices[0].Message

		// Если вызовов инструментов нет — возвращаем очищенный текст
		if len(msg.ToolCalls) == 0 {
			if msg.Content == nil {
				return "", fmt.Errorf("API returned nil content")
			}
			// Удаление мусора транскрибации Whisper
			return removeDimaTorzok(fmt.Sprintf("%v", msg.Content)), nil
		}

		// Добавляем сообщение ассистента с запросами инструментов в контекст
		messages = append(messages, msg)

		// Обработка вызовов инструментов через switch (рекомендация линтера QF1003)
		for _, tc := range msg.ToolCalls {
			var toolResult string
			switch tc.Function.Name {
			case "calculator":
				var args struct {
					Expression string `json:"expression"`
				}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				toolResult = executeCalculator(args.Expression)

			case "tavily_search":
				var args struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				// Двухэтапный поиск: Pollinations Search (gemini-search) -> Tavily Fallback
				res, err := executePollinationsSearch(apiKey, args.Query)
				if err == nil && res != "" {
					toolResult = res
				} else {
					logVerbose("Pollinations search failed, falling back to Tavily: %v", err)
					toolResult = executeTavilySearch(args.Query)
				}
			}

			// Добавляем результат работы инструмента в историю сообщений текущего запроса
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    toolResult,
			})
		}
		// Переход к следующей итерации для получения финального ответа модели по результатам инструментов
	}

	return "", fmt.Errorf("exceeded maximum tool calling iterations (5)")
}

// --- Main ---

// Функция полностью переписана для исправления ошибок компиляции и добавления логики Tavily/Chat
func main() {
	flags := parseArgs()
	flagVerbose = flags.Verbose

	// Добавлена обработка ключа Tavily, как в mistral.exe
	if flags.AddTavilyKey != "" {
		err := addTavilyKey(flags.AddTavilyKey)
		if err != nil {
			fatal("Ошибка добавления Tavily ключа: %v", err)
		}
		fmt.Printf("Tavily ключ добавлен в %s\n", getTavilyConfigPath())
		return
	}

	configPath, err := getConfigPath()
	if err != nil {
		fatal("Path error: %v", err)
	}

	if flags.SaveKey != "" {
		_ = addKeyToConfig(configPath, flags.SaveKey)
		fmt.Println("Ключ сохранен.")
		return
	}
	if flags.ClearChat != "" {
		_ = os.Remove(getChatFilePath(flags.ClearChat))
		fmt.Printf("История чата %s очищена.\n", flags.ClearChat)
		return
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fatal("Config error: %v", err)
	}

	userPrompt := readStdin()
	// Проверка на команду очистки внутри чата
	if flags.ChatID != "" && strings.TrimSpace(userPrompt) == "/clear" {
		_ = os.Remove(getChatFilePath(flags.ChatID))
		fmt.Println("История очищена.")
		return
	}

	files, hasImg, hasAudio, hasPdf := processFiles(flags.Files)
	if userPrompt == "" && len(files) == 0 {
		printHelp()
		return
	}

	mode := flags.Mode
	if mode == "auto" {
		if hasAudio {
			mode = "audio"
		} else if hasPdf {
			mode = "ocr"
		} else if hasImg {
			mode = "vision"
		} else {
			mode = "general"
		}
	}

	// Дефолтные промпты для файлов без текста
	if userPrompt == "" {
		switch mode {
		case "audio":
			userPrompt = "Запиши это аудио дословно."
		case "vision":
			userPrompt = "Опиши это изображение подробно."
		case "ocr":
			userPrompt = "Распознай текст с документа."
		}
	}

	modelName := PrimaryModel
	if list, ok := cfg.Models[mode]; ok && len(list) > 0 {
		modelName = list[0]
	}

	// Исправлено формирование контента: теперь используются ранее объявленные ContentPart и ImageUrl
	var currentUserContent interface{}
	if len(files) == 0 {
		currentUserContent = userPrompt
	} else {
		parts := []ContentPart{{Type: "text", Text: userPrompt}}
		for _, f := range files {
			parts = append(parts, ContentPart{
				Type: "image_url",
				ImageUrl: &ImageUrl{
					Url: fmt.Sprintf("data:%s;base64,%s", f.MimeType, f.Base64Content),
				},
			})
		}
		currentUserContent = parts
	}

	finalSys := cfg.SystemPrompt
	if flags.System != "" {
		finalSys = flags.System
	}
	finalTemp := cfg.Temperature
	if flags.Temp != -1.0 {
		finalTemp = flags.Temp
	}

	var history *ChatHistory
	if flags.ChatID != "" {
		history, _ = loadChatHistory(flags.ChatID)
	}

	// Ротация ключей Pollinations
	keys := make([]string, 0)
	for _, k := range cfg.ApiKeys {
		if strings.TrimSpace(k) != "" {
			keys = append(keys, k)
		}
	}

	if len(keys) == 0 {
		// Pollinations может работать без ключа, но мы следуем логике конфига
		keys = []string{""}
	}

	rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })

	var lastErr error
	for _, key := range keys {
		suffix := "none"
		if len(key) > 4 {
			suffix = "..." + key[len(key)-4:]
		}
		logVerbose("Запрос: модель=%s, режим=%s, ключ=%s", modelName, mode, suffix)

		res, err := requestPollinations(key, cfg.BaseURL, modelName, finalSys, currentUserContent, finalTemp, cfg.MaxTokens, flags.Json, history, flags.NoTools)
		if err == nil {
			if flags.ChatID != "" {
				updateAndSaveHistory(flags.ChatID, history, currentUserContent, res, cfg)
			}
			printOutput(res, flags.Json)
			return
		}
		lastErr = err
		// Если ошибка 401 или 429 — пробуем следующий ключ
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "429") {
			continue
		}
		break
	}
	fatal("Ошибка: %v", lastErr)
}

// --- Остальные утилиты ---

// Унифицированный парсер аргументов, поддерживающий -flag и --flag (как в geminillm)
func parseArgs() *UnifiedFlags {
	flags := &UnifiedFlags{
		Mode: "auto",
		Temp: -1.0,
	}

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Обработка --flag и -flag
		isLong := strings.HasPrefix(arg, "--")
		isShort := !isLong && strings.HasPrefix(arg, "-")

		if !isLong && !isShort {
			continue
		}

		key := strings.TrimPrefix(arg, "--")
		if isShort {
			key = strings.TrimPrefix(arg, "-")
		}

		switch key {
		case "h", "help", "?":
			printHelp()
			os.Exit(0)
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
		case "add-tavily-key":
			i++
			if i < len(args) {
				flags.AddTavilyKey = args[i]
			}
		case "chat", "chat-id":
			i++
			if i < len(args) {
				flags.ChatID = args[i]
			}
		case "clear-chat":
			i++
			if i < len(args) {
				flags.ClearChat = args[i]
			}
		case "no-tools":
			flags.NoTools = true
		}
	}
	return flags
}

func printHelp() {
	fmt.Printf("Pollinations CLI Utility (plnllm) v0.19\n\n")
	fmt.Printf("Использование:\n")
	fmt.Printf("  echo \"Привет\" | plnllm.exe [флаги]\n")
	fmt.Printf("  plnllm.exe -f image.png -s \"Опиши картинку\"\n\n")
	fmt.Printf("Основные флаги:\n")
	fmt.Printf("  -f, --file <путь>          Добавить файл (изображение, аудио, документ)\n")
	fmt.Printf("  -s, --system <текст>       Системный промпт (инструкция для модели)\n")
	fmt.Printf("  -m, --mode <режим>         Режим: auto (default), general, code, vision, audio, ocr\n")
	fmt.Printf("  -j, --json                 Форсировать ответ в формате JSON\n")
	fmt.Printf("  -t, --temp <число>         Температура генерации (0.0 - 2.0)\n")
	fmt.Printf("  -v, --verbose              Подробный вывод в stderr и лог\n\n")
	fmt.Printf("Управление чатом:\n")
	fmt.Printf("  -chat, --chat-id <id>      Идентификатор чата для сохранения истории\n")
	fmt.Printf("  --clear-chat <id>          Очистить историю указанного чата\n\n")
	fmt.Printf("Инструменты и ключи:\n")
	fmt.Printf("  --no-tools                 Отключить вызов инструментов (Calculator/Search)\n")
	fmt.Printf("  --save-key <ключ>          Сохранить API ключ Pollinations в конфиг\n")
	fmt.Printf("  --add-tavily-key <ключ>    Добавить API ключ Tavily для поиска\n")
}

// Функция обновлена для поддержки миграций, лимитов истории и автоматического заполнения полей
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	var cfg Config

	if err != nil {
		// Если файла нет, создаем дефолтный конфиг
		cfg = Config{
			BaseURL:                DefaultBaseURL,
			SystemPrompt:           DefaultSystemPrompt,
			Temperature:            0.7,
			MaxTokens:              8000,
			Models:                 DefaultModels,
			ChatHistoryMaxMessages: 30,
			ChatHistoryMaxChars:    50000,
			ImageCharCost:          2000,
			ApiKeys:                []string{""},
		}
		_ = saveConfig(path, &cfg)
		return &cfg, nil
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга конфигурации: %v", err)
	}

	// Валидация и заполнение отсутствующих полей
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
		cfg.MaxTokens = 8000
		dirty = true
	}
	if cfg.ChatHistoryMaxMessages == 0 {
		cfg.ChatHistoryMaxMessages = 30
		dirty = true
	}
	if cfg.ChatHistoryMaxChars == 0 {
		cfg.ChatHistoryMaxChars = 50000
		dirty = true
	}
	if cfg.ImageCharCost == 0 {
		cfg.ImageCharCost = 2000
		dirty = true
	}

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

	// Если мы обновили структуру, сохраним её
	if dirty && len(cfg.ApiKeys) > 0 {
		_ = saveConfig(path, &cfg)
	}

	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	f, _ := os.Create(path)
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func addKeyToConfig(path, key string) error {
	cfg, _ := loadConfig(path)
	newK := []string{}
	for _, k := range cfg.ApiKeys {
		if strings.TrimSpace(k) != "" && k != key {
			newK = append(newK, k)
		}
	}
	cfg.ApiKeys = append(newK, key)
	return saveConfig(path, cfg)
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

// Функция обновлена для поддержки транскрибации и перекодировки сложных форматов (AMR и др.)
func processFiles(paths []string) (res []FileData, hasImg, hasAudio, hasPdf bool) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(p))
		mt := mime.TypeByExtension(ext)

		// Авто-определение и перекодировка специфичных форматов
		if ext == ".amr" || ext == ".opus" || ext == ".ogg" {
			if transcoded, err := transcodeAudioWithFFmpeg(p); err == nil {
				data = transcoded
				mt = "audio/wav"
			}
		}

		if mt == "" || mt == "application/octet-stream" {
			switch ext {
			case ".mp3":
				mt = "audio/mpeg"
			case ".wav":
				mt = "audio/wav"
			case ".pdf":
				mt = "application/pdf"
			default:
				if utf8.Valid(data) {
					mt = "text/plain"
				}
			}
		}

		if strings.HasPrefix(mt, "image/") {
			hasImg = true
		}
		if strings.HasPrefix(mt, "audio/") {
			hasAudio = true
		}
		if mt == "application/pdf" {
			hasPdf = true
		}

		res = append(res, FileData{
			Name:          filepath.Base(p),
			MimeType:      mt,
			Base64Content: base64.StdEncoding.EncodeToString(data),
		})
	}
	return
}

// Базовая функция для получения пути к папке данных приложения в AppData
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

// Добавлены функции путей для устранения ошибок компиляции и унификации с mistral
// getConfigPath возвращает путь к основному файлу конфигурации pollinations.conf
func getConfigPath() (string, error) {
	dir, err := getAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigFileName), nil
}

// getLogFilePath возвращает путь к файлу логов для функции appendLog
func getLogFilePath() string {
	dir, err := getAppDataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, LogFileName)
}

// Добавлены функции управления ключами Tavily для работы инструментов поиска
// getTavilyConfigPath возвращает путь к общему конфигу Tavily
func getTavilyConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "clipgen-m", "tavily.conf")
}

// addTavilyKey добавляет новый API ключ Tavily в конфигурацию, создавая файл при необходимости
func addTavilyKey(newKey string) error {
	configPath := getTavilyConfigPath()

	configDir := filepath.Dir(configPath)
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		_ = os.MkdirAll(configDir, 0755)
	}

	var config struct {
		ApiKeys []string `json:"api_keys"`
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return err
		}
	}

	for _, existingKey := range config.ApiKeys {
		if existingKey == newKey {
			return fmt.Errorf("ключ уже существует в конфигурации")
		}
	}

	config.ApiKeys = append(config.ApiKeys, newKey)

	file, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(config)
}

// Добавлена очистка галлюцинаций Whisper, как в groqllm
func removeDimaTorzok(text string) string {
	trash := []string{
		"Субтитры сделал DimaTorzok.", "Субтитры сделал DimaTorzok",
		"Субтитры добавил DimaTorzok.", "Субтитры создавал DimaTorzok.",
		"Субтитры создавал DimaTorzok", "Субтитры добавил DimaTorzok",
		"Субтитры делал DimaTorzok", "DimaTorzok.", "DimaTorzok",
		"Продолжение следует...", "Подпишись на канал",
	}
	for _, t := range trash {
		text = strings.ReplaceAll(text, t, "")
	}
	return strings.TrimSpace(text)
}

// Добавлена перекодировка аудио через ffmpeg для поддержки всех форматов, как в geminillm
func transcodeAudioWithFFmpeg(inputPath string) ([]byte, error) {
	tempOutput, err := os.CreateTemp("", "transcoded_*.wav")
	if err != nil {
		return nil, err
	}
	tempPath := tempOutput.Name()
	tempOutput.Close()
	defer os.Remove(tempPath)

	// Стандартные параметры для ИИ: 16kHz, моно, 64k
	cmd := exec.Command("ffmpeg", "-i", inputPath, "-ar", "16000", "-ac", "1", "-b:a", "64k", "-y", tempPath)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(tempPath)
}
