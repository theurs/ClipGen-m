// file: internal/config/config.go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ChatSettings настройки конкретной сессии
type ChatSettings struct {
	SystemPrompt string  `json:"system_prompt"`
	Temperature  float64 `json:"temperature"`
	ModelMode    string  `json:"model_mode"` // "auto", "creative", "precise" и т.д.
	LLMProvider  string  `json:"llm_provider"` // "mistral", "geminillm", "ghllm", "groqllm", "ollama" и т.д.
}

// Config глобальная конфигурация приложения
type Config struct {
	// Параметры окна
	Width         int  `json:"width"`
	Height        int  `json:"height"`
	X             int  `json:"x"`
	Y             int  `json:"y"`
	SendCtrlEnter bool `json:"send_ctrl_enter"`

	// Глобальные настройки по умолчанию (для новых чатов)
	DefaultSettings ChatSettings `json:"default_settings"`

	// Настройки для каждого чата: map[chat_id]ChatSettings
	Sessions map[string]ChatSettings `json:"sessions"`

	// Мьютекс для безопасной записи из разных горутин (на будущее)
	mu sync.Mutex
}

func GetConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "chatui_config.json"
	}
	appDir := filepath.Join(configDir, "clipgen-m")
	_ = os.MkdirAll(appDir, 0755)
	return filepath.Join(appDir, "chatui_config.json")
}

func Load() *Config {
	// Значения по умолчанию
	cfg := &Config{
		Width:         600,
		Height:        800,
		SendCtrlEnter: false,
		Sessions:      make(map[string]ChatSettings),
		DefaultSettings: ChatSettings{
			SystemPrompt: "Ты полезный и точный ассистент. Не используй markdown разметку в своих ответах. Отвечай простым текстом без символов форматирования, звездочек, решеток и других элементов markdown. Для математических выражений используй Unicode символы вместо LaTeX формул. Формулы записывай обычным текстом с использованием математических символов Unicode.",
			Temperature:  0.7,
			ModelMode:    "auto",
			LLMProvider:  "mistral",
		},
	}

	file, err := os.ReadFile(GetConfigPath())
	if err != nil {
		return cfg
	}

	_ = json.Unmarshal(file, cfg)

	// Если карта сессий nil после загрузки (старый конфиг), инициализируем её
	if cfg.Sessions == nil {
		cfg.Sessions = make(map[string]ChatSettings)
	}

	return cfg
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(GetConfigPath(), data, 0644)
}

// GetChatSettings возвращает настройки для конкретного чата.
// Если их нет — возвращает дефолтные.
func (c *Config) GetChatSettings(chatID string) ChatSettings {
	if settings, ok := c.Sessions[chatID]; ok {
		return settings
	}
	return c.DefaultSettings
}

// SetChatSettings сохраняет настройки для чата
func (c *Config) SetChatSettings(chatID string, settings ChatSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Sessions == nil {
		c.Sessions = make(map[string]ChatSettings)
	}
	c.Sessions[chatID] = settings
}

// RemoveChatSettings удаляет настройки (например, при удалении чата)
func (c *Config) RemoveChatSettings(chatID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Sessions, chatID)
}
