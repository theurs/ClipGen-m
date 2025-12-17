// file: internal/mistral/client.go
package mistral

import (
	"bytes"
	"context" // <--- Важно
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

type RunOptions struct {
	Prompt       string
	ChatID       string
	Files        []string
	SystemPrompt string
	Temperature  float64
	ModelMode    string
}

// Run теперь принимает контекст для отмены
func Run(ctx context.Context, opts RunOptions) (string, error) {
	args := []string{}

	if opts.ChatID != "" {
		args = append(args, "-chat", opts.ChatID)
	}
	for _, f := range opts.Files {
		args = append(args, "-f", f)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "-s", opts.SystemPrompt)
	}
	if opts.Temperature >= 0 {
		args = append(args, "-t", fmt.Sprintf("%.2f", opts.Temperature))
	}
	if opts.ModelMode != "" && opts.ModelMode != "auto" {
		args = append(args, "-m", opts.ModelMode)
	}

	// Создаем команду с контекстом.
	// Если ctx будет отменен, процесс mistral.exe будет убит автоматически.
	cmd := exec.CommandContext(ctx, "mistral.exe", args...)

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
		// Проверяем, не была ли это принудительная отмена
		if ctx.Err() == context.Canceled {
			return "⏹ Генерация остановлена пользователем.", nil
		}

		if stderr.Len() > 0 {
			return stderr.String(), nil
		}
		return "", err
	}

	return out.String(), nil
}
