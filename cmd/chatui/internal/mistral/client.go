// file: internal/mistral/client.go
package mistral

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// RunOptions параметры для запуска
type RunOptions struct {
	Prompt       string
	ChatID       string
	Files        []string
	SystemPrompt string
	Temperature  float64
	ModelMode    string // "auto", "code", "vision" и т.д.
}

// Run отправляет запрос к mistral.exe с расширенными параметрами
func Run(opts RunOptions) (string, error) {
	args := []string{}

	// 1. Чат ID
	if opts.ChatID != "" {
		args = append(args, "-chat", opts.ChatID)
	}

	// 2. Файлы
	for _, f := range opts.Files {
		args = append(args, "-f", f)
	}

	// 3. Системный промпт (переопределяет конфиг mistral.conf)
	if opts.SystemPrompt != "" {
		args = append(args, "-s", opts.SystemPrompt)
	}

	// 4. Температура
	// Передаем только если она отличается от дефолта или явно задана
	// Для надежности передаем всегда, форматируя как строку
	if opts.Temperature >= 0 {
		args = append(args, "-t", fmt.Sprintf("%.2f", opts.Temperature))
	}

	// 5. Режим модели (если задан и не auto)
	if opts.ModelMode != "" && opts.ModelMode != "auto" {
		args = append(args, "-m", opts.ModelMode)
	}

	cmd := exec.Command("mistral.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	cmd.Stdin = strings.NewReader(opts.Prompt)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
		if stderr.Len() > 0 {
			// Если stderr не пустой, возвращаем его как текст ошибки
			return stderr.String(), nil
		}
		return "", err
	}

	return out.String(), nil
}
