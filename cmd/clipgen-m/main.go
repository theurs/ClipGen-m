package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/micmonay/keybd_event"
	"golang.design/x/clipboard"
	"golang.design/x/hotkey"
	"gopkg.in/yaml.v3"

	_ "golang.org/x/image/bmp"
)

// ==========================================================
// WINDOWS API DEFINITIONS
// ==========================================================
var (
	user32           = syscall.NewLazyDLL("user32.dll")
	shell32          = syscall.NewLazyDLL("shell32.dll")
	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	getClipboardData = user32.NewProc("GetClipboardData")
	dragQueryFile    = shell32.NewProc("DragQueryFileW")
)

const (
	CF_HDROP = 15
)

// ==========================================================
// CONFIG STRUCTURES
// ==========================================================
type Config struct {
	Actions []Action `yaml:"actions"`
}
type Action struct {
	Name        string `yaml:"name"`
	Hotkey      string `yaml:"hotkey"`
	Prompt      string `yaml:"prompt"`
	MistralArgs string `yaml:"mistral_args"`
	InputType   string `yaml:"input_type"` // text, image, files
	OutputMode  string `yaml:"output_mode,omitempty"`
}

var (
	iconNormal []byte
	iconWait   []byte
	config     Config
	logFile    *os.File
)

// ==========================================================
// MAIN & LIFECYCLE
// ==========================================================
func main() {
	setupLogging()
	defer logFile.Close()

	log.Println("=== ClipGen-m Запущен ===")

	// Инициализация буфера
	if err := clipboard.Init(); err != nil {
		log.Fatalf("FATAL: Ошибка инициализации clipboard: %v", err)
	}

	systray.Run(onReady, onExit)
}

func onReady() {
	loadIcons()

	if err := loadOrCreateConfig(); err != nil {
		log.Printf("ERROR: Ошибка загрузки конфига: %v", err)
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
// LOGGING
// ==========================================================
func setupLogging() {
	// Лог пишем в папку конфига пользователя
	configDir, _ := os.UserConfigDir()
	appDir := filepath.Join(configDir, "clipgen-m")
	os.MkdirAll(appDir, 0755)

	logPath := filepath.Join(appDir, "clipgen.log")

	var err error
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		// Если не вышло в папку конфига, пробуем рядом с exe
		logFile, _ = os.OpenFile("clipgen.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	}

	// Дублируем лог в консоль и в файл
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
}

func openLogFile() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "clipgen-m", "clipgen.log")
	cmd := exec.Command("notepad.exe", logPath)
	cmd.Start()
}

// ==========================================================
// ACTION HANDLER
// ==========================================================
func handleAction(action Action) {
	log.Printf("Запуск действия: %s [%s]", action.Name, action.InputType)

	systray.SetIcon(iconWait)
	defer systray.SetIcon(iconNormal)

	// Пауза для UI
	time.Sleep(250 * time.Millisecond)

	var resultText string
	var err error

	// === ВЫПОЛНЕНИЕ ===
	switch action.InputType {
	case "files":
		resultText, err = handleFilesAction(action)

	case "image":
		imageBytes := clipboard.Read(clipboard.FmtImage)
		if len(imageBytes) == 0 {
			err = fmt.Errorf("буфер не содержит изображения")
		} else {
			img, _, decodeErr := image.Decode(bytes.NewReader(imageBytes))
			if decodeErr != nil {
				err = fmt.Errorf("ошибка декодирования картинки: %v", decodeErr)
			} else {
				tempFile, _ := saveImageToTemp(img)
				defer os.Remove(tempFile)

				// Обработка промпта (вдруг там есть {{ask_user}})
				finalPrompt, pErr := preparePrompt(action.Prompt)
				if pErr != nil {
					log.Printf("Отмена ввода пользователем: %v", pErr)
					return
				} else {
					resultText, err = runMistral(finalPrompt, []string{tempFile}, action.MistralArgs)
				}
			}
		}

	case "text":
		clipboardText, copyErr := copySelection()
		if copyErr != nil {
			err = copyErr
		} else {
			log.Printf("Скопировано символов: %d", len(clipboardText))
			basePrompt := strings.Replace(action.Prompt, "{{.clipboard}}", clipboardText, 1)

			finalPrompt, pErr := preparePrompt(basePrompt)
			if pErr != nil {
				log.Printf("Отмена ввода")
				return
			}
			resultText, err = runMistral(finalPrompt, nil, action.MistralArgs)
		}
	}

	// === ОБРАБОТКА ОШИБОК ===
	if err != nil {
		log.Printf("ERROR: %v", err)
		showInNotepad(fmt.Sprintf("ОШИБКА ВЫПОЛНЕНИЯ:\n\n%v\n\nСмотри лог для деталей.", err))
		return
	}

	if resultText == "" {
		log.Println("Результат пустой (действие отменено или модель промолчала).")
		return
	}

	// === ВЫВОД РЕЗУЛЬТАТА ===
	log.Println("Успех. Вывод результата.")
	switch action.OutputMode {
	case "notepad":
		if err := showInNotepad(resultText); err != nil {
			log.Printf("Ошибка открытия блокнота: %v", err)
		}
	default: // replace
		clipboard.Write(clipboard.FmtText, []byte(resultText))
		time.Sleep(100 * time.Millisecond)
		paste()
	}
}

// ----------------------------------------------------------
// ЛОГИКА ФАЙЛОВ (PIPELINE)
// ----------------------------------------------------------
func handleFilesAction(action Action) (string, error) {
	files, err := getClipboardFiles()
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("нет файлов в буфере")
	}

	log.Printf("Получено файлов: %d. %v", len(files), files)

	var finalFileList []string
	var audioFiles []string

	// Сортировка
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".ogg" {
			audioFiles = append(audioFiles, f)
		} else {
			finalFileList = append(finalFileList, f)
		}
	}

	// Транскрипция Аудио
	if len(audioFiles) > 0 {
		log.Printf("Найдено аудио (%d), начинаю транскрипцию...", len(audioFiles))
		for _, audioPath := range audioFiles {
			// Внимание: Промпт должен быть понятен модели
			prompt := "Transcribe this audio file to text verbatim. Output ONLY the text."

			transText, err := runMistral(prompt, []string{audioPath}, "")
			if err != nil {
				log.Printf("WARN: Ошибка транскрипции %s: %v", audioPath, err)
				transText = fmt.Sprintf("[Ошибка чтения аудио %s]", filepath.Base(audioPath))
			} else if strings.TrimSpace(transText) == "" {
				transText = "[Пустая транскрипция]"
			}

			// Сохраняем транскрипцию как текстовый файл
			tempTxt, err := saveTextToTemp(transText)
			if err != nil {
				return "", err
			}
			defer os.Remove(tempTxt)

			// Добавляем этот текст в список для финального анализа
			finalFileList = append(finalFileList, tempTxt)
			log.Printf("Аудио %s преобразовано в текст.", filepath.Base(audioPath))
		}
	}

	// Запрос к пользователю
	finalPrompt, err := preparePrompt(action.Prompt)
	if err != nil {
		return "", nil // Отмена
	}
	if finalPrompt == "" {
		return "", nil
	}

	log.Printf("Отправка финального запроса с %d файлами...", len(finalFileList))
	return runMistral(finalPrompt, finalFileList, action.MistralArgs)
}

// ----------------------------------------------------------
// HELPER: INPUT BOX & PROMPT
// ----------------------------------------------------------
func preparePrompt(promptTmpl string) (string, error) {
	if strings.Contains(promptTmpl, "{{ask_user}}") {
		// Заголовок окна на английском (VBS не любит кириллицу в заголовках скрипта),
		// но ВВОДИТЬ внутрь можно по-русски.
		userInput, err := showInputBox("Enter task for these files:", "")
		if err != nil {
			return "", err
		}
		if userInput == "" {
			return "", fmt.Errorf("input canceled")
		}
		return strings.Replace(promptTmpl, "{{ask_user}}", userInput, 1), nil
	}
	return promptTmpl, nil
}

func showInputBox(title, defaultText string) (string, error) {
	log.Println("Opening InputBox...")

	// 1. Создаем файлы для скрипта и результата
	scriptFile, err := os.CreateTemp("", "dialog-*.vbs")
	if err != nil {
		return "", err
	}
	defer os.Remove(scriptFile.Name())

	resultFile, err := os.CreateTemp("", "result-*.txt")
	if err != nil {
		return "", err
	}
	resultPath := resultFile.Name()
	resultFile.Close() // Закрываем, чтобы скрипт мог в него писать
	defer os.Remove(resultPath)

	// 2. VBScript с использованием ADODB.Stream для записи в UTF-8
	// Это единственный способ в VBScript гарантировать кодировку
	vbsContent := fmt.Sprintf(`
Dim text
text = InputBox("%s", "ClipGen AI", "%s")

' Используем ADODB для записи в UTF-8 без BOM (или с ним, Go разберется)
Set objStream = CreateObject("ADODB.Stream")
objStream.CharSet = "utf-8"
objStream.Open
objStream.WriteText text
objStream.SaveToFile "%s", 2 ' 2 = adSaveCreateOverWrite
objStream.Close
`, title, defaultText, resultPath)

	if _, err := scriptFile.WriteString(vbsContent); err != nil {
		return "", err
	}
	scriptFile.Close()

	// 3. Запускаем wscript (он скрытый, без черного окна)
	cmd := exec.Command("wscript", scriptFile.Name())
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("script execution failed: %v", err)
	}

	// 4. Читаем результат
	content, err := os.ReadFile(resultPath)
	if err != nil {
		return "", err
	}

	// Go отлично читает UTF-8. TrimSpace уберет возможные BOM или переносы
	res := strings.TrimSpace(string(content))

	if res == "" {
		log.Println("User input was empty or canceled.")
	} else {
		log.Printf("User input received: %s", res)
	}

	return res, nil
}

// ----------------------------------------------------------
// HELPER: RUN MISTRAL
// ----------------------------------------------------------
func runMistral(prompt string, filePaths []string, args string) (string, error) {
	argList := strings.Fields(args)
	for _, f := range filePaths {
		argList = append(argList, "-f", f)
	}

	log.Printf("Вызов Mistral. Файлы: %d, Аргументы: %v", len(filePaths), argList)

	cmd := exec.Command("mistral.exe", argList...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(prompt)

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("exit code: %v | stderr: %s", err, stderr.String())
	}
	duration := time.Since(start)
	log.Printf("Mistral завершен за %v. Ответ получен (%d байт).", duration, out.Len())

	return strings.TrimSpace(out.String()), nil
}

// ----------------------------------------------------------
// HELPER: FILES & CLIPBOARD
// ----------------------------------------------------------
func getClipboardFiles() ([]string, error) {
	// Открываем буфер
	r, _, _ := openClipboard.Call(0)
	if r == 0 {
		return nil, fmt.Errorf("не удалось открыть clipboard")
	}
	defer closeClipboard.Call()

	// Есть ли файлы (HDROP)?
	hDrop, _, _ := getClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, nil // Не ошибка, просто нет файлов
	}

	// Количество файлов
	cnt, _, _ := dragQueryFile.Call(hDrop, 0xFFFFFFFF, 0, 0)
	if cnt == 0 {
		return nil, nil
	}

	var files []string
	for i := uintptr(0); i < cnt; i++ {
		// Узнаем размер буфера для пути
		lenRet, _, _ := dragQueryFile.Call(hDrop, i, 0, 0)
		if lenRet == 0 {
			continue
		}

		// Читаем путь (Unicode)
		buf := make([]uint16, lenRet+1)
		dragQueryFile.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), lenRet+1)

		files = append(files, syscall.UTF16ToString(buf))
	}
	return files, nil
}

func copySelection() (string, error) {
	originalContent := clipboard.Read(clipboard.FmtText)
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return "", err
	}
	kb.SetKeys(keybd_event.VK_C)
	kb.HasCTRL(true)
	kb.Launching()

	startTime := time.Now()
	for time.Since(startTime) < time.Second*1 {
		newContent := clipboard.Read(clipboard.FmtText)
		if len(newContent) > 0 && !bytes.Equal(newContent, originalContent) {
			return string(newContent), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("буфер обмена не изменился (ничего не выделено?)")
}

func paste() {
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return
	}
	kb.SetKeys(keybd_event.VK_V)
	kb.HasCTRL(true)
	kb.Launching()
}

// ----------------------------------------------------------
// HELPER: TEMP FILES
// ----------------------------------------------------------
func showInNotepad(content string) error {
	tempFile, err := saveTextToTemp(content)
	if err != nil {
		return err
	}
	defer os.Remove(tempFile) // Удаляем после закрытия окна

	cmd := exec.Command("notepad.exe", tempFile)
	return cmd.Run() // Ждем закрытия
}

func saveImageToTemp(img image.Image) (string, error) {
	tempFile, err := os.CreateTemp("", "clipgen-img-*.png")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()
	return tempFile.Name(), png.Encode(tempFile, img)
}

func saveTextToTemp(content string) (string, error) {
	tempFile, err := os.CreateTemp("", "clipgen-txt-*.txt")
	if err != nil {
		return "", err
	}
	// Пишем BOM для корректной кириллицы в Блокноте
	tempFile.Write([]byte{0xEF, 0xBB, 0xBF})
	tempFile.WriteString(content)
	tempFile.Close()
	return tempFile.Name(), nil
}

// ==========================================================
// TRAY & CONFIG
// ==========================================================
func setupTray() {
	systray.SetIcon(iconNormal)
	systray.SetTitle("ClipGen-m")

	mLog := systray.AddMenuItem("Открыть лог", "Посмотреть ошибки")
	mReload := systray.AddMenuItem("Перезагрузка", "Применить конфиг")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")

	go func() {
		for {
			select {
			case <-mLog.ClickedCh:
				openLogFile()
			case <-mReload.ClickedCh:
				log.Println("Запрос перезагрузки...")
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
		return
	}
	cmd := exec.Command(executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()
	systray.Quit()
}

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
		log.Println("Конфиг не найден, создание нового...")
		os.MkdirAll(filepath.Dir(path), 0755)

		// ПОЛНЫЙ КОНФИГ
		defaultConfig := `actions:
  # --- ТЕКСТ ---
  - name: "Исправить текст (F1)"
    hotkey: "Ctrl+F1"
    prompt: |
      Исправь грамматику, пунктуацию и стиль. Верни ТОЛЬКО исправленный текст.
      Текст: {{.clipboard}}
    mistral_args: "-t 0.1"
    input_type: "text"
    output_mode: "replace"

  - name: "Перевести (F3)"
    hotkey: "Ctrl+F3"
    prompt: "Переведи на русский (если уже рус - на англ). Верни только перевод.\n{{.clipboard}}"
    mistral_args: ""
    input_type: "text"
    output_mode: "notepad"

  - name: "Объяснить (F6)"
    hotkey: "Ctrl+F6"
    prompt: "Объясни это простыми словами:\n{{.clipboard}}"
    mistral_args: ""
    input_type: "text"
    output_mode: "notepad"

  - name: "Ответить на вопрос (F7)"
    hotkey: "Ctrl+F7"
    prompt: "Ответь на вопрос:\n{{.clipboard}}"
    mistral_args: ""
    input_type: "text"
    output_mode: "notepad"
  
  - name: "Выполнить просьбу (F8)"
    hotkey: "Ctrl+F8"
    prompt: "Выполни просьбу:\n{{.clipboard}}"
    mistral_args: ""
    input_type: "text"
    output_mode: "replace"

  # --- КАРТИНКИ ---
  - name: "OCR / Текст с картинки (F12)"
    hotkey: "Ctrl+F12"
    prompt: "Извлеки весь текст с изображения."
    mistral_args: ""
    input_type: "image"
    output_mode: "notepad"

  # --- ФАЙЛЫ (МУЛЬТИМОДАЛЬНОСТЬ) ---
  - name: "Анализ файлов (F10)"
    hotkey: "Ctrl+F10"
    # {{ask_user}} вызовет окно ввода
    prompt: |
      Ты аналитик. Используй предоставленные файлы (текст, изображения, транскрипции аудио).
      Задача пользователя: {{ask_user}}
    mistral_args: ""
    input_type: "files"
    output_mode: "notepad"`

		os.WriteFile(path, []byte(defaultConfig), 0644)
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
			log.Printf("Ошибка хоткея '%s': %v", action.Hotkey, err)
			continue
		}
		hk := hotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			log.Printf("Не удалось зарегистрировать '%s': %v", action.Hotkey, err)
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
				return nil, 0, fmt.Errorf("unknown key")
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
