package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// mainUnified — основной сценарий работы утилиты
func mainUnified() {
	flags := parseArgs()
	rand.Seed(time.Now().UnixNano())

	// Устанавливаем глобальную переменную для совместимости с logVerbose
	flagVerbose = flags.Verbose

	configPath, err := getConfigPath()
	if err != nil {
		fatal("Ошибка пути конфига: %v", err)
	}

	if flags.SaveKey != "" {
		if err := addKeyToConfig(configPath, flags.SaveKey); err != nil {
			fatal("Ошибка сохранения ключа: %v", err)
		}
		fmt.Printf("Ключ сохранен в %s\n", configPath)
		return
	}

	config, err := loadConfig(configPath)
	if err != nil {
		fatal("Ошибка конфига: %v", err)
	}

	baseURL := resolveBaseURL(config, flags)
	systemPrompt := flags.System
	if systemPrompt == "" {
		systemPrompt = config.SystemPrompt
	}
	temp := flags.Temp
	if temp <= 0 {
		temp = config.Temperature
	}

	// Список ключей может быть пустым — локальные серверы часто не требуют авторизации
	apiKeys := filterEmptyKeys(config.ApiKeys)

	if flags.ListModels {
		models, err := fetchModels(baseURL, firstKey(apiKeys))
		if err != nil {
			fatal("Не удалось получить список моделей: %v", err)
		}
		for _, m := range models {
			fmt.Println(m)
		}
		return
	}

	userPrompt := readStdin()
	filesData, hasImages, hasAudio, hasPdf := processFiles(flags.Files)

	if userPrompt == "" && len(filesData) == 0 {
		fatal("Нет данных для обработки (пустой ввод)")
	}

	mode := determineMode(flags.Mode, userPrompt, hasImages, hasAudio, hasPdf)

	if flags.Json && mode != "audio" {
		userPrompt += "\nОТВЕТ ТОЛЬКО В ФОРМАТЕ JSON."
	}

	// История чата (единый формат, общий с geminillm/groqllm)
	var history *ChatHistory
	if flags.ChatID != "" {
		history, _ = loadChatHistory(flags.ChatID)
	}

	modelsList := selectModelList(mode, config, baseURL, apiKeys)

	// Явно указанная модель имеет приоритет над списком из конфига/сервера
	if flags.Model != "" {
		modelsList = []string{flags.Model}
	}

	// --- Цикл перебора моделей и ключей ---
	var lastErr error
	bannedKeys := make(map[string]bool)

	for _, modelName := range modelsList {
		usedKeys := make(map[string]bool)
		for k := range bannedKeys {
			usedKeys[k] = true
		}

		keyAttempts := 0
		maxKeyAttempts := len(apiKeys) * 2
		if len(apiKeys) == 0 {
			maxKeyAttempts = 1
		}

		for keyAttempts < maxKeyAttempts {
			apiKey := ""
			if len(apiKeys) > 0 {
				apiKey = getRandomKey(apiKeys, usedKeys)
				if apiKey == "" {
					if attemptsResetted(apiKeys, usedKeys) {
						break
					}
					usedKeys = make(map[string]bool)
					for k := range bannedKeys {
						usedKeys[k] = true
					}
					apiKey = getRandomKey(apiKeys, usedKeys)
				}
				if apiKey == "" {
					break
				}
			}

			logVerbose("Модель [%s], Key [%s], Режим [%s], Base URL [%s]", modelName, maskKey(apiKey), mode, baseURL)

			var result string
			var errReq error

			switch mode {
			case "audio":
				if len(filesData) > 0 {
					result, errReq = requestAudio(apiKey, baseURL, modelName, filesData[0])
				} else {
					errReq = fmt.Errorf("режим audio требует файл")
				}
			default:
				result, errReq = requestChat(apiKey, baseURL, modelName, systemPrompt, userPrompt, filesData, temp, flags.Json, history)
			}

			if errReq == nil {
				printOutput(result, flags.Json)
				if flags.ChatID != "" && history != nil {
					saveHistory(flags.ChatID, history, userPrompt, result, config)
				}
				return
			}

			lastErr = errReq
			logVerbose("Ошибка (Key %s): %v", maskKey(apiKey), errReq)

			errMsg := strings.ToLower(errReq.Error())

			if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "unauthorized") {
				bannedKeys[apiKey] = true
				usedKeys[apiKey] = true
				keyAttempts++
				logVerbose("Ключ невалиден (401). Бан на время.")

			} else if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "rate limit") {
				logVerbose("Rate limit (429). Ждем 1 сек и меняем ключ...")
				time.Sleep(1 * time.Second)
				usedKeys[apiKey] = true
				keyAttempts++

			} else if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "503") || strings.Contains(errMsg, "service unavailable") {
				logVerbose("Модель %s лежит (500/503). Переход к следующей модели...", modelName)
				time.Sleep(1 * time.Second)
				break

			} else {
				logVerbose("Критическая ошибка: %v. Пробуем следующую модель...", errReq)
				break
			}
		}
	}

	fatal("Не удалось получить ответ после всех попыток. Последняя ошибка: %v", lastErr)
}
