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
// CONSTANTS & GLOBALS
// ==========================================================

// СИСТЕМНЫЙ ПРОМПТ
const SystemPromptText = `You are a helpful assistant.
IMPORTANT OUTPUT RULES:
1. Output ONLY PLAIN TEXT. Do NOT use Markdown formatting (no **bold**, no headers, no code blocks).
2. If the result involves a table, use TABS as separators between columns so it can be pasted directly into Excel.
3. Do not add introductory or concluding chatter, just the result.`

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

// ==========================================================
// CONFIG STRUCTURES
// ==========================================================
type Config struct {
	Actions []Action `yaml:"actions"`
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
		logFile, _ = os.OpenFile("clipgen.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	}
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
		// Ошибка тоже теперь выводится красиво через диалог, если zenity доступен
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
	case "notepad":
		if err := showInNotepad(resultText); err != nil {
			log.Printf("Ошибка открытия блокнота: %v", err)
		}
	default:
		clipboard.Write(clipboard.FmtText, []byte(resultText))
		time.Sleep(100 * time.Millisecond)
		paste()
	}
}

// ==========================================================
// PUNTO SWITCHER LOGIC
// ==========================================================

var (
	engToRus = map[rune]rune{
		'q': 'й', 'w': 'ц', 'e': 'у', 'r': 'к', 't': 'е', 'y': 'н', 'u': 'г', 'i': 'ш', 'o': 'щ', 'p': 'з', '[': 'х', ']': 'ъ',
		'a': 'ф', 's': 'ы', 'd': 'в', 'f': 'а', 'g': 'п', 'h': 'р', 'j': 'о', 'k': 'л', 'l': 'д', ';': 'ж', '\'': 'э',
		'z': 'я', 'x': 'ч', 'c': 'с', 'v': 'м', 'b': 'и', 'n': 'т', 'm': 'ь', ',': 'б', '.': 'ю', '`': 'ё',
		'Q': 'Й', 'W': 'Ц', 'E': 'У', 'R': 'К', 'T': 'Е', 'Y': 'Н', 'U': 'Г', 'I': 'Ш', 'O': 'Щ', 'P': 'З', '{': 'Х', '}': 'Ъ',
		'A': 'Ф', 'S': 'Ы', 'D': 'В', 'F': 'А', 'G': 'П', 'H': 'Р', 'J': 'О', 'K': 'Л', 'L': 'Д', ':': 'Ж', '"': 'Э',
		'Z': 'Я', 'X': 'Ч', 'C': 'С', 'V': 'М', 'B': 'И', 'N': 'Т', 'M': 'Ь', '<': 'Б', '>': 'Ю', '~': 'Ё',
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
	files, _ := getClipboardFiles()
	if len(files) > 0 {
		return processFiles(files, action)
	}
	clipboardText, err := copySelection()
	if err == nil && len(clipboardText) > 0 {
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
	if len(imageBytes) == 0 {
		var err error
		imageBytes, err = getClipboardImageViaAPI()
		if err != nil {
			return "", fmt.Errorf("буфер не содержит изображения: %v", err)
		}
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
	return runMistral(finalPrompt, nil, action.MistralArgs)
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
	return runMistral(finalPrompt, []string{tempFile}, action.MistralArgs)
}

func processFiles(files []string, action Action) (string, error) {
	var finalFileList, audioFiles []string
	var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".webp": true}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".ogg" {
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
			transText, err := runMistral(prompt, []string{audioPath}, "")
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
	return runMistral(finalPrompt, finalFileList, action.MistralArgs)
}

// ==========================================================
// HELPERS
// ==========================================================

func preparePrompt(promptTmpl string, actionName string) (string, error) {
	if strings.Contains(promptTmpl, "{{ask_user}}") {
		defaultInput := inputHistory[actionName]
		windowTitle := fmt.Sprintf("ClipGen: %s", actionName)

		// --- ХАК ДЛЯ ВЫВОДА ОКНА НА ПЕРЕДНИЙ ПЛАН ---
		go func() {
			// Даем окну время на отрисовку (100-200мс обычно достаточно)
			time.Sleep(200 * time.Millisecond)

			// Преобразуем строку заголовка для Windows API
			titlePtr, _ := syscall.UTF16PtrFromString(windowTitle)

			// Ищем окно по заголовку (класс окна = 0/nil)
			hwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(titlePtr)))

			if hwnd != 0 {
				// Если нашли - тащим наверх
				setForegroundWindow.Call(hwnd)
			} else {
				log.Println("Не удалось найти окно для фокусировки (возможно, открылось слишком медленно)")
			}
		}()
		// ---------------------------------------------

		userInput, err := zenity.Entry("Что нужно сделать с этими данными?",
			zenity.Title(windowTitle), // Важно: заголовок должен совпадать с тем, что мы ищем выше
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

// showInputBox - БОЛЬШЕ НЕ НУЖЕН, заменен на zenity.Entry внутри preparePrompt.
// Но если где-то остался вызов, можно удалить. В коде выше он удален.

func runMistral(prompt string, filePaths []string, args string) (string, error) {
	var argList []string

	argList = append(argList, "-s", SystemPromptText)

	if args != "" {
		argList = append(argList, strings.Fields(args)...)
	}

	for _, f := range filePaths {
		argList = append(argList, "-f", f)
	}

	log.Printf("Mistral CMD: mistral.exe %v", argList)

	cmd := exec.Command("mistral.exe", argList...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(prompt)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	start := time.Now()

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mistral error: %v | stderr: %s", err, stderr.String())
	}

	log.Printf("Mistral выполнено за %v. Размер ответа: %d", time.Since(start), out.Len())
	return strings.TrimSpace(out.String()), nil
}

func getClipboardImageViaAPI() ([]byte, error) {
	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	if r == 0 {
		return nil, fmt.Errorf("CF_DIB не доступен")
	}
	if r, _, _ := openClipboard.Call(0); r == 0 {
		return nil, fmt.Errorf("OpenClipboard failed")
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

func getClipboardFiles() ([]string, error) {
	r, _, _ := openClipboard.Call(0)
	if r == 0 {
		return nil, fmt.Errorf("OpenClipboard failed")
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

func showInNotepad(content string) error {
	tempFile, err := saveTextToTemp(content)
	if err != nil {
		return err
	}
	cmd := exec.Command("notepad.exe", tempFile)
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

		defaultConfig := `actions:
  # --- ЛОКАЛЬНЫЕ ДЕЙСТВИЯ (без ИИ) ---
  - name: "Сменить раскладку (Punto)"
    hotkey: "Pause"
    input_type: "layout_switch"
    output_mode: "replace"

  # --- ТЕКСТ (с ИИ) ---
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
    input_type: "text"
    output_mode: "notepad"

  - name: "Объяснить (F6)"
    hotkey: "Ctrl+F6"
    prompt: "Объясни это простыми словами:\n{{.clipboard}}"
    input_type: "text"
    output_mode: "notepad"

  - name: "Выполнить просьбу (F8)"
    hotkey: "Ctrl+F8"
    prompt: "Выполни просьбу:\n{{.clipboard}}"
    input_type: "text"
    output_mode: "replace"

  # --- ИЗОБРАЖЕНИЯ (конкретная задача с ИИ) ---
  - name: "OCR / Текст с картинки (F9)"
    hotkey: "Ctrl+F9"
    prompt: "Извлеки весь текст с изображения. Верни только текст. То есть выполни работу OCR."
    input_type: "image"
    output_mode: "notepad"

  # --- ФАЙЛЫ (конкретная задача с ИИ) ---
  - name: "Сделать саммари из файлов (F10)"
    hotkey: "Ctrl+F10"
    prompt: |
      Ты аналитик. Сделай краткое саммари по всем предоставленным файлам.
      Структурируй ответ.
    input_type: "files"
    output_mode: "notepad"

  # --- УНИВЕРСАЛЬНЫЙ РЕЖИМ (с окном ввода, с ИИ) ---
  - name: "Умный анализ (F12)"
    hotkey: "Ctrl+F12"
    prompt: |
      Ты — ИИ-ассистент. Проанализируй данные из буфера обмена.
      
      ДАННЫЕ:
      {{.clipboard}}
      
      ЗАДАЧА ПОЛЬЗОВАТЕЛЯ:
      {{ask_user}}
    input_type: "auto"
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
