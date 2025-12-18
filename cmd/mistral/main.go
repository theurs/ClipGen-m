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
	"os/exec"
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
	DefaultSystemPrompt = "Вы — ИИ-ассистент, интегрированный в инструмент командной строки Windows под названием ClipGen-m. Ваш вывод часто копируется непосредственно в буфер обмена пользователя или вставляется в редакторы кода.\n\nРУКОВОДСТВО:\n1. Будьте лаконичны и прямолинейны.\n2. Если ввод — это лог ошибки, кратко объясните причину.\n3. Не используйте разговорные фразы типа 'Вот код'.\n4. Пиши простой текст без маркдауна."
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
	ApiKeys                []string            `json:"api_keys"`
	BaseURL                string              `json:"base_url"`
	SystemPrompt           string              `json:"system_prompt"`
	Temperature            float64             `json:"temperature"`
	MaxTokens              int                 `json:"max_tokens"`
	Models                 map[string][]string `json:"models"`
	ChatHistoryMaxMessages int                 `json:"chat_history_max_messages"` // максимальное количество сообщений (по умолчанию 30)
	ChatHistoryMaxChars    int                 `json:"chat_history_max_chars"`    // максимальное количество символов (по умолчанию 50000)
	ImageCharCost          int                 `json:"image_char_cost"`           // стоимость изображения в символах (по умолчанию 2000)
}

type ChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
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

// --- Структуры данных для истории чата ---
type ChatMessageHistory struct {
	Role      string      `json:"role"`
	Content   interface{} `json:"content"`
	Size      int         `json:"size"`
	Timestamp time.Time   `json:"timestamp"`
}

type ChatHistory struct {
	ID       string               `json:"id"`
	Messages []ChatMessageHistory `json:"messages"`
}

type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  struct {
		Type       string                 `json:"type"`
		Properties map[string]interface{} `json:"properties"`
		Required   []string               `json:"required"`
	} `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (tc ToolCall) MarshalJSON() ([]byte, error) {
	// Убедимся, что Type всегда равен "function" при маршализации
	type Alias ToolCall
	aux := struct {
		*Alias
		Type string `json:"type"`
	}{
		Alias: (*Alias)(&tc),
		Type:  "function", // Принудительно устанавливаем тип в "function"
	}
	return json.Marshal(aux)
}

type ToolMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id"`
	Name       string     `json:"name"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	Tools          []Tool        `json:"tools,omitempty"`
	ToolChoice     interface{}   `json:"tool_choice,omitempty"` // "auto", "required", или объект
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
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
	flagFiles        arrayFlags
	flagSystem       string
	flagJson         bool
	flagMode         string
	flagTemp         float64
	flagVerbose      bool
	flagSaveKey      string
	flagChatID       string
	flagClearChat    string
	flagTools        bool
	flagNoTools      bool
	flagAddTavilyKey string
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
	flag.StringVar(&flagChatID, "chat", "", "ID чата для контекста (включает режим чата)")
	flag.StringVar(&flagClearChat, "clear-chat", "", "Очистить историю указанного чата")
	flag.BoolVar(&flagTools, "tools", false, "Включить режим вызова инструментов (устаревший, инструменты теперь включены по умолчанию)")
	flag.BoolVar(&flagNoTools, "no-tools", false, "Отключить режим вызова инструментов")
	flag.StringVar(&flagAddTavilyKey, "add-tavily-key", "", "Добавить Tavily API ключ и выйти")
}

// --- Main ---

func main() {
	flag.Parse()

	// Check if we're adding a Tavily key
	if flagAddTavilyKey != "" {
		err := addTavilyKey(flagAddTavilyKey)
		if err != nil {
			fatal("Ошибка добавления Tavily ключа: %v", err)
		}
		fmt.Printf("Tavily ключ добавлен в %s\n", getTavilyConfigPath())
		return
	}

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

	// Проверяем флаг очистки чата, если задан - очищаем и завершаем работу
	if flagClearChat != "" {
		if err := clearChatHistory(flagClearChat); err != nil {
			fatal("Ошибка очистки истории чата: %v", err)
		}
		fmt.Printf("История чата '%s' очищена\n", flagClearChat)
		return
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

	// Проверяем команду /clear в тексте для очистки текущего чата
	if flagChatID != "" && strings.TrimSpace(userPrompt) == "/clear" {
		if err := clearChatHistory(flagChatID); err != nil {
			fatal("Ошибка очистки истории чата: %v", err)
		}
		fmt.Printf("История чата '%s' очищена командой /clear\n", flagChatID)
		return
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
				if flagChatID != "" {
					// Режим чата - загружаем историю
					chatHistory, err := loadChatHistory(flagChatID)
					if err != nil {
						errReq = fmt.Errorf("ошибка загрузки истории чата: %v", err)
					} else {
						// Формируем контекст запроса с историей
						result, errReq = requestChatWithHistory(apiKey, baseURL, modelName, finalSystem, userPrompt, filesData, finalTemp, config.MaxTokens, flagJson, chatHistory)
					}
				} else {
					result, errReq = requestChat(apiKey, baseURL, modelName, finalSystem, userPrompt, filesData, finalTemp, config.MaxTokens, flagJson)
				}
			}

			if errReq == nil {
				// Если используется режим чата, сохраняем обновленную историю
				if flagChatID != "" {
					chatHistory, err := loadChatHistory(flagChatID)
					if err == nil {
						userMessage := ChatMessage{
							Role:    "user",
							Content: formatChatContent(userPrompt, filesData),
						}
						updateChatHistory(chatHistory, userMessage, result, config)
						if saveErr := saveChatHistory(chatHistory); saveErr != nil {
							logVerbose("Ошибка сохранения истории чата: %v", saveErr)
						}
					}
				}
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

func createCalculatorTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "calculator",
			Description: "Executes Lua scripts for mathematical calculations, algorithms, and general script execution",
			Parameters: struct {
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			}{
				Type: "object",
				Properties: map[string]interface{}{
					"expression": map[string]interface{}{
						"type":        "string",
						"description": "Lua script to execute - can be mathematical expression, algorithm function, loop, or any valid Lua code (e.g., '2 + 3 * 4', 'math.sqrt(16)', 'math.sin(math.pi / 2)', 'function factorial(n) if n <= 1 then return 1 else return n * factorial(n-1) end; return factorial(10)', 'for i=1,10 do sum = sum or 0; sum = sum + i end; return sum')",
					},
				},
				Required: []string{"expression"},
			},
		},
	}
}

func createTavilyTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "tavily_search",
			Description: "Performs web searches to get current information on various topics",
			Parameters: struct {
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			}{
				Type: "object",
				Properties: map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query for finding current information",
					},
				},
				Required: []string{"query"},
			},
		},
	}
}

// executeTavilySearch performs web search using Tavily API
func executeTavilySearch(query string) (string, error) {
	// Get the path to tavily.conf
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("error getting config directory: %v", err)
	}

	configPath := filepath.Join(configDir, "clipgen-m", "tavily.conf")

	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("error reading tavily config: %v", err)
	}

	var config struct {
		ApiKeys []string `json:"api_keys"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("error parsing tavily config: %v", err)
	}

	if len(config.ApiKeys) == 0 {
		return "", fmt.Errorf("no API keys found in tavily.conf")
	}

	// Shuffle the API keys randomly to distribute usage
	shuffledKeys := make([]string, len(config.ApiKeys))
	copy(shuffledKeys, config.ApiKeys)

	// Create a new random source for shuffling
	rand.Seed(time.Now().UnixNano())
	for i := len(shuffledKeys) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		shuffledKeys[i], shuffledKeys[j] = shuffledKeys[j], shuffledKeys[i]
	}

	// Try each API key until one works
	var lastError error
	for _, apiKey := range shuffledKeys {
		if apiKey == "" {
			continue
		}

		// Prepare the request payload
		payload := map[string]interface{}{
			"api_key":             apiKey,
			"query":               query,
			"search_depth":        "basic",
			"include_images":      false,
			"include_answer":      true,
			"include_raw_content": false,
			"max_results":         3, // Limiting results to avoid context overload
		}

		// Convert payload to JSON
		jsonData, err := json.Marshal(payload)
		if err != nil {
			continue // Try next key
		}

		// Make the request to Tavily API
		resp, err := http.Post(
			"https://api.tavily.com/search",
			"application/json",
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			lastError = err
			continue // Try next key
		}
		defer resp.Body.Close()

		// Read the response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastError = err
			continue // Try next key
		}

		// Parse the response
		var searchResp map[string]interface{}
		err = json.Unmarshal(body, &searchResp)
		if err != nil {
			lastError = err
			continue // Try next key
		}

		// Check if the request was successful
		if errStr, hasError := searchResp["error"].(string); !hasError || errStr == "" {
			// Extract answer and results
			var resultStrings []string

			if answer, ok := searchResp["answer"].(string); ok && answer != "" {
				resultStrings = append(resultStrings, fmt.Sprintf("Summary: %s", answer))
			}

			if results, ok := searchResp["results"].([]interface{}); ok {
				// Limit to top 2 results to avoid context overload
				maxResults := 2
				if len(results) < maxResults {
					maxResults = len(results)
				}

				for i := 0; i < maxResults; i++ {
					if result, ok := results[i].(map[string]interface{}); ok {
						if title, hasTitle := result["title"].(string); hasTitle {
							if url, hasUrl := result["url"].(string); hasUrl {
								content := ""
								if cont, hasCont := result["content"].(string); hasCont {
									// Limit content length to avoid context overload
									if len(cont) > 4000 {
										cont = cont[:4000] + "..."
									}
									content = fmt.Sprintf(" - %s", cont)
								}
								resultStrings = append(resultStrings, fmt.Sprintf("%d. [%s](%s)%s", i+1, title, url, content))
							}
						}
					}
				}
			}

			return strings.Join(resultStrings, "\n"), nil
		} else {
			// Check if the error is related to the API key
			lastError = fmt.Errorf("API error: %s", errStr)
		}
	}

	if lastError != nil {
		return "", fmt.Errorf("all API keys failed: %v", lastError)
	}

	return "", fmt.Errorf("all API keys failed without specific error")
}

func executeCalculator(expression string) (string, error) {
	// Try to find lua-executor.exe in various locations

	// First, try local directory
	localPath := "./lua-executor/lua-executor.exe"
	if _, err := os.Stat(localPath); err == nil {
		cmd := exec.Command(localPath, expression)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			return "", fmt.Errorf("error executing calculator binary (%s): %v, stderr: %s", localPath, err, stderr.String())
		}

		return strings.TrimSpace(stdout.String()), nil
	}

	// Second, try executable in current directory
	currentDirPath := "./lua-executor.exe"
	if _, err := os.Stat(currentDirPath); err == nil {
		cmd := exec.Command(currentDirPath, expression)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			return "", fmt.Errorf("error executing calculator binary (%s): %v, stderr: %s", currentDirPath, err, stderr.String())
		}

		return strings.TrimSpace(stdout.String()), nil
	}

	// Third, try to find lua-executor.exe in PATH
	luaExecPath, err := exec.LookPath("lua-executor.exe")
	if err == nil {
		cmd := exec.Command(luaExecPath, expression)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			return "", fmt.Errorf("error executing calculator binary (%s): %v, stderr: %s", luaExecPath, err, stderr.String())
		}

		return strings.TrimSpace(stdout.String()), nil
	}

	// Fallback: try to execute with go run
	cmd := fmt.Sprintf("cd lua-executor && go run main.go \"%s\"", expression)
	out, err := runCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("error executing calculator (fallback method): %v", err)
	}
	return strings.TrimSpace(out), nil
}

func runCommand(cmdStr string) (string, error) {
	// Split the command to handle "cd directory && command" pattern
	parts := strings.Split(cmdStr, " && ")

	if len(parts) == 1 {
		// Simple command
		cmd := exec.Command("cmd", "/c", parts[0])
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			return "", fmt.Errorf("command failed: %v, stderr: %s", err, stderr.String())
		}
		return stdout.String(), nil
	} else if len(parts) >= 2 {
		// "cd dir && command" pattern
		dir := strings.TrimPrefix(parts[0], "cd ")
		dir = strings.TrimSpace(dir)
		command := strings.Join(parts[1:], " && ")

		cmd := exec.Command("cmd", "/c", command)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			return "", fmt.Errorf("command failed: %v, stderr: %s", err, stderr.String())
		}
		return stdout.String(), nil
	}

	return "", fmt.Errorf("invalid command format")
}

func requestChatWithTools(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, maxTokens int, jsonMode bool) (string, error) {
	// Create the initial messages
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

	// Create the tools
	calculatorTool := createCalculatorTool()
	tavilyTool := createTavilyTool()
	tools := []Tool{calculatorTool, tavilyTool}

	// Maximum number of tool call iterations to prevent infinite loops
	maxIterations := 5
	currentIteration := 0
	var resp ChatResponse

	// Loop to handle multiple rounds of tool calls
	for currentIteration < maxIterations {
		url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

		reqBody := ChatRequest{
			Model:       model,
			Messages:    messages,
			Temperature: temp,
			MaxTokens:   maxTokens,
			Tools:       tools,
			ToolChoice:  "auto", // Let the model decide when to use tools
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

		resp = ChatResponse{} // Reset response
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			return "", fmt.Errorf("json parse error: %v | Body: %s", err, string(respBytes))
		}
		if resp.Error != nil {
			return "", fmt.Errorf("api error %s: %s", resp.Error.Code, resp.Error.Message)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty choices")
		}

		choice := resp.Choices[0]

		// Check if the model wants to call any tools
		if len(choice.Message.ToolCalls) > 0 {
			// Add the assistant message with tool calls to the conversation
			// This preserves the original tool calls for the API
			assistantMessage := ChatMessage{
				Role:      "assistant",
				Content:   choice.Message.Content,   // This can be empty if only tool calls
				ToolCalls: choice.Message.ToolCalls, // Include the original tool calls
			}
			messages = append(messages, assistantMessage)

			// Process each tool call and add results as separate messages
			for _, toolCall := range choice.Message.ToolCalls {
				if toolCall.Function.Name == "calculator" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						return "", fmt.Errorf("error parsing tool arguments: %v", err)
					}

					// Convert the expression to string - args["expression"] is interface{}
					expression, ok := args["expression"].(string)
					if !ok {
						return "", fmt.Errorf("expression argument is not a string")
					}

					logVerbose("Executing calculator tool with expression: %s", expression)
					result, err := executeCalculator(expression)
					if err != nil {
						return "", fmt.Errorf("error executing calculator: %v", err)
					}

					logVerbose("Calculator result: %s", result[:min(len(result), 500)]) // Log first 500 chars of result

					// Add the tool result to the conversation
					toolResultMsg := ChatMessage{
						Role:       "tool",
						Content:    result,
						ToolCallID: toolCall.ID,
					}
					messages = append(messages, toolResultMsg)
				} else if toolCall.Function.Name == "tavily_search" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						return "", fmt.Errorf("error parsing tool arguments: %v", err)
					}

					// Convert the query to string - args["query"] is interface{}
					query, ok := args["query"].(string)
					if !ok {
						return "", fmt.Errorf("query argument is not a string")
					}

					logVerbose("Executing tavily search tool with query: %s", query)
					result, err := executeTavilySearch(query)
					if err != nil {
						return "", fmt.Errorf("error executing tavily search: %v", err)
					}

					logVerbose("Tavily result: %s", result[:min(len(result), 500)]) // Log first 500 chars of result

					// Add the tool result to the conversation
					toolResultMsg := ChatMessage{
						Role:       "tool",
						Content:    result,
						ToolCallID: toolCall.ID,
					}
					messages = append(messages, toolResultMsg)
				}
			}

			// Continue to next iteration to get model's response to tool results
			currentIteration++
		} else {
			// No more tool calls, return final response
			return choice.Message.Content, nil
		}
	}

	// If we've reached max iterations without a complete response, return an error
	return "", fmt.Errorf("reached maximum iterations without complete response")
}

func requestChat(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, maxTokens int, jsonMode bool) (string, error) {
	if !flagNoTools {
		return requestChatWithTools(apiKey, baseURL, model, systemPrompt, userText, files, temp, maxTokens, jsonMode)
	}

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

func formatChatContent(userText string, files []FileData) interface{} {
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
	return content
}

func requestChatWithToolsHistory(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, maxTokens int, jsonMode bool, chatHistory *ChatHistory) (string, error) {
	// Создаем начальные сообщения с системным промптом
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Добавляем сообщения из истории чата
	for _, msg := range chatHistory.Messages {
		if msg.Role == "user" || msg.Role == "assistant" || msg.Role == "tool" {
			messages = append(messages, ChatMessage{Role: msg.Role, Content: msg.Content})
		}
	}

	// Добавляем текущий запрос
	currentContent := formatChatContent(userText, files)
	messages = append(messages, ChatMessage{Role: "user", Content: currentContent})

	// Create the tools
	calculatorTool := createCalculatorTool()
	tavilyTool := createTavilyTool()
	tools := []Tool{calculatorTool, tavilyTool}

	// Maximum number of tool call iterations to prevent infinite loops
	maxIterations := 5
	currentIteration := 0

	// Loop to handle multiple rounds of tool calls
	for currentIteration < maxIterations {
		url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

		reqBody := ChatRequest{
			Model:       model,
			Messages:    messages,
			Temperature: temp,
			MaxTokens:   maxTokens,
			Tools:       tools,
			ToolChoice:  "auto", // Let the model decide when to use tools
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

		choice := resp.Choices[0]

		// Check if the model wants to call any tools
		if len(choice.Message.ToolCalls) > 0 {
			// Add the assistant message with tool calls to the conversation
			// This preserves the original tool calls for the API
			assistantMessage := ChatMessage{
				Role:      "assistant",
				Content:   choice.Message.Content,   // This can be empty if only tool calls
				ToolCalls: choice.Message.ToolCalls, // Include the original tool calls
			}
			messages = append(messages, assistantMessage)

			// Process each tool call and add results as separate messages
			for _, toolCall := range choice.Message.ToolCalls {
				if toolCall.Function.Name == "calculator" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						return "", fmt.Errorf("error parsing tool arguments: %v", err)
					}

					// Convert the expression to string - args["expression"] is interface{}
					expression, ok := args["expression"].(string)
					if !ok {
						return "", fmt.Errorf("expression argument is not a string")
					}

					logVerbose("Executing calculator tool with expression: %s", expression)
					result, err := executeCalculator(expression)
					if err != nil {
						return "", fmt.Errorf("error executing calculator: %v", err)
					}

					logVerbose("Calculator result: %s", result[:min(len(result), 500)]) // Log first 500 chars of result

					// Add the tool result to the conversation
					toolResultMsg := ChatMessage{
						Role:       "tool",
						Content:    result,
						ToolCallID: toolCall.ID,
					}
					messages = append(messages, toolResultMsg)
				} else if toolCall.Function.Name == "tavily_search" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						return "", fmt.Errorf("error parsing tool arguments: %v", err)
					}

					// Convert the query to string - args["query"] is interface{}
					query, ok := args["query"].(string)
					if !ok {
						return "", fmt.Errorf("query argument is not a string")
					}

					logVerbose("Executing tavily search tool with query: %s", query)
					result, err := executeTavilySearch(query)
					if err != nil {
						return "", fmt.Errorf("error executing tavily search: %v", err)
					}

					logVerbose("Tavily result: %s", result[:min(len(result), 500)]) // Log first 500 chars of result

					// Add the tool result to the conversation
					toolResultMsg := ChatMessage{
						Role:       "tool",
						Content:    result,
						ToolCallID: toolCall.ID,
					}
					messages = append(messages, toolResultMsg)
				}
			}

			// Continue to next iteration to get model's response to tool results
			currentIteration++
		} else {
			// No more tool calls, return final response
			return choice.Message.Content, nil
		}
	}

	// If we've reached max iterations without a complete response, return an error
	return "", fmt.Errorf("reached maximum iterations without complete response")
}

func requestChatWithHistory(apiKey, baseURL, model, systemPrompt, userText string, files []FileData, temp float64, maxTokens int, jsonMode bool, chatHistory *ChatHistory) (string, error) {
	if !flagNoTools {
		return requestChatWithToolsHistory(apiKey, baseURL, model, systemPrompt, userText, files, temp, maxTokens, jsonMode, chatHistory)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

	// Создаем начальные сообщения с системным промптом
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Добавляем сообщения из истории чата
	for _, msg := range chatHistory.Messages {
		if msg.Role == "user" || msg.Role == "assistant" {
			messages = append(messages, ChatMessage{Role: msg.Role, Content: msg.Content})
		}
	}

	// Добавляем текущий запрос
	currentContent := formatChatContent(userText, files)
	messages = append(messages, ChatMessage{Role: "user", Content: currentContent})

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

	// Устанавливаем параметры истории чата, если не заданы
	if cfg.ChatHistoryMaxMessages == 0 {
		cfg.ChatHistoryMaxMessages = 30 // значение по умолчанию
		dirty = true
	}
	if cfg.ChatHistoryMaxChars == 0 {
		cfg.ChatHistoryMaxChars = 50000 // значение по умолчанию
		dirty = true
	}
	if cfg.ImageCharCost == 0 {
		cfg.ImageCharCost = 2000 // значение по умолчанию
		dirty = true
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

// --- Функции для работы с историей чата ---

func getChatDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	chatDir := filepath.Join(configDir, ConfigDirName, "mistral_chats")
	if _, err := os.Stat(chatDir); os.IsNotExist(err) {
		_ = os.MkdirAll(chatDir, 0755)
	}
	return chatDir, nil
}

func getChatFilePath(chatID string) (string, error) {
	chatDir, err := getChatDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(chatDir, chatID+".json"), nil
}

func loadChatHistory(chatID string) (*ChatHistory, error) {
	chatFilePath, err := getChatFilePath(chatID)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(chatFilePath); os.IsNotExist(err) {
		// Если файла нет, создаем новую историю
		return &ChatHistory{
			ID:       chatID,
			Messages: []ChatMessageHistory{},
		}, nil
	}

	data, err := os.ReadFile(chatFilePath)
	if err != nil {
		return nil, err
	}

	var history ChatHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}

	return &history, nil
}

func saveChatHistory(history *ChatHistory) error {
	chatFilePath, err := getChatFilePath(history.ID)
	if err != nil {
		return err
	}

	file, err := os.Create(chatFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(history)
}

func clearChatHistory(chatID string) error {
	chatFilePath, err := getChatFilePath(chatID)
	if err != nil {
		return err
	}

	// Удаляем файл истории чата
	return os.Remove(chatFilePath)
}

func calculateMessageSize(content interface{}, imageCharCost int) int {
	size := 0
	switch v := content.(type) {
	case string:
		size = len(v)
	case []ContentPart:
		for _, part := range v {
			switch part.Type {
			case "text":
				size += len(part.Text)
			case "image_url":
				// Изображения считаются за фиксированное количество символов (по умолчанию 2000)
				size += imageCharCost
			case "input_audio":
				// Аудио тоже может занимать место, можно установить свою стоимость при необходимости
				// Пока просто добавим длину текста и base64 данных
				size += len(part.Text)
				if part.InputAudio != nil && part.InputAudio.Data != "" {
					size += len(part.InputAudio.Data) // Длина base64 данных
				}
			}
		}
	}
	return size
}

func updateChatHistory(history *ChatHistory, userMessage ChatMessage, assistantResponse string, config *Config) {
	// Устанавливаем значения по умолчанию, если они не заданы
	maxMessages := config.ChatHistoryMaxMessages
	if maxMessages == 0 {
		maxMessages = 30 // значение по умолчанию
	}
	maxChars := config.ChatHistoryMaxChars
	if maxChars == 0 {
		maxChars = 50000 // значение по умолчанию
	}
	imageCharCost := config.ImageCharCost
	if imageCharCost == 0 {
		imageCharCost = 2000 // значение по умолчанию
	}

	// Добавляем сообщение пользователя
	userSize := calculateMessageSize(userMessage.Content, imageCharCost)
	history.Messages = append(history.Messages, ChatMessageHistory{
		Role:      userMessage.Role,
		Content:   userMessage.Content,
		Size:      userSize,
		Timestamp: time.Now(),
	})

	// Добавляем ответ ассистента
	assistantSize := len(assistantResponse)
	history.Messages = append(history.Messages, ChatMessageHistory{
		Role:      "assistant",
		Content:   assistantResponse,
		Size:      assistantSize,
		Timestamp: time.Now(),
	})

	// Применяем ограничения
	applyHistoryLimits(history, maxMessages, maxChars)
}

func applyHistoryLimits(history *ChatHistory, maxMessages, maxChars int) {
	// Ограничиваем количество сообщений
	if len(history.Messages) > maxMessages {
		// Удаляем самые старые сообщения
		history.Messages = history.Messages[len(history.Messages)-maxMessages:]
	}

	// Ограничиваем размер контекста
	totalSize := 0
	for _, msg := range history.Messages {
		totalSize += msg.Size
	}

	for totalSize > maxChars && len(history.Messages) > 0 {
		// Удаляем самое старое сообщение
		removedMsg := history.Messages[0]
		history.Messages = history.Messages[1:]
		totalSize -= removedMsg.Size
	}
}

func getTavilyConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "clipgen-m", "tavily.conf")
}

func addTavilyKey(newKey string) error {
	configPath := getTavilyConfigPath()

	// Ensure directory exists
	configDir := filepath.Dir(configPath)
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		_ = os.MkdirAll(configDir, 0755)
	}

	// Load existing config or create new one
	var config struct {
		ApiKeys []string `json:"api_keys"`
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		// Read existing config
		data, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(data, &config); err != nil {
			return err
		}
	}

	// Check if key already exists
	for _, existingKey := range config.ApiKeys {
		if existingKey == newKey {
			return fmt.Errorf("ключ уже существует в конфигурации")
		}
	}

	// Add new key
	config.ApiKeys = append(config.ApiKeys, newKey)

	// Write back to file
	file, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(config)
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
