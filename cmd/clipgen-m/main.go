package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/micmonay/keybd_event"
	"golang.design/x/clipboard"
	"golang.design/x/hotkey"
	"gopkg.in/yaml.v3"

	_ "golang.org/x/image/bmp"
)

// Структуры YAML
type Config struct {
	Actions []Action `yaml:"actions"`
}
type Action struct {
	Name        string `yaml:"name"`
	Hotkey      string `yaml:"hotkey"`
	Prompt      string `yaml:"prompt"`
	MistralArgs string `yaml:"mistral_args"`
	InputType   string `yaml:"input_type"`
	OutputMode  string `yaml:"output_mode,omitempty"`
}

var (
	iconNormal []byte
	iconWait   []byte
	config     Config
)

func main() {
	// Инициализация буфера обмена
	if err := clipboard.Init(); err != nil {
		log.Fatalf("Ошибка инициализации буфера: %v", err)
	}
	systray.Run(onReady, onExit)
}

func onReady() {
	loadIcons()
	if err := loadOrCreateConfig(); err != nil {
		log.Fatalf("Ошибка конфига: %v", err)
		systray.Quit()
		return
	}
	setupTray()
	go listenHotkeys()
}

func onExit() {
	log.Println("Завершение работы.")
}

// ==========================================================
// УПРАВЛЕНИЕ ТРЕЕМ И ПЕРЕЗАГРУЗКОЙ
// ==========================================================
func setupTray() {
	systray.SetIcon(iconNormal)
	systray.SetTitle("ClipGen-m")
	systray.SetTooltip("AI-помощник активен")

	mReload := systray.AddMenuItem("Перезагрузка", "Применить изменения в конфиге")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")

	go func() {
		for {
			select {
			case <-mReload.ClickedCh:
				log.Println("Перезагрузка приложения...")
				restartApp()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func restartApp() {
	executable, err := os.Executable()
	if err != nil {
		log.Printf("Не удалось получить путь к exe: %v", err)
		return
	}

	// Запускаем новый процесс
	cmd := exec.Command(executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true, // Скрываем консоль нового процесса, если она есть
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Не удалось перезапустить: %v", err)
		return
	}

	// Завершаем текущий
	systray.Quit()
}

// ==========================================================
// ОСНОВНАЯ ЛОГИКА
// ==========================================================
func handleAction(action Action) {
	systray.SetIcon(iconWait)
	defer systray.SetIcon(iconNormal)

	// Даем время отпустить клавиши
	time.Sleep(250 * time.Millisecond)

	var resultText string
	var err error

	switch action.InputType {
	case "text":
		clipboardText, copyErr := copySelection()
		if copyErr != nil {
			log.Println(copyErr)
			return
		}
		prompt := strings.Replace(action.Prompt, "{{.clipboard}}", clipboardText, 1)
		resultText, err = runMistral(prompt, "", action.MistralArgs)

	case "image":
		imageBytes := clipboard.Read(clipboard.FmtImage)
		if len(imageBytes) == 0 {
			log.Println("Буфер не содержит изображения.")
			return
		}
		img, _, imgErr := image.Decode(bytes.NewReader(imageBytes))
		if imgErr != nil {
			log.Printf("Ошибка декодирования изображения: %v", imgErr)
			return
		}
		tempFile, createErr := saveImageToTemp(img)
		if createErr != nil {
			log.Printf("Ошибка сохранения картинки: %v", createErr)
			return
		}
		defer os.Remove(tempFile) // Удаляем картинку сразу после запроса
		resultText, err = runMistral(action.Prompt, tempFile, action.MistralArgs)
	}

	if err != nil {
		log.Printf("Ошибка Mistral: %v", err)
		return
	}
	if resultText == "" {
		return
	}

	// Вывод результата
	switch action.OutputMode {
	case "notepad":
		if err := showInNotepad(resultText); err != nil {
			log.Printf("Не удалось открыть Блокнот: %v", err)
		}
	default: // "replace"
		clipboard.Write(clipboard.FmtText, []byte(resultText))
		time.Sleep(100 * time.Millisecond)
		paste()
	}
}

// Надежное копирование текста
func copySelection() (string, error) {
	originalContent := clipboard.Read(clipboard.FmtText)

	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return "", fmt.Errorf("ошибка клавиатуры")
	}
	kb.SetKeys(keybd_event.VK_C)
	kb.HasCTRL(true)
	if err := kb.Launching(); err != nil {
		return "", fmt.Errorf("ошибка нажатия Ctrl+C")
	}

	startTime := time.Now()
	for time.Since(startTime) < time.Second*1 {
		newContent := clipboard.Read(clipboard.FmtText)
		if len(newContent) > 0 && !bytes.Equal(newContent, originalContent) {
			return string(newContent), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("не удалось скопировать (буфер не изменился)")
}

// Вставка текста (Ctrl+V)
func paste() {
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasCTRL(true)
	kb.Launching()
}

// Запуск Mistral
func runMistral(prompt, filePath, args string) (string, error) {
	argList := strings.Fields(args)
	if filePath != "" {
		argList = append(argList, "-f", filePath)
	}
	cmd := exec.Command("mistral.exe", argList...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(prompt)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ошибка: %v, stderr: %s", err, stderr.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// ==========================================================
// ФУНКЦИЯ БЛОКНОТА (БЕЗ МУСОРА)
// ==========================================================
func showInNotepad(content string) error {
	tempFile, err := os.CreateTemp("", "clipgen-*.txt")
	if err != nil {
		return err
	}
	filename := tempFile.Name()

	// Удаляем файл, когда функция завершится (после закрытия блокнота)
	defer os.Remove(filename)

	bom := []byte{0xEF, 0xBB, 0xBF}
	if _, err := tempFile.Write(bom); err != nil {
		tempFile.Close()
		return err
	}
	if _, err := tempFile.WriteString(content); err != nil {
		tempFile.Close()
		return err
	}
	tempFile.Close()

	// Используем Run вместо Start, чтобы ждать закрытия
	cmd := exec.Command("notepad.exe", filename)
	return cmd.Run()
}

// Сохранение временной картинки
func saveImageToTemp(img image.Image) (string, error) {
	tempFile, err := os.CreateTemp("", "clipgen-*.png")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()
	return tempFile.Name(), png.Encode(tempFile, img)
}

// ==========================================================
// КОНФИГУРАЦИЯ И ЗАГРУЗКА
// ==========================================================
func getConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(configDir, "clipgen-m")
	return filepath.Join(appDir, "config.yaml"), nil
}

func loadOrCreateConfig() error {
	path, err := getConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		// Дефолтный конфиг
		defaultConfig := `actions:
  - name: "Исправить текст (F1)"
    hotkey: "Ctrl+F1"
    prompt: |
      Исправь грамматику. Верни ТОЛЬКО исправленный текст.
      Текст: {{.clipboard}}
    mistral_args: "-t 0.1"
    input_type: "text"
    output_mode: "replace"
  - name: "Текст с картинки (F12)"
    hotkey: "Ctrl+F12"
    prompt: "Извлеки текст."
    mistral_args: ""
    input_type: "image"
    output_mode: "notepad"`
		if err := os.WriteFile(path, []byte(defaultConfig), 0644); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, &config)
}

func loadIcons() {
	iconNormal, _ = os.ReadFile("icon.ico")
	iconWait, _ = os.ReadFile("icon_wait.ico")
	if len(iconWait) == 0 {
		iconWait = iconNormal
	}
}

func listenHotkeys() {
	actionChan := make(chan Action)
	for _, action := range config.Actions {
		mods, key, err := parseHotkey(action.Hotkey)
		if err != nil {
			log.Printf("Ошибка парсинга хоткея '%s': %v", action.Hotkey, err)
			continue
		}
		hk := hotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			log.Printf("Не удалось зарегистрировать хоткей '%s': %v", action.Hotkey, err)
			continue
		}
		go func(a Action, h *hotkey.Hotkey) {
			for {
				<-h.Keydown()
				actionChan <- a
			}
		}(action, hk)
	}
	for action := range actionChan {
		go handleAction(action)
	}
}

func parseHotkey(hotkeyStr string) ([]hotkey.Modifier, hotkey.Key, error) {
	parts := strings.Split(hotkeyStr, "+")
	var mods []hotkey.Modifier
	var key hotkey.Key
	keyMap := map[string]hotkey.Key{
		"F1": hotkey.KeyF1, "F2": hotkey.KeyF2, "F3": hotkey.KeyF3, "F4": hotkey.KeyF4,
		"F5": hotkey.KeyF5, "F6": hotkey.KeyF6, "F7": hotkey.KeyF7, "F8": hotkey.KeyF8,
		"F9": hotkey.KeyF9, "F10": hotkey.KeyF10, "F11": hotkey.KeyF11, "F12": hotkey.KeyF12,
		"C": hotkey.KeyC, "X": hotkey.KeyX,
	}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if i == len(parts)-1 {
			if k, ok := keyMap[strings.ToUpper(part)]; ok {
				key = k
			} else {
				return nil, 0, fmt.Errorf("неизвестная клавиша: %s", part)
			}
		} else {
			switch strings.ToLower(part) {
			case "ctrl":
				mods = append(mods, hotkey.ModCtrl)
			case "shift":
				mods = append(mods, hotkey.ModShift)
			case "alt":
				mods = append(mods, hotkey.ModAlt)
			case "win":
				mods = append(mods, hotkey.ModWin)
			}
		}
	}
	return mods, key, nil
}
