package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// ==========================================================
// Глобальные переменные и WinAPI
// ==========================================================

var (
	// Путь к нашему чату.
	chatUIPath = "ClipGen-m-chatui.exe"

	// Глобальные переменные для управления процессом
	chatUIProcess *os.Process
	chatUIMutex   sync.Mutex

	// WinAPI функции
	user32                   = syscall.NewLazyDLL("user32.dll")
	enumWindows              = user32.NewProc("EnumWindows")
	getWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	sendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	registerHotKey           = user32.NewProc("RegisterHotKey")
	unregisterHotKey         = user32.NewProc("UnregisterHotKey")
	getMessageW              = user32.NewProc("GetMessageW")
)

// Структура для сообщений Windows
type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

const (
	// Константы для хоткея
	MOD_CONTROL  = 0x0002
	MOD_NOREPEAT = 0x4000
	VK_M         = 0x4D   // Виртуальный код клавиши 'M'
	HOTKEY_ID    = 1      // Уникальный ID для нашего хоткея
	WM_HOTKEY    = 0x0312 // Системное сообщение о нажатии хоткея

	// Константы для управления окном
	WM_CLOSE         = 0x0010
	SMTO_ABORTIFHUNG = 0x0002
)

// ==========================================================
// Основная логика
// ==========================================================

func main() {
	// 1. Проверяем, существует ли chatui.exe
	exePath, _ := os.Executable()
	dir := filepath.Dir(exePath)
	fullChatPath := filepath.Join(dir, chatUIPath)
	if _, err := os.Stat(fullChatPath); os.IsNotExist(err) {
		log.Fatalf("Ошибка: Не найден %s рядом с тестовой утилитой.", chatUIPath)
	}
	chatUIPath = fullChatPath

	// 2. Регистрируем хоткей через WinAPI
	log.Println("Регистрация хоткея Ctrl+M...")
	ret, _, err := registerHotKey.Call(
		0,                        // NULL hwnd
		HOTKEY_ID,                // ID нашего хоткея
		MOD_CONTROL|MOD_NOREPEAT, // Модификаторы с нужным флагом
		VK_M,                     // Клавиша
	)
	if ret == 0 {
		log.Fatalf("Не удалось зарегистрировать хоткей: %v", err)
	}
	defer unregisterHotKey.Call(0, HOTKEY_ID) // Гарантируем отмену регистрации при выходе

	log.Printf("Хоткей зарегистрирован. Нажмите Ctrl+M для запуска/остановки '%s'", chatUIPath)
	log.Println("Для выхода закройте это консольное окно.")

	// 3. Запускаем цикл обработки сообщений Windows для перехвата нажатия
	var msg MSG
	for {
		// GetMessageW является блокирующей операцией
		getMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if msg.Message == WM_HOTKEY && msg.WParam == HOTKEY_ID {
			log.Println("--- Хоткей Ctrl+M нажат ---")
			toggleChatUI()
		}
	}
}

// Самая простая и правильная логика
func toggleChatUI() {
	chatUIMutex.Lock()
	defer chatUIMutex.Unlock()

	// Сценарий 1: Процесс запущен, нужно его закрыть
	if chatUIProcess != nil {
		log.Printf("[Действие] Процесс существует (PID: %d). Ищем его окно...", chatUIProcess.Pid)
		hwnd, found := findWindowByPID(uint32(chatUIProcess.Pid))
		if found {
			log.Printf(" -> Окно найдено. Отправка WM_CLOSE...")
			var result uintptr
			sendMessageTimeoutW.Call(
				uintptr(hwnd),
				WM_CLOSE,
				0,
				0,
				SMTO_ABORTIFHUNG,
				2000,
				uintptr(unsafe.Pointer(&result)),
			)
			log.Println(" -> Команда на закрытие отправлена.")
		} else {
			log.Println(" -> Окно не найдено, но процесс существует (зомби?). Принудительное завершение.")
			chatUIProcess.Kill()
			chatUIProcess = nil
		}
		return
	}

	// Сценарий 2: Процесс не запущен, нужно его создать
	log.Println("[Действие] Процесс не существует. Запускаем новый...")
	cmd := exec.Command(chatUIPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}

	if err := cmd.Start(); err != nil {
		log.Printf(" -> Ошибка запуска: %v", err)
		return
	}

	chatUIProcess = cmd.Process
	log.Printf(" -> Процесс успешно запущен (PID: %d)", chatUIProcess.Pid)

	// Горутина-наблюдатель для очистки состояния
	go func(p *os.Process) {
		log.Printf("[Наблюдатель] Следим за процессом PID %d", p.Pid)
		p.Wait()
		log.Printf("[Наблюдатель] Процесс PID %d завершился.", p.Pid)
		chatUIMutex.Lock()
		defer chatUIMutex.Unlock()
		if chatUIProcess != nil && chatUIProcess.Pid == p.Pid {
			log.Println("[Наблюдатель] Очищаем ссылку на процесс.")
			chatUIProcess = nil
		}
	}(cmd.Process)
}

// ==========================================================
// Вспомогательные функции
// ==========================================================

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
