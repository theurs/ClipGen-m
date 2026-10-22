package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Config — настройки утилиты (файл openai.conf в %APPDATA%\clipgen-m).
type Config struct {
	ApiKeys                []string            `json:"api_keys"`
	BaseURL                string              `json:"base_url"`
	SystemPrompt           string              `json:"system_prompt"`
	Temperature            float64             `json:"temperature"`
	Models                 map[string][]string `json:"models"`
	ChatHistoryMaxMessages int                 `json:"chat_history_max_messages"`
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

// loadConfig читает конфиг, создавая шаблон при первом запуске
func loadConfig(path string) (*Config, error) {
	dirty := false

	data, err := os.ReadFile(path)
	var cfg Config

	if err != nil {
		// Если файла нет — создаем его с дефолтными настройками
		cfg = Config{
			BaseURL:                DefaultBaseURL,
			SystemPrompt:           DefaultSystemPrompt,
			Temperature:            0.6,
			Models:                 DefaultModels,
			ChatHistoryMaxMessages: 30,
			ApiKeys:                []string{""}, // пустой ключ для локальных серверов без авторизации
		}
		dirty = true
	} else {
		// Если файл есть — читаем его
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("ошибка разбора конфигурации: %v", err)
		}

		// Проверяем наличие базовых полей
		if cfg.BaseURL == "" {
			cfg.BaseURL = DefaultBaseURL
			dirty = true
		}
		if cfg.SystemPrompt == "" {
			cfg.SystemPrompt = DefaultSystemPrompt
			dirty = true
		}
		if cfg.Temperature <= 0 {
			cfg.Temperature = 0.6
			dirty = true
		}
		if cfg.ChatHistoryMaxMessages == 0 {
			cfg.ChatHistoryMaxMessages = 30
			dirty = true
		}

		// Проверяем и восстанавливаем модели
		if cfg.Models == nil {
			cfg.Models = make(map[string][]string)
			dirty = true
		}
		for category, defaults := range DefaultModels {
			if list, exists := cfg.Models[category]; !exists || len(list) == 0 {
				cfg.Models[category] = defaults
				dirty = true
			}
		}

		// Если ключ не задан — добавляем пустой (работа без авторизации)
		if len(cfg.ApiKeys) == 0 {
			cfg.ApiKeys = []string{""}
			dirty = true
		}
	}

	// Если мы изменили или создали конфиг — сохраняем изменения
	if dirty {
		logVerbose("Конфигурация обновлена или создана. Сохранение в %s", path)
		if err := saveConfig(path, &cfg); err != nil {
			return nil, fmt.Errorf("не удалось сохранить исправленную конфигурацию: %v", err)
		}
	}

	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")

	// Записываем структуру конфигурации в формате JSON
	return enc.Encode(cfg)
}

func addKeyToConfig(path, key string) error {
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}

	// Проверяем, нет ли такого ключа уже в списке
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

	// Открываем файл на запись (создаем или перезаписываем)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")

	if err := enc.Encode(cfg); err != nil {
		return err
	}

	fmt.Printf("Ключ успешно добавлен в %s\n", path)
	return nil
}

// getRandomKey выбирает случайный ключ, который еще не использовался
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

// maskKey маскирует ключ для логов
func maskKey(k string) string {
	if k == "" {
		return "без ключа"
	}
	if len(k) < 10 {
		return "???"
	}
	return "..." + k[len(k)-4:]
}

// filterEmptyKeys убирает пустые ключи (серверы без авторизации)
func filterEmptyKeys(keys []string) []string {
	result := []string{}
	for _, k := range keys {
		if strings.TrimSpace(k) != "" {
			result = append(result, k)
		}
	}
	return result
}

// firstKey возвращает первый доступный ключ
func firstKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// resolveBaseURL определяет адрес API: флаг > конфиг > значение по умолчанию
func resolveBaseURL(cfg *Config, flags *UnifiedFlags) string {
	base := ""
	if flags != nil {
		base = flags.BaseURL
	}
	if base == "" && cfg != nil {
		base = cfg.BaseURL
	}
	if base == "" {
		base = DefaultBaseURL
	}
	// Убираем возможный завершающий слеш
	return strings.TrimRight(strings.TrimSpace(base), "/")
}
