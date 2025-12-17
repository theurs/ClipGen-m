// file: internal/mistral/client.go
package mistral

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
)

// Run отправляет запрос к mistral.exe
// files - список путей к файлам (может быть пустым)
func Run(prompt string, chatID string, files []string) (string, error) {
	args := []string{}

	// Добавляем ID чата
	if chatID != "" {
		args = append(args, "-chat", chatID)
	}

	// Добавляем файлы: каждый файл через свой флаг -f
	for _, f := range files {
		args = append(args, "-f", f)
	}

	cmd := exec.Command("mistral.exe", args...)

	// Скрываем консольное окно mistral.exe при запуске (чтобы не мелькало)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	cmd.Stdin = strings.NewReader(prompt)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
		if stderr.Len() > 0 {
			return stderr.String(), nil
		}
		return "", err
	}

	return out.String(), nil
}
