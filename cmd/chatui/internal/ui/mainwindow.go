// file: internal/ui/mainwindow.go
package ui

import (
	"clipgen-m-chatui/internal/chat"
	"clipgen-m-chatui/internal/config"
	"clipgen-m-chatui/internal/mistral"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var (
	app *walk.Application
)

func Initialize() error {
	app = walk.App()
	return nil
}

func Terminate() {
	// walk сам обрабатывает выход
}

func CreateAndRunMainWindow() {
	var mainWindow *walk.MainWindow
	var historyTE, inputTE *walk.TextEdit
	var sendBtn *walk.PushButton
	var chatCombo *walk.ComboBox
	var chkCtrlEnter *walk.CheckBox

	var filesLabel *walk.Label
	var attachBtn *walk.PushButton
	var attachedFiles []string

	cfg := config.Load()
	availableChats := chat.ListChats()

	// --- ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ ---

	appendHistory := func(author, text string) {
		// ИЗМЕНЕНИЕ ЗДЕСЬ: Полная дата и время для текущего сообщения
		currentTime := time.Now().Format("02.01.2006 15:04")

		msg := fmt.Sprintf("%s [%s]:\r\n%s\r\n\r\n", author, currentTime, text)
		historyTE.AppendText(msg)

		// Прокрутка вниз
		historyTE.SendMessage(277, 7, 0)
	}

	updateFilesLabel := func() {
		if len(attachedFiles) == 0 {
			filesLabel.SetText("")
			filesLabel.SetVisible(false)
		} else {
			var names []string
			for _, f := range attachedFiles {
				names = append(names, filepath.Base(f))
			}
			text := fmt.Sprintf("Прикреплено (%d): %s", len(attachedFiles), strings.Join(names, ", "))
			filesLabel.SetText(text)
			filesLabel.SetVisible(true)
		}
	}

	loadSelectedChat := func() {
		chatID := chatCombo.Text()
		if chatID == "" {
			historyTE.SetText("")
			return
		}
		text := chat.LoadHistory(chatID)
		historyTE.SetText(text)

		// Прокрутка вниз при загрузке
		historyTE.SendMessage(277, 7, 0)
	}

	doSend := func() {
		prompt := inputTE.Text()
		if strings.TrimSpace(prompt) == "" && len(attachedFiles) == 0 {
			return
		}

		currentChatID := chatCombo.Text()
		if strings.TrimSpace(currentChatID) == "" {
			currentChatID = "default"
			chatCombo.SetText(currentChatID)
		}

		if strings.TrimSpace(prompt) == "" {
			prompt = "[Анализ файлов]"
		}

		inputTE.SetText("")

		displayPrompt := prompt
		if len(attachedFiles) > 0 {
			displayPrompt += fmt.Sprintf("\r\n[Файлы: %d шт.]", len(attachedFiles))
		}
		appendHistory("Вы", displayPrompt)

		sendBtn.SetEnabled(false)
		attachBtn.SetEnabled(false)
		sendBtn.SetText("Думаю...")

		filesToSend := make([]string, len(attachedFiles))
		copy(filesToSend, attachedFiles)

		attachedFiles = []string{}
		updateFilesLabel()

		go func() {
			answer, err := mistral.Run(prompt, currentChatID, filesToSend)
			if err != nil {
				answer = "Ошибка: " + err.Error()
			}

			mainWindow.Synchronize(func() {
				appendHistory("AI", answer)
				sendBtn.SetEnabled(true)
				attachBtn.SetEnabled(true)
				sendBtn.SetText("Отправить")
				inputTE.SetFocus()
			})
		}()
	}

	selectFiles := func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "Выберите файлы"
		dlg.Filter = "Все файлы (*.*)|*.*"

		if ok, err := dlg.ShowOpen(mainWindow); err == nil && ok {
			attachedFiles = append(attachedFiles, dlg.FilePath)
			updateFilesLabel()
		}
	}

	// --- UI ---

	err := MainWindow{
		AssignTo: &mainWindow,
		Title:    "ClipGen-m ChatUI",
		Bounds: Rectangle{
			X: cfg.X, Y: cfg.Y, Width: cfg.Width, Height: cfg.Height,
		},
		Layout: VBox{},
		Children: []Widget{
			// Тулбар
			Composite{
				Layout: HBox{},
				Children: []Widget{
					Label{Text: "Чат:"},
					ComboBox{
						AssignTo:              &chatCombo,
						Editable:              true,
						Model:                 availableChats,
						OnCurrentIndexChanged: func() { loadSelectedChat() },
						OnEditingFinished:     func() { loadSelectedChat() },
					},

					PushButton{
						AssignTo:  &attachBtn,
						Text:      "📎 Файл",
						OnClicked: selectFiles,
					},

					CheckBox{
						AssignTo: &chkCtrlEnter,
						Text:     "Ctrl+Enter",
						Checked:  cfg.SendCtrlEnter,
					},
					HSpacer{},
				},
			},

			// Рабочая область
			VSplitter{
				Children: []Widget{
					TextEdit{
						AssignTo:      &historyTE,
						ReadOnly:      true,
						VScroll:       true,
						StretchFactor: 10,
					},

					Composite{
						Layout:        VBox{MarginsZero: true},
						StretchFactor: 1,
						MinSize:       Size{Height: 100},
						Children: []Widget{

							Label{
								AssignTo:  &filesLabel,
								Text:      "",
								Visible:   false,
								TextColor: walk.RGB(0, 0, 150),
							},

							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									TextEdit{
										AssignTo: &inputTE,
										VScroll:  true,
										OnKeyDown: func(key walk.Key) {
											mods := walk.ModifiersDown()
											isCtrlEnterMode := chkCtrlEnter.Checked()
											shouldSend := false

											if isCtrlEnterMode {
												if key == walk.KeyReturn && mods == walk.ModControl {
													shouldSend = true
												}
											} else {
												if key == walk.KeyReturn && mods == 0 {
													shouldSend = true
												}
											}

											if shouldSend {
												doSend()
												go func() {
													time.Sleep(10 * time.Millisecond)
													mainWindow.Synchronize(func() { inputTE.SetText("") })
												}()
											}
										},
									},
									PushButton{
										AssignTo:  &sendBtn,
										Text:      "Отправить",
										OnClicked: doSend,
									},
								},
							},
						},
					},
				},
			},
		},
	}.Create()

	if err != nil {
		log.Fatalf("Ошибка создания MainWindow: %v", err)
	}

	if len(availableChats) > 0 {
		_ = chatCombo.SetCurrentIndex(0)
		loadSelectedChat()
	} else {
		chatCombo.SetText("default")
	}

	mainWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		b := mainWindow.Bounds()
		cfg.X, cfg.Y, cfg.Width, cfg.Height = b.X, b.Y, b.Width, b.Height
		cfg.SendCtrlEnter = chkCtrlEnter.Checked()
		cfg.Save()
	})

	mainWindow.Run()
}
