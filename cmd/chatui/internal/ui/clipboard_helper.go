// file: internal/ui/clipboard_helper.go
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// HasClipboardFiles проверяет, есть ли в буфере файлы (копирование из проводника)
func HasClipboardFiles() bool {
	// Проверяем через PowerShell, есть ли FileDropList
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "if (Get-Clipboard -Format FileDropList) { exit 0 } else { exit 1 }")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	return err == nil
}

// GetClipboardFiles возвращает список путей
func GetClipboardFiles() ([]string, error) {
	// Получаем пути каждой новой строкой
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "(Get-Clipboard -Format FileDropList).FullName")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\r\n")
	var files []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}

	if len(files) == 0 {
		return nil, nil
	}
	return files, nil
}

// HasClipboardImage проверяет, есть ли картинка в буфере
func HasClipboardImage() bool {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "if (Get-Clipboard -Format Image) { exit 0 } else { exit 1 }")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	return err == nil
}

// SaveClipboardImageToTemp сохраняет картинку из буфера в файл
func SaveClipboardImageToTemp() (string, error) {
	tempName := fmt.Sprintf("clipgen_paste_%d.png", time.Now().UnixNano())
	tempPath := filepath.Join(os.TempDir(), tempName)

	psScript := fmt.Sprintf(`
		Add-Type -AssemblyName System.Windows.Forms
		$img = [System.Windows.Forms.Clipboard]::GetImage()
		if ($img -ne $null) {
			$img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
		}
	`, tempPath)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Run(); err != nil {
		return "", err
	}

	if _, err := os.Stat(tempPath); err == nil {
		return tempPath, nil
	}

	return "", fmt.Errorf("изображение не сохранено")
}
