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

// Карты раскладок
const (
	enLayout = "`1234567890-=qwertyuiop[]\\asdfghjkl;'zxcvbnm,./~!@#$%^&*()_+QWERTYUIOP{}|ASDFGHJKL:\"ZXCVBNM<>?"
	ruLayout = "ё1234567890-=йцукенгшщзхъ\\фывапролджэячсмитьбю.Ё!\"№;%:?*()_+ЙЦУКЕНГШЩЗХЪ/ФЫВАПРОЛДЖЭЯЧСМИТЬБЮ,"

	// Код клавиши Pause/Break в Windows = 19
	// Мы приводим его к типу hotkey.Key, который требует библиотека
	KeyPause = hotkey.Key(19)
)

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	setupTray()
	go listenHotkeys()
}

func onExit() {
	fmt.Println("Выход...")
}

func setupTray() {
	iconData, err := os.ReadFile("icon.ico")
	if err == nil {
		systray.SetIcon(iconData)
	} else {
		systray.SetTitle("CG")
	}
	systray.SetTitle("ClipGen-m")
	systray.SetTooltip("Ctrl+F1: UpCase\nPause: Смена раскладки")

	mQuit := systray.AddMenuItem("Выход", "Закрыть приложение")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func listenHotkeys() {
	// 1. Хоткей Uppercase (Ctrl + F1)
	hkUpper := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl}, hotkey.KeyF1)
	if err := hkUpper.Register(); err != nil {
		fmt.Println("Ошибка регистрации Ctrl+F1:", err)
	} else {
		defer hkUpper.Unregister()
	}

	// 2. Хоткей Pause (Исправлено)
	// Используем нашу константу KeyPause (код 19)
	hkSwitch := hotkey.New(nil, KeyPause)
	if err := hkSwitch.Register(); err != nil {
		fmt.Println("Ошибка регистрации Pause:", err)
	} else {
		defer hkSwitch.Unregister()
	}

	fmt.Println("Слушаем: Ctrl+F1 и Pause")

	for {
		select {
		case <-hkUpper.Keydown():
			fmt.Println("Нажат Ctrl+F1")
			processSelection(strings.ToUpper)
			<-hkUpper.Keyup()

		case <-hkSwitch.Keydown():
			fmt.Println("Нажат Pause")
			processSelection(switcherLogic)
			<-hkSwitch.Keyup()
		}
	}
}

func processSelection(modifierFunc func(string) string) {
	kb, err := keybd_event.NewKeyBonding()
	if err != nil {
		return
	}

	// Небольшая задержка, чтобы физическая клавиша Pause успела отжаться
	time.Sleep(200 * time.Millisecond)

	clipboard.WriteAll("")

	// Ctrl+C
	kb.SetKeys(keybd_event.VK_C)
	kb.HasCTRL(true)
	kb.Launching()
	kb.HasCTRL(false)

	time.Sleep(100 * time.Millisecond)

	text, err := clipboard.ReadAll()
	if err != nil || text == "" {
		return
	}

	newText := modifierFunc(text)
	if newText == text {
		return
	}

	clipboard.WriteAll(newText)

	// Ctrl+V
	kb.SetKeys(keybd_event.VK_V)
	kb.HasCTRL(true)
	kb.Launching()
	kb.HasCTRL(false)
}

func switcherLogic(input string) string {
	var ruCount, enCount int

	for _, r := range input {
		if strings.ContainsRune(ruLayout, r) {
			ruCount++
		} else if strings.ContainsRune(enLayout, r) {
			enCount++
		}
	}

	if ruCount == 0 && enCount == 0 {
		return input
	}

	toRu := enCount > ruCount

	var sb strings.Builder
	for _, r := range input {
		converted := r
		if toRu {
			idx := strings.IndexRune(enLayout, r)
			if idx != -1 {
				runes := []rune(ruLayout)
				if idx < len(runes) {
					converted = runes[idx]
				}
			}
		} else {
			idx := strings.IndexRune(ruLayout, r)
			if idx != -1 {
				runes := []rune(enLayout)
				if idx < len(runes) {
					converted = runes[idx]
				}
			}
		}
		sb.WriteRune(converted)
	}

	return sb.String()
}
