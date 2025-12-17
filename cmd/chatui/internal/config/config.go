// file: internal/config/config.go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config описывает структуру нашего файла настроек
type Config struct {
	Width         int  `json:"width"`
	Height        int  `json:"height"`
	X             int  `json:"x"`
	Y             int  `json:"y"`
	SendCtrlEnter bool `json:"send_ctrl_enter"`
	// Позже добавим сюда LastChatID и Theme
}

// GetConfigPath возвращает путь к файлу конфигурации в AppData
func GetConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "chatui_config.json" // Фолбэк в текущую папку
	}
	// Путь: C:\Users\User\AppData\Roaming\clipgen-m\chatui_config.json
	appDir := filepath.Join(configDir, "clipgen-m")

	// Создаем папку, если её нет
	_ = os.MkdirAll(appDir, 0755)

	return filepath.Join(appDir, "chatui_config.json")
}

// Load загружает настройки или возвращает дефолтные
func Load() *Config {
	cfg := &Config{
		Width:         600, // Значения по умолчанию
		Height:        800,
		SendCtrlEnter: false,
	}

	file, err := os.ReadFile(GetConfigPath())
	if err != nil {
		return cfg // Если файла нет, возвращаем дефолт
	}

	_ = json.Unmarshal(file, cfg)
	return cfg
}

// Save сохраняет настройки на диск
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(GetConfigPath(), data, 0644)
}
