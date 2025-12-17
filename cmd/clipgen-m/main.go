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
)

const (
	CF_HDROP         = 15
	CF_DIB           = 8
	WM_CLOSE         = 0x0010
	SMTO_ABORTIFHUNG = 0x0002
	// НОВЫЙ ФЛАГ: Для надежной регистрации глобальных хоткеев.
	MOD_NOREPEAT = 0x4000
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

func onExit() {
	chatUIMutex.Lock()
	if chatUIProcess != nil {
		log.Println("Завершение процесса Chat UI при выходе (KILL)...")
		// Жесткое убийство при выходе из программы
		err := chatUIProcess.Kill()
		if err != nil {
			log.Printf("Ошибка при убийстве процесса: %v", err)
		}
		chatUIProcess = nil
	}
	chatUIMutex.Unlock()

	disableHotkeys()
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
	cmd := exec.Command(config.EditorPath, logPath)
	if err := cmd.Start(); err != nil {
		exec.Command("notepad.exe", logPath).Start()
	}
}

func openConfigFile() {
	configDir, _ := os.UserConfigDir()
	configPath := filepath.Join(configDir, "clipgen-m", "config.yaml")
	cmd := exec.Command(config.EditorPath, configPath)
	if err := cmd.Start(); err != nil {
		exec.Command("notepad.exe", configPath).Start()
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
	mConfig := systray.AddMenuItem("Настройки", "Редактировать конфиг")
	mLog := systray.AddMenuItem("Открыть лог", "Посмотреть ошибки")
	mReload := systray.AddMenuItem("Перезагрузка", "Применить конфиг")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")

	toggleHotkeyCh, err := setupToggleHotkey()
	if err != nil {
		log.Printf("WARNING: %v", err)
	}

	go func() {
		for {
			select {
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
			case <-mLog.ClickedCh:
				openLogFile()
			case <-mConfig.ClickedCh:
				openConfigFile()
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
	mods, key, err := parseHotkey(config.ChatUIHotkey)
	if err != nil {
		log.Printf("WARNING: ошибка парсинга ChatUIHotkey: %v", err)
		return
	}
	hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(mods)}, key)
	if err := hk.Register(); err != nil {
		log.Printf("WARNING: не удалось зарегистрировать ChatUIHotkey: %v", err)
		return
	}
	go func() {
		for range hk.Keydown() {
			toggleChatUI()
		}
	}()
	log.Printf("Зарегистрирован хоткей для чата: %s", config.ChatUIHotkey)
}

func toggleChatUI() {
	chatUIMutex.Lock()
	defer chatUIMutex.Unlock()

	// Сценарий 1: Процесс запущен, нужно его жестко убить
	if chatUIProcess != nil {
		log.Printf("[Действие] Убиваем процесс ChatUI (PID: %d)", chatUIProcess.Pid)
		// Убиваем процесс
		if err := chatUIProcess.Kill(); err != nil {
			log.Printf(" -> Ошибка Kill: %v", err)
		}
		// Сразу обнуляем переменную, не дожидаясь наблюдателя,
		// чтобы интерфейс был отзывчивым
		chatUIProcess = nil
		return
	}

	// Сценарий 2: Процесс не запущен, запускаем
	log.Println("[Действие] Процесс не существует. Запускаем новый...")
	// Используем путь из конфига!
	cmd := exec.Command(config.ChatUIPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW (если это консольное приложение)
	}

	if err := cmd.Start(); err != nil {
		log.Printf(" -> Ошибка запуска: %v", err)
		return
	}

	chatUIProcess = cmd.Process
	log.Printf(" -> Процесс успешно запущен (PID: %d)", chatUIProcess.Pid)

	// Горутина-наблюдатель для очистки (на случай, если пользователь закроет окно руками)
	go func(p *os.Process) {
		p.Wait()
		chatUIMutex.Lock()
		// Проверяем, что это тот самый процесс, а не новый, запущенный после перезапуска
		if chatUIProcess != nil && chatUIProcess.Pid == p.Pid {
			log.Println("[Наблюдатель] Процесс завершился, очищаем переменную.")
			chatUIProcess = nil
		}
		chatUIMutex.Unlock()
	}(cmd.Process)
}

func restartApp() {
	executable, _ := os.Executable()
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
editor_path: "notepad.exe"
llm_path: "mistral.exe"
app_toggle_hotkey: "Ctrl+F12"
chatui_path: "ClipGen-m-chatui.exe"
chatui_hotkey: "Ctrl+M"
system_prompt: |
  Вы работаете в Windows-утилите ClipGen-m, которая помогает отправлять запросы к ИИ-моделям через буфер обмена.
  Используйте только обычный текст, маркдаун не разрешен.

actions:
  - name: "Сменить раскладку (Punto)"
    hotkey: "Pause"
    input_type: "layout_switch"
    output_mode: "replace"
  - name: "Исправить текст (F1)"
    hotkey: "Ctrl+F1"
    prompt: |
      Исправь грамматику, пунктуацию и стиль. Верни ТОЛЬКО исправленный текст.
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
  - name: "Объяснить (AltF5)"
    hotkey: "Alt+F5"
    prompt: "Объясни это простыми словами.\n{{.clipboard}}"
    input_type: "auto"
    output_mode: "editor"
  - name: "OCR / Текст с картинки (F6)"
    hotkey: "Ctrl+F6"
    prompt: "Извлеки весь текст с изображения. Верни только текст."
    input_type: "auto"
    output_mode: "editor"
  - name: "Сделать саммари из файлов (F7)"
    hotkey: "Ctrl+F7"
    prompt: "Сделай краткое саммари по всем предоставленным файлам."
    input_type: "files"
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
	systray.SetTooltip("ClipGen-m: Активен")
	log.Println("Регистрация горячих клавиш...")
	for _, action := range config.Actions {
		mods, key, err := parseHotkey(action.Hotkey)
		if err != nil {
			log.Printf("Ошибка хоткея '%s': %v", action.Hotkey, err)
			continue
		}
		hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(mods)}, key)
		if err := hk.Register(); err != nil {
			log.Printf("Не удалось зарегистрировать '%s': %v", action.Hotkey, err)
			continue
		}
		control := HotkeyControl{hk: hk, quit: make(chan struct{})}
		activeHotkeys = append(activeHotkeys, control)
		go func(a Action, c HotkeyControl) {
			for {
				select {
				case <-c.hk.Keydown():
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
		if i == len(parts)-1 {
			if k, ok := keyMap[part]; ok {
				key = k
			} else {
				return 0, 0, fmt.Errorf("неизвестная клавиша: %s", part)
			}
		} else {
			switch part {
			// ИСПРАВЛЕНИЕ: Добавлено явное приведение типа к uint32
			case "CTRL":
				mods |= uint32(hotkey.ModCtrl)
			case "SHIFT":
				mods |= uint32(hotkey.ModShift)
			case "ALT":
				mods |= uint32(hotkey.ModAlt)
			case "WIN":
				mods |= uint32(hotkey.ModWin)
			default:
				return 0, 0, fmt.Errorf("неизвестный модификатор: %s", part)
			}
		}
	}
	if key == 0 {
		return 0, 0, fmt.Errorf("клавиша не указана в хоткее")
	}
	return mods | MOD_NOREPEAT, key, nil
}
