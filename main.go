package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/getlantern/systray"
	"github.com/micmonay/keybd_event"
	"golang.design/x/hotkey"
)

func main() {
	// Основной цикл UI (трея) блокирует поток, поэтому хоткеи будем запускать внутри
	systray.Run(onReady, onExit)
}

func onReady() {
	setupTray()

	// Запускаем прослушку хоткеев в отдельной горутине
	go listenHotkeys()
}

func onExit() {
	fmt.Println("Выход из приложения...")
}

// Настройка трея (иконка и меню)
func setupTray() {
	iconData, err := os.ReadFile("icon.ico")
	if err == nil {
		systray.SetIcon(iconData)
	} else {
		systray.SetTitle("ClipGen")
	}

	systray.SetTitle("ClipGen-m")
	systray.SetTooltip("ClipGen-m: Ctrl+F1 для апперкейса")

	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

// Слушаем глобальные горячие клавиши
func listenHotkeys() {
	// Регистрируем Ctrl + F1
	// ModCtrl = Ctrl, KeyF1 = F1
	hk := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl}, hotkey.KeyF1)

	err := hk.Register()
	if err != nil {
		fmt.Printf("Ошибка регистрации хоткея: %v\n", err)
		return
	}

	// Удаляем хоткей при завершении функции (обычно при выходе из программы)
	defer hk.Unregister()

	fmt.Println("Хоткей Ctrl+F1 зарегистрирован. Выдели текст и нажми!")

	// Бесконечный цикл ожидания нажатия
	for {
		// Ждем события нажатия
		<-hk.Keydown()

		fmt.Println("Хоткей нажат! Обрабатываем...")
		handleClipGenAction()

		// Ждем события отпускания (чтобы не спамило, если зажать)
		<-hk.Keyup()
	}
}

// Логика: Копировать -> Обработать -> Вставить
func handleClipGenAction() {
	// 1. Инициализируем эмулятор клавиатуры
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		fmt.Println("Ошибка клавиатуры:", err)
		return
	}

	// Для Windows часто нужно небольшое ожидание, чтобы клавиши "Ctrl+F1" физически успели отжаться
	// иначе они могут смешаться с нашими эмулируемыми Ctrl+C
	time.Sleep(100 * time.Millisecond)

	// --- ЭТАП 1: КОПИРОВАНИЕ (Ctrl+C) ---
	// Очистим буфер перед копированием, чтобы убедиться, что мы взяли именно новый текст
	// (хотя это не обязательно, но полезно для отладки)
	clipboard.WriteAll("")

	kb.SetKeys(keybd_event.VK_C) // Клавиша C
	kb.HasCTRL(true)             // Зажат Ctrl

	// Нажимаем
	err = kb.Launching()
	if err != nil {
		fmt.Println("Не удалось нажать Ctrl+C")
		return
	}

	// Ждем, пока ОС скопирует текст в буфер (это не мгновенно!)
	time.Sleep(100 * time.Millisecond)

	// --- ЭТАП 2: ЧТЕНИЕ И ОБРАБОТКА ---
	text, err := clipboard.ReadAll()
	if err != nil || text == "" {
		fmt.Println("Буфер пуст или ошибка чтения")
		return
	}

	fmt.Printf("Исходный текст: %s\n", text)

	// МАГИЯ ЗДЕСЬ: Делаем текст Uppercase
	newText := strings.ToUpper(text)

	// --- ЭТАП 3: ВСТАВКА (Ctrl+V) ---

	// Записываем измененный текст в буфер
	clipboard.WriteAll(newText)

	// Эмулируем Ctrl+V
	kb.SetKeys(keybd_event.VK_V) // Клавиша V
	kb.HasCTRL(true)             // Зажат Ctrl

	err = kb.Launching()
	if err != nil {
		fmt.Println("Не удалось нажать Ctrl+V")
	}

	fmt.Println("Готово!")
}
