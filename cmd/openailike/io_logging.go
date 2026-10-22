package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// --- Ввод/вывод и логирование ---

// readStdin читает текст из stdin с учетом кодировок консоли Windows
func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Данные пришли из пайпа
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
	return ""
}

// printOutput печатает результат, при json-режиме убирает markdown-заборчики
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

func appendLog(level, format string, v ...interface{}) {
	dir, _ := getAppDataDir()
	logPath := filepath.Join(dir, LogFileName)

	fi, err := os.Stat(logPath)
	if err == nil && fi.Size() > MaxLogSize {
		_ = os.Remove(logPath + ".old")
		_ = os.Rename(logPath, logPath+".old")
	}

	msg := fmt.Sprintf(format, v...)
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, msg)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(logLine)
	}
}

var flagVerbose bool // Глобальная переменная для совместимости с logVerbose

func logVerbose(format string, v ...interface{}) {
	appendLog("INFO", format, v...)
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "[OpenAILike] "+format+"\n", v...)
	}
}

func fatal(format string, v ...interface{}) {
	appendLog("FATAL", format, v...)
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", v...)
	os.Exit(1)
}

// --- Вспомогательные проверки ---

// isTextual определяет, является ли файл текстовым
func isTextual(mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json", "application/xml", "application/x-yaml", "application/javascript", "application/x-sh":
		return true
	}
	return false
}

// isResponseFormatUnsupported определяет, отверг ли бэкенд параметр response_format
func isResponseFormatUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "response_format") ||
		strings.Contains(msg, "response format") ||
		strings.Contains(msg, "json_object") ||
		strings.Contains(msg, "unsupported")
}
