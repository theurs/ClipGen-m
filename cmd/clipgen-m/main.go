package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/micmonay/keybd_event"
	"github.com/ncruces/zenity"
	"golang.design/x/clipboard"
	"golang.design/x/hotkey"
	"gopkg.in/yaml.v3"

	_ "image/jpeg"

	_ "golang.org/x/image/bmp"
)

// ==========================================================
// GLOBALS & WINAPI
// ==========================================================

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	shell32                    = syscall.NewLazyDLL("shell32.dll")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	globalSize                 = kernel32.NewProc("GlobalSize")
	dragQueryFile              = shell32.NewProc("DragQueryFileW")
	findWindow                 = user32.NewProc("FindWindowW")
	setForegroundWindow        = user32.NewProc("SetForegroundWindow")
)

const (
	CF_HDROP = 15
	CF_DIB   = 8
)

type BITMAPFILEHEADER struct {
	BfType      uint16
	BfSize      uint32
	BfReserved1 uint16
	BfReserved2 uint16
	BfOffBits   uint32
}

// НОВАЯ ФУНКЦИЯ: Безопасное открытие буфера с повторными попытками
// Это решает проблему "Access Denied", если буфер занят другим процессом
func tryOpenClipboard() (bool, error) {
	for i := 0; i < 20; i++ { // Пытаемся 20 раз
		r, _, _ := openClipboard.Call(0)
		if r != 0 {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond) // Пауза 10мс между попытками
	}
	return false, fmt.Errorf("не удалось открыть буфер обмена (занят другим приложением)")
}

// ==========================================================
// CONFIG STRUCTURES
// ==========================================================

type Config struct {
	EditorPath   string   `yaml:"editor_path"`   // Путь к редактору (Notepad, MarkText и т.д.)
	LLMPath      string   `yaml:"llm_path"`      // Путь к исполняемому файлу LLM (mistral.exe и т.д.)
	SystemPrompt string   `yaml:"system_prompt"` // Базовая инструкция для LLM
	Actions      []Action `yaml:"actions"`
}

type Action struct {
	Name        string `yaml:"name"`
	Hotkey      string `yaml:"hotkey"`
	Prompt      string `yaml:"prompt,omitempty"`
	MistralArgs string `yaml:"mistral_args,omitempty"`
	InputType   string `yaml:"input_type"`
	OutputMode  string `yaml:"output_mode,omitempty"`
}

var (
	iconNormal   []byte
	iconWait     []byte
	config       Config
	logFile      *os.File
	inputHistory = make(map[string]string)
)

// ==========================================================
// MAIN & LIFECYCLE
// ==========================================================

func main() {
	setupLogging()
	defer logFile.Close()

	log.Println("=== ClipGen-m Запущен ===")

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
	configDir, _ := os.UserConfigDir()
	appDir := filepath.Join(configDir, "clipgen-m")
	os.MkdirAll(appDir, 0755)
	logPath := filepath.Join(appDir, "clipgen.log")
	var err error
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		// Fallback если не удалось создать в папке пользователя
		logFile, _ = os.OpenFile("clipgen.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	}
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
}

func openLogFile() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "clipgen-m", "clipgen.log")

	// Используем редактор из конфига
	cmd := exec.Command(config.EditorPath, logPath)
	if err := cmd.Start(); err != nil {
		log.Printf("Ошибка открытия лога через %s: %v", config.EditorPath, err)
		// Fallback на обычный блокнот, если кастомный путь кривой
		exec.Command("notepad.exe", logPath).Start()
	}
}

// ==========================================================
// ACTION HANDLER
// ==========================================================

func handleAction(action Action) {
	log.Printf("Запуск действия: %s [%s]", action.Name, action.InputType)

	if action.InputType != "layout_switch" {
		systray.SetIcon(iconWait)
		defer systray.SetIcon(iconNormal)
		time.Sleep(250 * time.Millisecond)
	}

	var resultText string
	var err error

	switch action.InputType {
	case "auto":
		resultText, err = handleAutoAction(action)
	case "files":
		resultText, err = handleFilesAction(action)
	case "image":
		resultText, err = handleImageAction(action)
	case "text":
		resultText, err = handleTextAction(action)
	case "layout_switch":
		resultText, err = handleLayoutSwitchAction()
	}

	if err != nil {
		zenity.Error(fmt.Sprintf("Ошибка выполнения:\n%v", err),
			zenity.Title("ClipGen Error"),
			zenity.Icon(zenity.ErrorIcon))
		log.Printf("ERROR: %v", err)
		return
	}

	if resultText == "" {
		log.Println("Результат пустой (действие отменено или модель промолчала).")
		return
	}

	log.Println("Успех. Вывод результата.")
	switch action.OutputMode {
	case "notepad", "editor": // Поддерживаем оба названия для удобства
		if err := showInEditor(resultText); err != nil {
			log.Printf("Ошибка открытия редактора: %v", err)
		}
	default:
		// Режим replace / copy
		clipboard.Write(clipboard.FmtText, []byte(resultText))

		// ИЗМЕНЕНИЕ: Добавлена пауза перед вставкой.
		// Если вставить мгновенно, Windows может не успеть разблокировать буфер после записи.
		time.Sleep(200 * time.Millisecond)

		paste()
	}
}

// ==========================================================
// PUNTO SWITCHER LOGIC
// ==========================================================

var (
	engToRus = map[rune]rune{
		// Основной ряд (нижний регистр)
		'`': 'ё', 'q': 'й', 'w': 'ц', 'e': 'у', 'r': 'к', 't': 'е', 'y': 'н', 'u': 'г', 'i': 'ш', 'o': 'щ', 'p': 'з', '[': 'х', ']': 'ъ',
		'a': 'ф', 's': 'ы', 'd': 'в', 'f': 'а', 'g': 'п', 'h': 'р', 'j': 'о', 'k': 'л', 'l': 'д', ';': 'ж', '\'': 'э',
		'z': 'я', 'x': 'ч', 'c': 'с', 'v': 'м', 'b': 'и', 'n': 'т', 'm': 'ь', ',': 'б', '.': 'ю', '/': '.',

		// Основной ряд (верхний регистр + Shift)
		'~': 'Ё', 'Q': 'Й', 'W': 'Ц', 'E': 'У', 'R': 'К', 'T': 'Е', 'Y': 'Н', 'U': 'Г', 'I': 'Ш', 'O': 'Щ', 'P': 'З', '{': 'Х', '}': 'Ъ',
		'A': 'Ф', 'S': 'Ы', 'D': 'В', 'F': 'А', 'G': 'П', 'H': 'Р', 'J': 'О', 'K': 'Л', 'L': 'Д', ':': 'Ж', '"': 'Э',
		'Z': 'Я', 'X': 'Ч', 'C': 'С', 'V': 'М', 'B': 'И', 'N': 'Т', 'M': 'Ь', '<': 'Б', '>': 'Ю', '?': ',',

		// Цифровой ряд (Спецсимволы Shift+Цифра)
		// Важно: 1->1 не пишем, так как они совпадают, но Shift+Цифра отличаются
		'@': '"', '#': '№', '$': ';', '^': ':', '&': '?', '|': '/',

		// Специфические символы, которые часто забывают
		// В русской раскладке слэш (/) на месте пайпа (|)
		// А бэкслеш (\) часто совпадает, но иногда зависит от клавиатуры.
		// Обычно '\' -> '\', но Shift+'\' ('|') -> '/'.
	}
	rusToEng = make(map[rune]rune)
)

func init() {
	for k, v := range engToRus {
		rusToEng[v] = k
	}
}

func switchLayout(text string) string {
	engChars, rusChars := 0, 0
	for _, r := range text {
		if _, ok := engToRus[r]; ok {
			engChars++
		} else if _, ok := rusToEng[r]; ok {
			rusChars++
		}
	}
	var conversionMap map[rune]rune
	if rusChars > engChars {
		conversionMap = rusToEng
	} else if engChars > rusChars {
		conversionMap = engToRus
	} else {
		return text
	}
	var builder strings.Builder
	for _, r := range text {
		if convertedChar, ok := conversionMap[r]; ok {
			builder.WriteRune(convertedChar)
		} else {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func handleLayoutSwitchAction() (string, error) {
	selectedText, err := copySelection()
	if err != nil {
		return "", err
	}
	return switchLayout(selectedText), nil
}

// ==========================================================
// AI LOGIC
// ==========================================================

func handleAutoAction(action Action) (string, error) {
	log.Println("Режим 'auto': определяем тип данных в буфере...")

	// 1. Пробуем найти саму картинку (байты)
	imageBytes := clipboard.Read(clipboard.FmtImage)
	if len(imageBytes) == 0 {
		var apiErr error
		imageBytes, apiErr = getClipboardImageViaAPI()
		if apiErr != nil {
			log.Printf("API fallback: %v", apiErr)
		}
	}
	if len(imageBytes) > 0 {
		return processImage(imageBytes, action)
	}

	// 2. Пробуем найти список файлов (CF_HDROP)
	files, _ := getClipboardFiles()
	if len(files) > 0 {
		return processFiles(files, action)
	}

	// 3. Пробуем получить текст
	clipboardText, err := copySelection()
	if err == nil && len(clipboardText) > 0 {
		// --- НОВАЯ ЛОГИКА ---
		// Проверяем, не является ли скопированный текст путем к файлу картинки
		cleanedPath := strings.Trim(strings.TrimSpace(clipboardText), "\"")
		if isImageFile(cleanedPath) {
			log.Printf("В буфере найден путь к картинке: %s", cleanedPath)
			// Передаем как список из одного файла в обработчик файлов
			return processFiles([]string{cleanedPath}, action)
		}
		// --------------------

		return processText(clipboardText, action)
	}

	return "", fmt.Errorf("буфер пуст или формат не поддерживается")
}

func handleTextAction(action Action) (string, error) {
	clipboardText, err := copySelection()
	if err != nil {
		return "", err
	}
	return processText(clipboardText, action)
}

func handleImageAction(action Action) (string, error) {
	imageBytes := clipboard.Read(clipboard.FmtImage)

	// Попытка через WinAPI
	if len(imageBytes) == 0 {
		var err error
		imageBytes, err = getClipboardImageViaAPI()
		if err != nil {
			log.Printf("WinAPI image read failed: %v", err)
		}
	}

	// --- НОВАЯ ЛОГИКА ---
	// Если байтов нет, проверяем, может в буфере путь к файлу (текст)
	if len(imageBytes) == 0 {
		text, err := copySelection()
		if err == nil {
			cleanedPath := strings.Trim(strings.TrimSpace(text), "\"")
			if isImageFile(cleanedPath) {
				log.Printf("Загружаем изображение по пути из буфера: %s", cleanedPath)
				fileBytes, err := os.ReadFile(cleanedPath)
				if err == nil {
					imageBytes = fileBytes
				}
			}
		}
	}
	// --------------------

	if len(imageBytes) == 0 {
		return "", fmt.Errorf("буфер не содержит изображения или пути к нему")
	}

	return processImage(imageBytes, action)
}

func handleFilesAction(action Action) (string, error) {
	files, err := getClipboardFiles()
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("нет файлов в буфере")
	}
	return processFiles(files, action)
}

func processText(text string, action Action) (string, error) {
	log.Printf("Символов: %d", len(text))
	basePrompt := strings.Replace(action.Prompt, "{{.clipboard}}", text, 1)
	finalPrompt, pErr := preparePrompt(basePrompt, action.Name)
	if pErr != nil {
		return "", nil
	}
	return runLLM(finalPrompt, nil, action.MistralArgs)
}

func processImage(imageBytes []byte, action Action) (string, error) {
	img, _, decodeErr := image.Decode(bytes.NewReader(imageBytes))
	if decodeErr != nil {
		return "", fmt.Errorf("ошибка декодирования: %v", decodeErr)
	}
	tempFile, err := saveImageToTemp(img)
	if err != nil {
		return "", err
	}
	defer os.Remove(tempFile)
	finalPrompt, pErr := preparePrompt(action.Prompt, action.Name)
	if pErr != nil {
		return "", nil
	}
	return runLLM(finalPrompt, []string{tempFile}, action.MistralArgs)
}

func processFiles(files []string, action Action) (string, error) {
	var finalFileList, audioFiles []string
	var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".webp": true}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".ogg" || ext == ".flac" || ext == ".opus" {
			audioFiles = append(audioFiles, f)
		} else if imageExtensions[ext] {
			finalFileList = append(finalFileList, f)
		} else {
			finalFileList = append(finalFileList, f)
		}
	}
	if len(audioFiles) > 0 {
		log.Printf("Транскрипция аудио (%d)...", len(audioFiles))
		for _, audioPath := range audioFiles {
			prompt := "Transcribe this audio file to text verbatim. Output ONLY the text."
			transText, err := runLLM(prompt, []string{audioPath}, "")
			if err != nil {
				transText = fmt.Sprintf("[Ошибка аудио %s]", filepath.Base(audioPath))
			} else if strings.TrimSpace(transText) == "" {
				transText = "[Пустая транскрипция]"
			}
			tempTxt, err := saveTextToTemp(transText)
			if err != nil {
				return "", err
			}
			defer os.Remove(tempTxt)
			finalFileList = append(finalFileList, tempTxt)
		}
	}
	finalPrompt, err := preparePrompt(action.Prompt, action.Name)
	if err != nil {
		return "", nil
	}
	return runLLM(finalPrompt, finalFileList, action.MistralArgs)
}

// ==========================================================
// HELPERS
// ==========================================================

// Проверяет, является ли строка путем к существующему изображению
// isImageFile проверяет расширение и существование файла
func isImageFile(path string) bool {
	// Очищаем путь от кавычек
	path = strings.Trim(strings.TrimSpace(path), "\"")

	// Сначала проверяем длину и запрещенные символы, чтобы не мучить диск
	// В Windows пути редко длиннее 260 символов, а текст может быть огромным
	if len(path) > 260 || strings.ContainsAny(path, "<>\"|?*") {
		return false
	}

	info, err := os.Stat(path)
	// ИСПРАВЛЕНИЕ: Если есть ЛЮБАЯ ошибка (не найден, кривое имя, нет прав),
	// считаем, что это не файл.
	if err != nil {
		return false
	}

	if info.IsDir() {
		return false
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".bmp", ".webp":
		return true
	}
	return false
}

func preparePrompt(promptTmpl string, actionName string) (string, error) {
	if strings.Contains(promptTmpl, "{{ask_user}}") {
		defaultInput := inputHistory[actionName]
		windowTitle := fmt.Sprintf("ClipGen: %s", actionName)

		go func() {
			time.Sleep(200 * time.Millisecond)
			titlePtr, _ := syscall.UTF16PtrFromString(windowTitle)
			hwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(titlePtr)))
			if hwnd != 0 {
				setForegroundWindow.Call(hwnd)
			}
		}()

		userInput, err := zenity.Entry("Что нужно сделать с этими данными?",
			zenity.Title(windowTitle),
			zenity.EntryText(defaultInput))

		if err != nil {
			return "", err
		}
		if userInput == "" {
			return "", fmt.Errorf("ввод пустой")
		}

		inputHistory[actionName] = userInput

		return strings.Replace(promptTmpl, "{{ask_user}}", userInput, 1), nil
	}
	return promptTmpl, nil
}

func runLLM(prompt string, filePaths []string, args string) (string, error) {
	var argList []string

	// Используем системный промпт из конфига
	if config.SystemPrompt != "" {
		argList = append(argList, "-s", config.SystemPrompt)
	}

	if args != "" {
		argList = append(argList, strings.Fields(args)...)
	}

	for _, f := range filePaths {
		argList = append(argList, "-f", f)
	}

	// Используем путь к утилите из конфига
	log.Printf("LLM CMD: %s %v", config.LLMPath, argList)

	cmd := exec.Command(config.LLMPath, argList...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(prompt)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	start := time.Now()

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("LLM error: %v | stderr: %s", err, stderr.String())
	}

	log.Printf("LLM выполнено за %v. Размер ответа: %d", time.Since(start), out.Len())
	return strings.TrimSpace(out.String()), nil
}

// ИЗМЕНЕНИЕ: Функция теперь блокирует поток и использует tryOpenClipboard
func getClipboardImageViaAPI() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	if r == 0 {
		return nil, fmt.Errorf("CF_DIB не доступен")
	}

	// Используем безопасное открытие
	if success, err := tryOpenClipboard(); !success {
		return nil, err
	}
	defer closeClipboard.Call()

	hMem, _, _ := getClipboardData.Call(CF_DIB)
	if hMem == 0 {
		return nil, fmt.Errorf("GetClipboardData failed")
	}
	pData, _, _ := globalLock.Call(hMem)
	if pData == 0 {
		return nil, fmt.Errorf("GlobalLock failed")
	}
	defer globalUnlock.Call(hMem)
	memSize, _, _ := globalSize.Call(hMem)
	dibData := make([]byte, memSize)
	copy(dibData, (*[1 << 30]byte)(unsafe.Pointer(pData))[:memSize])
	infoHeaderSize := binary.LittleEndian.Uint32(dibData[0:4])
	header := BITMAPFILEHEADER{
		BfType:    0x4D42,
		BfSize:    uint32(14 + memSize),
		BfOffBits: uint32(14 + infoHeaderSize),
	}
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &header)
	buf.Write(dibData)
	return buf.Bytes(), nil
}

// ИЗМЕНЕНИЕ: Функция теперь блокирует поток и использует tryOpenClipboard
func getClipboardFiles() ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Используем безопасное открытие
	if success, err := tryOpenClipboard(); !success {
		return nil, err
	}
	defer closeClipboard.Call()

	hDrop, _, _ := getClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, nil
	}
	cnt, _, _ := dragQueryFile.Call(hDrop, 0xFFFFFFFF, 0, 0)
	if cnt == 0 {
		return nil, nil
	}
	var files []string
	for i := uintptr(0); i < cnt; i++ {
		lenRet, _, _ := dragQueryFile.Call(hDrop, i, 0, 0)
		if lenRet == 0 {
			continue
		}
		buf := make([]uint16, lenRet+1)
		dragQueryFile.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), lenRet+1)
		files = append(files, syscall.UTF16ToString(buf))
	}
	return files, nil
}

// ИЗМЕНЕНИЕ: Добавлены стратегические паузы, чтобы исправить Race Condition
func copySelection() (string, error) {
	originalContent := clipboard.Read(clipboard.FmtText)
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return "", err
	}
	kb.SetKeys(keybd_event.VK_C)
	kb.HasCTRL(true)
	kb.Launching()

	// ВАЖНО: Пауза, чтобы дать системе записать данные в буфер
	// Без этого вызов clipboard.Read блокирует буфер ДО того, как приложение туда напишет
	time.Sleep(150 * time.Millisecond)

	startTime := time.Now()
	for time.Since(startTime) < time.Second*1 {
		newContent := clipboard.Read(clipboard.FmtText)
		if len(newContent) > 0 && !bytes.Equal(newContent, originalContent) {
			return string(newContent), nil
		}
		// Увеличили паузу внутри цикла для стабильности
		time.Sleep(100 * time.Millisecond)
	}
	if len(originalContent) > 0 {
		return string(originalContent), nil
	}
	return "", fmt.Errorf("буфер не изменился")
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

func showInEditor(content string) error {
	tempFile, err := saveTextToTemp(content)
	if err != nil {
		return err
	}

	// Используем настроенный редактор
	log.Printf("Открываем редактор: %s %s", config.EditorPath, tempFile)
	cmd := exec.Command(config.EditorPath, tempFile)
	return cmd.Start()
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
	// BOM для Windows приложений (Блокнота), но нормальные редакторы типа MarkText его игнорируют или едят нормально
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

		defaultConfig := `
# --- СИСТЕМНЫЕ НАСТРОЙКИ ---

# Программа для открытия результатов (Блокнот, MarkText, VS Code)
# Если используешь MarkText, укажи полный путь, например: "C:\\Program Files\\MarkText\\MarkText.exe"
editor_path: "notepad.exe"

# Утилита для запуска нейросети (mistral.exe, llama3.exe и т.д.)
llm_path: "mistral.exe"

# Базовая инструкция для нейросети
# Если используешь MarkText, разреши Markdown. Если Блокнот - запрети.
system_prompt: |
  Вы работаете в Windows-утилите ClipGen-m, которая помогает отправлять запросы к ИИ-моделям через буфер обмена.
  Используйте только обычный текст, маркдаун не разрешен.
  Используйте табуляцию для разделения столбцов в таблицах, это необходимо для возможности вставки текста в Excel.
  Не добавляйте вступительные заполнители.

actions:
  # --- ЛОКАЛЬНЫЕ ДЕЙСТВИЯ (без ИИ) ---
  - name: "Сменить раскладку (Punto)"
    hotkey: "Pause"
    input_type: "layout_switch"
    output_mode: "replace"

  # --- ТЕКСТ (с ИИ) ---
  - name: "Исправить текст (F1)"
    hotkey: "Ctrl+F1"
    prompt: |
      Исправь грамматику, пунктуацию и стиль. Верни ТОЛЬКО исправленный текст. Не используй маркдаун в ответе, только простой текст.
      Текст: {{.clipboard}}
    mistral_args: "-t 0.1"
    input_type: "text"
    output_mode: "replace"

  - name: "Выполнить просьбу (F2)"
    hotkey: "Ctrl+F2"
    prompt: "Выполни просьбу:\n{{.clipboard}}"
    input_type: "text"
    output_mode: "replace"

	- name: "Перевести и показать (F3)"
    hotkey: "Ctrl+F3"
    prompt: "Переведи на русский. Верни только перевод. Не используй маркдаун в ответе, только простой текст.\n{{.clipboard}}"
    input_type: "auto"
    output_mode: "editor"

  - name: "Перевести и заменить (F4)"
    hotkey: "Ctrl+F4"
    prompt: "Переведи на русский (если уже рус - на англ). Верни только перевод. Не используй маркдаун в ответе, только простой текст.\n{{.clipboard}}"
    input_type: "text"
    output_mode: "replace"

  - name: "Объяснить (F5)"
    hotkey: "Ctrl+F5"
    prompt: "Объясни это простыми словами. Не используй маркдаун в ответе, только простой текст.\n{{.clipboard}}"
    input_type: "auto"
    output_mode: "editor"

  # --- ИЗОБРАЖЕНИЯ (конкретная задача с ИИ) ---
  - name: "OCR / Текст с картинки (F6)"
    hotkey: "Ctrl+F6"
    prompt: "Извлеки весь текст с изображения. Верни только текст. То есть выполни работу OCR. Не используй маркдаун в ответе, только простой текст."
    input_type: "auto"
    output_mode: "editor"

  # --- ФАЙЛЫ (конкретная задача с ИИ) ---
  - name: "Сделать саммари из файлов (F7)"
    hotkey: "Ctrl+F7"
    prompt: |
      Сделай краткое саммари по всем предоставленным файлам. Не используй маркдаун в ответе, только простой текст.
      Структурируй ответ, используй списки.
    input_type: "files"
    output_mode: "editor"

  # --- УНИВЕРСАЛЬНЫЙ РЕЖИМ (с окном ввода, с ИИ) ---
  - name: "Умный анализ (F8)"
    hotkey: "Ctrl+F8"
    prompt: |
      Проанализируй данные из буфера обмена. Не используй маркдаун в ответе, только простой текст.
      
      ДАННЫЕ:
      {{.clipboard}}
      
      ЗАДАЧА ПОЛЬЗОВАТЕЛЯ:
      {{ask_user}}
    input_type: "auto"
    output_mode: "editor"`

		os.WriteFile(path, []byte(strings.TrimSpace(defaultConfig)), 0644)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, &config)
}

func loadIcons() {
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		iconNormal, _ = os.ReadFile(filepath.Join(dir, "icon.ico"))
		iconWait, _ = os.ReadFile(filepath.Join(dir, "icon_wait.ico"))
	}
	if len(iconNormal) == 0 {
		iconNormal, _ = os.ReadFile("icon.ico")
		iconWait, _ = os.ReadFile("icon_wait.ico")
	}
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
		"F1":    hotkey.KeyF1,
		"F2":    hotkey.KeyF2,
		"F3":    hotkey.KeyF3,
		"F4":    hotkey.KeyF4,
		"F5":    hotkey.KeyF5,
		"F6":    hotkey.KeyF6,
		"F7":    hotkey.KeyF7,
		"F8":    hotkey.KeyF8,
		"F9":    hotkey.KeyF9,
		"F10":   hotkey.KeyF10,
		"F11":   hotkey.KeyF11,
		"F12":   hotkey.KeyF12,
		"PAUSE": hotkey.Key(19),
	}
	for i, part := range parts {
		part = strings.TrimSpace(strings.ToUpper(part))
		if i == len(parts)-1 {
			if k, ok := keyMap[part]; ok {
				key = k
			} else {
				return nil, 0, fmt.Errorf("неизвестная клавиша: %s", part)
			}
		} else {
			switch part {
			case "CTRL":
				mods = append(mods, hotkey.ModCtrl)
			case "SHIFT":
				mods = append(mods, hotkey.ModShift)
			case "ALT":
				mods = append(mods, hotkey.ModAlt)
			case "WIN":
				mods = append(mods, hotkey.ModWin)
			default:
				return nil, 0, fmt.Errorf("неизвестный модификатор: %s", part)
			}
		}
	}
	if key == 0 {
		return nil, 0, fmt.Errorf("клавиша не указана в хоткее")
	}
	return mods, key, nil
}
