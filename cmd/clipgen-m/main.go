package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

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

	enumWindows              = user32.NewProc("EnumWindows")
	getWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	sendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")

	// Новые функции для управления видимостью
	showWindow      = user32.NewProc("ShowWindow")
	isWindowVisible = user32.NewProc("IsWindowVisible")
)

const (
	CF_HDROP         = 15
	CF_DIB           = 8
	WM_CLOSE         = 0x0010
	SMTO_ABORTIFHUNG = 0x0002
	MOD_NOREPEAT     = 0x4000

	// Константы для ShowWindow
	SW_HIDE    = 0
	SW_RESTORE = 9
)

type BITMAPFILEHEADER struct {
	BfType      uint16
	BfSize      uint32
	BfReserved1 uint16
	BfReserved2 uint16
	BfOffBits   uint32
}

func tryOpenClipboard() (bool, error) {
	for i := 0; i < 20; i++ {
		r, _, _ := openClipboard.Call(0)
		if r != 0 {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, fmt.Errorf("не удалось открыть буфер обмена (занят другим приложением)")
}

// ==========================================================
// CONFIG STRUCTURES
// ==========================================================

type Config struct {
	EditorPath      string   `yaml:"editor_path"`
	LLMPath         string   `yaml:"llm_path"`
	SystemPrompt    string   `yaml:"system_prompt"`
	AppToggleHotkey string   `yaml:"app_toggle_hotkey"`
	ChatUIPath      string   `yaml:"chatui_path"`
	ChatUIHotkey    string   `yaml:"chatui_hotkey"`
	Actions         []Action `yaml:"actions"`
}

type Action struct {
	Name        string `yaml:"name"`
	Hotkey      string `yaml:"hotkey"`
	Prompt      string `yaml:"prompt,omitempty"`
	MistralArgs string `yaml:"mistral_args,omitempty"`
	InputType   string `yaml:"input_type"`
	OutputMode  string `yaml:"output_mode,omitempty"`
}

type HotkeyControl struct {
	hk   *hotkey.Hotkey
	quit chan struct{}
}

var (
	iconNormal   []byte
	iconWait     []byte
	iconStop     []byte
	config       Config
	logFile      *os.File
	inputHistory = make(map[string]string)

	hotkeyMutex   sync.Mutex
	activeHotkeys []HotkeyControl
	actionChan    = make(chan Action, 10)

	chatUIProcess *os.Process
	chatUIMutex   sync.Mutex
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
	go actionProcessor()
	setupChatUIHotkey()
	enableHotkeys()
}

func killChatUI() {
	chatUIMutex.Lock()
	defer chatUIMutex.Unlock()

	if chatUIProcess != nil {
		log.Println("Принудительное завершение процесса Chat UI...")
		err := chatUIProcess.Kill()
		if err != nil {
			log.Printf("Ошибка при убийстве Chat UI: %v", err)
		}
		// Обнуляем переменную сразу, чтобы не пытаться убить дважды
		chatUIProcess = nil
	}
}

func onExit() {
	// Гарантированно убиваем чат перед выходом
	killChatUI()

	disableHotkeys()
	log.Println("Завершение работы.")
}

// ==========================================================
// LOGGING
// ==========================================================

// syncWriter wraps an io.Writer and adds sync functionality
type syncWriter struct {
	file *os.File
}

func (sw *syncWriter) Write(p []byte) (n int, err error) {
	n, err = sw.file.Write(p)
	if err == nil {
		// Attempt to sync the file to ensure data is written to disk
		sw.file.Sync() // This forces OS buffer to be written to disk
	}
	return n, err
}

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

	// Use syncWriter to ensure logs are flushed to disk immediately
	syncFile := &syncWriter{file: logFile}
	log.SetOutput(syncFile)

	// Ensure the initial log message is written immediately
	log.SetFlags(log.LstdFlags | log.Lshortfile) // Add more detailed logging
}

func openLogFile() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "clipgen-m", "clipgen.log")
	cmd := exec.Command(config.EditorPath, logPath)
	if err := cmd.Start(); err != nil {
		exec.Command("notepad.exe", logPath).Start()
	}
}

func openConfigFile() {
	openFileInConfigDir("config.yaml")
}

func openFileInConfigDir(filename string) {
	configDir, _ := os.UserConfigDir()
	filePath := filepath.Join(configDir, "clipgen-m", filename)

	// Если файла нет — создаем пустой, чтобы редактор не ругался (опционально)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		os.WriteFile(filePath, []byte(""), 0644)
	}

	cmd := exec.Command(config.EditorPath, filePath)
	if err := cmd.Start(); err != nil {
		exec.Command("notepad.exe", filePath).Start()
	}
}

// ==========================================================
// ACTION HANDLER
// ==========================================================

func actionProcessor() {
	for action := range actionChan {
		go handleAction(action)
	}
}

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
	case "notepad", "editor":
		if err := showInEditor(resultText); err != nil {
			log.Printf("Ошибка открытия редактора: %v", err)
		}
	default:
		clipboard.Write(clipboard.FmtText, []byte(resultText))
		time.Sleep(200 * time.Millisecond)
		paste()
	}
}

// ==========================================================
// PUNTO SWITCHER LOGIC
// ==========================================================

var (
	engToRus = map[rune]rune{
		'`': 'ё', 'q': 'й', 'w': 'ц', 'e': 'у', 'r': 'к', 't': 'е', 'y': 'н', 'u': 'г', 'i': 'ш', 'o': 'щ', 'p': 'з', '[': 'х', ']': 'ъ',
		'a': 'ф', 's': 'ы', 'd': 'в', 'f': 'а', 'g': 'п', 'h': 'р', 'j': 'о', 'k': 'л', 'l': 'д', ';': 'ж', '\'': 'э',
		'z': 'я', 'x': 'ч', 'c': 'с', 'v': 'м', 'b': 'и', 'n': 'т', 'm': 'ь', ',': 'б', '.': 'ю', '/': '.',
		'~': 'Ё', 'Q': 'Й', 'W': 'Ц', 'E': 'У', 'R': 'К', 'T': 'Е', 'Y': 'Н', 'U': 'Г', 'I': 'Ш', 'O': 'Щ', 'P': 'З', '{': 'Х', '}': 'Ъ',
		'A': 'Ф', 'S': 'Ы', 'D': 'В', 'F': 'А', 'G': 'П', 'H': 'Р', 'J': 'О', 'K': 'Л', 'L': 'Д', ':': 'Ж', '"': 'Э',
		'Z': 'Я', 'X': 'Ч', 'C': 'С', 'V': 'М', 'B': 'И', 'N': 'Т', 'M': 'Ь', '<': 'Б', '>': 'Ю', '?': ',',
		'@': '"', '#': '№', '$': ';', '^': ':', '&': '?', '|': '/',
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
	} else {
		conversionMap = engToRus
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
		cleanedPath := strings.Trim(strings.TrimSpace(clipboardText), "\"")
		if isImageFile(cleanedPath) {
			log.Printf("В буфере найден путь к картинке: %s", cleanedPath)
			return processFiles([]string{cleanedPath}, action)
		}
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
			log.Printf("WinAPI image read failed: %v", err)
		}
	}
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
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("буфер не содержит изображения или пути к нему")
	}
	return processImage(imageBytes, action)
}

func handleFilesAction(action Action) (string, error) {
	files, err := getClipboardFiles()
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("нет файлов в буфере")
	}
	return processFiles(files, action)
}

func processText(text string, action Action) (string, error) {
	log.Printf("Символов: %d", len(text))
	basePrompt := strings.Replace(action.Prompt, "{{.clipboard}}", text, 1)
	finalPrompt, pErr := preparePrompt(basePrompt, action.Name)
	if pErr != nil {
		return "", nil // User canceled
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
	var finalFileList []string
	var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".webp": true}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if imageExtensions[ext] {
			finalFileList = append(finalFileList, f)
		} else {
			finalFileList = append(finalFileList, f)
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

func isImageFile(path string) bool {
	path = strings.Trim(strings.TrimSpace(path), "\"")
	if len(path) > 260 || strings.ContainsAny(path, "<>\"|?*") {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
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
	if config.SystemPrompt != "" {
		argList = append(argList, "-s", config.SystemPrompt)
	}
	if args != "" {
		argList = append(argList, strings.Fields(args)...)
	}
	for _, f := range filePaths {
		argList = append(argList, "-f", f)
	}
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

func getClipboardImageViaAPI() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	if r == 0 {
		return nil, fmt.Errorf("CF_DIB не доступен")
	}
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
	if memSize > 0 {
		srcSlice := (*[1 << 30]byte)(unsafe.Pointer(pData))[:memSize:memSize]
		copy(dibData, srcSlice)
	}
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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
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

func copySelection() (string, error) {
	originalContent := clipboard.Read(clipboard.FmtText)
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return "", err
	}
	kb.SetKeys(keybd_event.VK_C)
	kb.HasCTRL(true)
	kb.Launching()
	time.Sleep(150 * time.Millisecond)
	startTime := time.Now()
	for time.Since(startTime) < time.Second*1 {
		newContent := clipboard.Read(clipboard.FmtText)
		if len(newContent) > 0 && !bytes.Equal(newContent, originalContent) {
			return string(newContent), nil
		}
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
	tempFile.Write([]byte{0xEF, 0xBB, 0xBF})
	tempFile.WriteString(content)
	tempFile.Close()
	return tempFile.Name(), nil
}

// ==========================================================
// TRAY & CONFIG & HOTKEYS
// ==========================================================

func setupTray() {
	systray.SetIcon(iconNormal)
	systray.SetTitle("ClipGen-m")

	mToggle := systray.AddMenuItemCheckbox("Активен", "Включить/Выключить обработку клавиш", true)

	systray.AddSeparator()
	mConfig := systray.AddMenuItem("Настройки (main)", "Редактировать config.yaml")
	mMistralConf := systray.AddMenuItem("Mistral Config", "Редактировать mistral.conf")
	mTavilyConf := systray.AddMenuItem("Tavily Config", "Редактировать tavily.conf")

	systray.AddSeparator()
	mMistralLog := systray.AddMenuItem("Mistral Log", "Просмотр mistral_err.log")
	mLog := systray.AddMenuItem("ClipGen Log", "Посмотреть ошибки программы")

	systray.AddSeparator()
	mReload := systray.AddMenuItem("Перезагрузка", "Применить конфиг")
	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")

	toggleHotkeyCh, err := setupToggleHotkey()
	if err != nil {
		log.Printf("WARNING: %v", err)
	}

	go func() {
		for {
			select {
			// Обработка клика по галочке "Активен"
			case <-mToggle.ClickedCh:
				if mToggle.Checked() {
					mToggle.Uncheck()
					log.Println("Приложение деактивировано пользователем (Menu).")
					disableHotkeys()
				} else {
					mToggle.Check()
					log.Println("Приложение активировано пользователем (Menu).")
					enableHotkeys()
				}
			// Обработка хоткея включения/выключения
			case <-toggleHotkeyCh:
				if mToggle.Checked() {
					mToggle.Uncheck()
					log.Println("Приложение деактивировано пользователем (Hotkey).")
					disableHotkeys()
				} else {
					mToggle.Check()
					log.Println("Приложение активировано пользователем (Hotkey).")
					enableHotkeys()
				}

			// Конфиги
			case <-mConfig.ClickedCh:
				openConfigFile()
			case <-mMistralConf.ClickedCh:
				openFileInConfigDir("mistral.conf")
			case <-mTavilyConf.ClickedCh:
				openFileInConfigDir("tavily.conf")

			// Логи
			case <-mMistralLog.ClickedCh:
				openFileInConfigDir("mistral_err.log")
			case <-mLog.ClickedCh:
				openLogFile()

			// Системные
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

func setupToggleHotkey() (chan struct{}, error) {
	ch := make(chan struct{})
	if config.AppToggleHotkey == "" {
		return ch, nil
	}
	mods, key, err := parseHotkey(config.AppToggleHotkey)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга ToggleHotkey: %v", err)
	}
	hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(mods)}, key)
	if err := hk.Register(); err != nil {
		return nil, fmt.Errorf("не удалось зарегистрировать ToggleHotkey: %v", err)
	}
	go func() {
		for range hk.Keydown() {
			ch <- struct{}{}
		}
	}()
	log.Printf("Зарегистрирован переключатель: %s", config.AppToggleHotkey)
	return ch, nil
}

func findWindowByPID(pid uint32) (syscall.Handle, bool) {
	var hwnd syscall.Handle
	var found bool

	cb := syscall.NewCallback(func(h syscall.Handle, p uintptr) uintptr {
		var processID uint32
		getWindowThreadProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&processID)))
		if processID == pid {
			hwnd = h
			found = true
			return 0
		}
		return 1
	})

	enumWindows.Call(cb, 0)
	return hwnd, found
}

func setupChatUIHotkey() {
	if config.ChatUIHotkey == "" {
		return
	}
	log.Printf("Attempting to register chat hotkey: %s", config.ChatUIHotkey)
	mods, key, err := parseHotkey(config.ChatUIHotkey)
	if err != nil {
		log.Printf("WARNING: error parsing ChatUIHotkey: %v", err)
		return
	}
	log.Printf("Parsing successful. Modifiers: %d, Key: %d", mods, key)
	hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(mods)}, key)
	if err := hk.Register(); err != nil {
		log.Printf("WARNING: failed to register ChatUIHotkey: %v", err)
		log.Printf("POSSIBLY THIS HOTKEY IS USED BY ANOTHER APPLICATION: %s", config.ChatUIHotkey)
		return
	}
	go func() {
		for range hk.Keydown() {
			log.Printf("Chat hotkey triggered: %s", config.ChatUIHotkey)
			toggleChatUI()
		}
	}()
	log.Printf("SUCCESSFULLY registered chat hotkey: %s", config.ChatUIHotkey)
}

func toggleChatUI() {
	chatUIMutex.Lock()
	defer chatUIMutex.Unlock()

	// 1. Поиск окна по заголовку
	windowTitle := "ClipGen-m ChatUI" // Должен совпадать с Title в MainWindow chatui
	titlePtr, _ := syscall.UTF16PtrFromString(windowTitle)
	hwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(titlePtr)))

	// 2. Если окно найдено — закрываем его принудительно
	if hwnd != 0 {
		log.Println("[ChatUI] Окно найдено, закрываем процесс.")
		// Отправляем сообщение WM_CLOSE для корректного закрытия
		_, _, _ = sendMessageTimeoutW.Call(
			hwnd,
			WM_CLOSE,
			0,
			0,
			SMTO_ABORTIFHUNG,
			2000, // 2 секунды таймаут
		)

		// Ждем завершения процесса и очищаем переменную
		if chatUIProcess != nil {
			go func(p *os.Process) {
				p.Wait()
				chatUIMutex.Lock()
				if chatUIProcess != nil && chatUIProcess.Pid == p.Pid {
					log.Println("[Наблюдатель] Процесс ChatUI завершился.")
					chatUIProcess = nil
				}
				chatUIMutex.Unlock()
			}(chatUIProcess)
		}
		return
	}

	// 3. Если окно не найдено, проверяем состояние процесса
	if chatUIProcess != nil {
		// Если процесс жив, но окно не найдено, возможно оно еще создается или уже завершается
		if err := chatUIProcess.Signal(syscall.Signal(0)); err == nil {
			log.Println("[ChatUI] Процесс запущен, но окно еще не найдено (инициализация?)")
			return
		}
		// Процесс мертв
		chatUIProcess = nil
	}

	log.Println("[ChatUI] Окно не найдено. Запускаем процесс...")

	executablePath := config.ChatUIPath
	if !filepath.IsAbs(config.ChatUIPath) {
		if resolvedPath, err := exec.LookPath(config.ChatUIPath); err == nil {
			executablePath = resolvedPath
		}
	}

	cmd := exec.Command(executablePath)

	if err := cmd.Start(); err != nil {
		log.Printf(" -> Ошибка запуска: %v", err)
		return
	}

	chatUIProcess = cmd.Process
	log.Printf(" -> Процесс успешно запущен (PID: %d)", chatUIProcess.Pid)

	// Наблюдатель
	go func(p *os.Process) {
		p.Wait()
		chatUIMutex.Lock()
		if chatUIProcess != nil && chatUIProcess.Pid == p.Pid {
			log.Println("[Наблюдатель] Процесс ChatUI завершился.")
			chatUIProcess = nil
		}
		chatUIMutex.Unlock()
	}(cmd.Process)
}

func restartApp() {
	log.Println("Подготовка к перезагрузке...")

	// Сначала убиваем чат, чтобы он не остался висеть "сиротой"
	// и не конфликтовал с новой копией программы
	killChatUI()

	executable, _ := os.Executable()
	cmd := exec.Command(executable)
	if err := cmd.Start(); err != nil {
		log.Printf("Ошибка перезапуска: %v", err)
		return
	}

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
editor_path: "notepad.exe"
llm_path: "mistral.exe"
app_toggle_hotkey: "Ctrl+F12"
chatui_path: ".\\ClipGen-m-chatui.exe"
chatui_hotkey: "Ctrl+M"
system_prompt: |
  Вы работаете в Windows-утилите ClipGen-m, которая помогает отправлять запросы к ИИ-моделям через буфер обмена.
  Используйте табуляцию для разделения столбцов в таблицах, это необходимо для возможности вставки текста в Excel.
  Не добавляйте вступительные заполнители. Используй простой текст без маркдауна.

actions:
  - name: "Сменить раскладку (Punto)"
    hotkey: "Pause"
    input_type: "layout_switch"
    output_mode: "replace"
  - name: "Исправить текст (F1)"
    hotkey: "Ctrl+F1"
    prompt: |
      Исправь грамматику, пунктуацию и стиль. Не надо переводить на другой язык. Верни ТОЛЬКО исправленный текст.
      Текст: {{.clipboard}}
    input_type: "text"
    output_mode: "replace"
  - name: "Выполнить просьбу (F2)"
    hotkey: "Ctrl+F2"
    prompt: "Выполни просьбу:\n{{.clipboard}}"
    input_type: "text"
    output_mode: "replace"
  - name: "Перевести и показать (F3)"
    hotkey: "Ctrl+F3"
    prompt: "Переведи на русский. Верни только перевод.\n{{.clipboard}}"
    input_type: "auto"
    output_mode: "editor"
  - name: "Перевести и заменить (F4)"
    hotkey: "Ctrl+F4"
    prompt: "Переведи на русский (если уже рус - на англ). Верни только перевод.\n{{.clipboard}}"
    input_type: "text"
    output_mode: "replace"
  - name: "OCR / Текст с картинки (F6)"
    hotkey: "Ctrl+F6"
    prompt: "Извлеки весь текст с изображения. Верни только текст."
    input_type: "auto"
    output_mode: "editor"
  - name: "Умный анализ (F8)"
    hotkey: "Ctrl+F8"
    prompt: |
      Проанализируй данные из буфера обмена.
      ДАННЫЕ: {{.clipboard}}
      ЗАДАЧА ПОЛЬЗОВАТЕЛЯ: {{ask_user}}
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
	exePath, _ := os.Executable()
	dir := filepath.Dir(exePath)
	iconNormal, _ = os.ReadFile(filepath.Join(dir, "icon.ico"))
	iconWait, _ = os.ReadFile(filepath.Join(dir, "icon_wait.ico"))
	iconStop, _ = os.ReadFile(filepath.Join(dir, "icon_stop.ico"))
	if len(iconNormal) == 0 {
		iconNormal, _ = os.ReadFile("icon.ico")
	}
	if len(iconWait) == 0 {
		iconWait = iconNormal
	}
	if len(iconStop) == 0 {
		iconStop = iconNormal
	}
}

func disableHotkeys() {
	hotkeyMutex.Lock()
	defer hotkeyMutex.Unlock()
	systray.SetIcon(iconStop)
	systray.SetTooltip("ClipGen-m: Отключено")
	if len(activeHotkeys) == 0 {
		return
	}
	log.Println("Отмена регистрации горячих клавиш...")
	for _, control := range activeHotkeys {
		close(control.quit)
		control.hk.Unregister()
	}
	activeHotkeys = nil
}

func enableHotkeys() {
	hotkeyMutex.Lock()
	defer hotkeyMutex.Unlock()
	systray.SetIcon(iconNormal)
	systray.SetTooltip("ClipGen-m: Active")
	log.Println("Registering hotkeys...")
	for _, action := range config.Actions {
		log.Printf("Attempting to register hotkey '%s' for action '%s'", action.Hotkey, action.Name)
		mods, key, err := parseHotkey(action.Hotkey)
		if err != nil {
			log.Printf("Error parsing hotkey '%s': %v", action.Hotkey, err)
			continue
		}
		log.Printf("Parsing successful. Modifiers: %d, Key: %d for action '%s'", mods, key, action.Name)
		hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(mods)}, key)
		if err := hk.Register(); err != nil {
			log.Printf("Failed to register '%s' for action '%s': %v", action.Hotkey, action.Name, err)
			log.Printf("POSSIBLY THIS HOTKEY IS USED BY ANOTHER APPLICATION: %s", action.Hotkey)
			continue
		}
		log.Printf("SUCCESSFULLY registered hotkey '%s' for action '%s'", action.Hotkey, action.Name)
		control := HotkeyControl{hk: hk, quit: make(chan struct{})}
		activeHotkeys = append(activeHotkeys, control)
		go func(a Action, c HotkeyControl) {
			for {
				select {
				case <-c.hk.Keydown():
					log.Printf("Hotkey '%s' for action '%s' triggered", a.Hotkey, a.Name)
					actionChan <- a
				case <-c.quit:
					return
				}
			}
		}(action, control)
	}
}

// ИЗМЕНЕНИЕ: Функция теперь возвращает uint32 для модификаторов и использует флаг MOD_NOREPEAT
// ИЗМЕНЕНИЕ: Исправлена ошибка типов при смешивании uint32 и hotkey.Modifier
func parseHotkey(hotkeyStr string) (uint32, hotkey.Key, error) {
	log.Printf("Parsing hotkey: %s", hotkeyStr)
	parts := strings.Split(hotkeyStr, "+")
	var mods uint32
	var key hotkey.Key
	keyMap := map[string]hotkey.Key{
		"F1": hotkey.KeyF1, "F2": hotkey.KeyF2, "F3": hotkey.KeyF3, "F4": hotkey.KeyF4,
		"F5": hotkey.KeyF5, "F6": hotkey.KeyF6, "F7": hotkey.KeyF7, "F8": hotkey.KeyF8,
		"F9": hotkey.KeyF9, "F10": hotkey.KeyF10, "F11": hotkey.KeyF11, "F12": hotkey.KeyF12,
		"PAUSE": hotkey.Key(19),
	}
	for i := 0; i < 26; i++ {
		char := string(rune('A' + i))
		keyMap[char] = hotkey.Key(0x41 + i)
	}
	for i, part := range parts {
		part = strings.TrimSpace(strings.ToUpper(part))
		log.Printf("Processing hotkey part: %s (index: %d)", part, i)
		if i == len(parts)-1 {
			if k, ok := keyMap[part]; ok {
				key = k
				log.Printf("Found key: %s -> %d", part, key)
			} else {
				log.Printf("Unknown key: %s", part)
				return 0, 0, fmt.Errorf("unknown key: %s", part)
			}
		} else {
			switch part {
			// ИСПРАВЛЕНИЕ: Добавлено явное приведение типа к uint32
			case "CTRL":
				mods |= uint32(hotkey.ModCtrl)
				log.Printf("Added CTRL modifier: %d", mods)
			case "SHIFT":
				mods |= uint32(hotkey.ModShift)
				log.Printf("Added SHIFT modifier: %d", mods)
			case "ALT":
				mods |= uint32(hotkey.ModAlt)
				log.Printf("Added ALT modifier: %d", mods)
			case "WIN":
				mods |= uint32(hotkey.ModWin)
				log.Printf("Added WIN modifier: %d", mods)
			default:
				log.Printf("Unknown modifier: %s", part)
				return 0, 0, fmt.Errorf("unknown modifier: %s", part)
			}
		}
	}
	if key == 0 {
		log.Printf("Key not specified in hotkey: %s", hotkeyStr)
		return 0, 0, fmt.Errorf("key not specified in hotkey")
	}
	log.Printf("Parsing completed successfully: modifiers=%d, key=%d", mods|MOD_NOREPEAT, key)
	return mods | MOD_NOREPEAT, key, nil
}
