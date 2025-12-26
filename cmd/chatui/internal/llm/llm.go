// file: internal/llm/llm.go
package llm

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// LLMProvider интерфейс для различных LLM-провайдеров
type LLMProvider interface {
	Run(ctx context.Context, opts RunOptions) (string, error)
}

// RunOptions опции запуска LLM
type RunOptions struct {
	Prompt       string
	ChatID       string
	Files        []string
	SystemPrompt string
	Temperature  float64
	ModelMode    string
}

// GetProvider возвращает экземпляр LLM-провайдера по названию
func GetProvider(providerName string) (LLMProvider, error) {
	switch strings.ToLower(providerName) {
	case "mistral":
		return &MistralClient{}, nil
	case "geminillm":
		return &GenericClient{command: "geminillm.exe"}, nil
	case "ghllm":
		return &GenericClient{command: "ghllm.exe"}, nil
	case "groqllm":
		return &GenericClient{command: "groqllm.exe"}, nil
	default:
		return &MistralClient{}, nil // Mistral по умолчанию
	}
}

// MistralClient клиент для Mistral
type MistralClient struct{}

func (m *MistralClient) Run(ctx context.Context, opts RunOptions) (string, error) {
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

	cmd := exec.CommandContext(ctx, "mistral.exe", args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	cmd.Stdin = strings.NewReader(opts.Prompt)

	var out strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
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

// GenericClient общий клиент для других LLM-утилит
type GenericClient struct {
	command string
}

func (g *GenericClient) Run(ctx context.Context, opts RunOptions) (string, error) {
	args := []string{}

	// Используем те же аргументы, что и mistral, так как geminillm, ghllm и groqllm
	// также поддерживают те же флаги, что и mistral
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

	cmd := exec.CommandContext(ctx, g.command, args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	cmd.Stdin = strings.NewReader(opts.Prompt)

	var out strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
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