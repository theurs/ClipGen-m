package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func main() {
	var mainWindow *walk.MainWindow
	var inputTE *walk.TextEdit
	var logTE *walk.TextEdit
	var rbEnter, rbCtrlEnter *walk.RadioButton

	// Функция логирования
	logMsg := func(msg string) {
		logTE.AppendText(msg + "\r\n")
		// Прокрутка вниз
		logTE.SetTextSelection(len(logTE.Text()), len(logTE.Text()))
	}

	// Функция отправки
	sendMessage := func() {
		text := inputTE.Text()
		if strings.TrimSpace(text) == "" {
			return
		}

		logMsg(fmt.Sprintf(">>> ОТПРАВЛЕНО: '%s'", strings.ReplaceAll(text, "\r\n", "[CRLF]")))

		// Очистка поля
		inputTE.SetText("")
	}

	err := MainWindow{
		AssignTo: &mainWindow,
		Title:    "Тест клавиш (Enter vs Ctrl+Enter)",
		Size:     Size{Width: 400, Height: 500},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "Выберите режим отправки:"},
			GroupBox{
				Layout: HBox{},
				Children: []Widget{
					RadioButton{
						AssignTo: &rbEnter,
						Text:     "Enter = Отпр / Shift+Enter = Стр",
						// Checked здесь убрали, поставим ниже
					},
					RadioButton{
						AssignTo: &rbCtrlEnter,
						Text:     "Ctrl+Enter = Отпр / Enter = Стр",
					},
				},
			},

			Label{Text: "Поле ввода:"},
			TextEdit{
				AssignTo: &inputTE,
				VScroll:  true,
				MinSize:  Size{Height: 100},

				// ИСПРАВЛЕНИЕ: Функция принимает только key
				OnKeyDown: func(key walk.Key) {
					// Получаем модификаторы (Ctrl, Shift) отдельно
					mods := walk.ModifiersDown()

					// 1. Режим "Enter = Отправить"
					if rbEnter.Checked() {
						// Enter нажат, а Shift/Ctrl/Alt - НЕТ
						if key == walk.KeyReturn && mods == 0 {
							sendMessage()
						}
					}

					// 2. Режим "Ctrl+Enter = Отправить"
					if rbCtrlEnter.Checked() {
						// Enter нажат И Ctrl зажат
						if key == walk.KeyReturn && mods == walk.ModControl {
							sendMessage()
						}
					}
				},
			},

			Label{Text: "Лог событий:"},
			TextEdit{
				AssignTo: &logTE,
				ReadOnly: true,
				VScroll:  true,
			},

			PushButton{
				Text:      "Очистить лог",
				OnClicked: func() { logTE.SetText("") },
			},
		},
	}.Create() // Используем Create(), чтобы окно создалось, но не запустилось сразу

	if err != nil {
		log.Fatal(err)
	}

	// ИСПРАВЛЕНИЕ: Устанавливаем галочку по умолчанию здесь
	rbEnter.SetChecked(true)

	// Запускаем окно
	mainWindow.Run()
}
