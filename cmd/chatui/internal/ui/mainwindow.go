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

// loadIcon пытается загрузить иконку.
// В текущей версии мы закомментировали использование, чтобы не было паники.
func loadIcon(name string) *walk.Icon {
	icon, err := walk.NewIconFromFile(filepath.Join("assets", name))
	if err != nil {
		return nil
	}
	return icon
}

func CreateAndRunMainWindow() {
	var mainWindow *walk.MainWindow
	var historyTE, inputTE *walk.TextEdit
	var sendBtn *walk.PushButton
	var chatCombo *walk.ComboBox
	var chkCtrlEnter *walk.CheckBox

	var filesLabel *walk.Label
	var attachBtn *walk.PushButton
	var settingsBtn *walk.PushButton

	var attachedFiles []string

	cfg := config.Load()
	availableChats := chat.ListChats()

	// --- ЛОГИКА ---

	appendHistory := func(author, text string) {
		currentTime := time.Now().Format("02.01.2006 15:04")
		msg := fmt.Sprintf("%s [%s]:\r\n%s\r\n\r\n", author, currentTime, text)
		historyTE.AppendText(msg)
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
		historyTE.SendMessage(277, 7, 0)
	}

	deleteCurrentChat := func() {
		currentChatID := chatCombo.Text()
		if currentChatID == "" || currentChatID == "default" {
			walk.MsgBox(mainWindow, "Ошибка", "Нельзя удалить этот чат.", walk.MsgBoxIconError)
			return
		}

		res := walk.MsgBox(mainWindow, "Удаление",
			fmt.Sprintf("Вы уверены, что хотите удалить чат '%s'?", currentChatID),
			walk.MsgBoxYesNo|walk.MsgBoxIconWarning)

		if res == walk.DlgCmdYes {
			if err := chat.DeleteChat(currentChatID); err != nil {
				walk.MsgBox(mainWindow, "Ошибка", "Не удалось удалить файл: "+err.Error(), walk.MsgBoxIconError)
				return
			}
			cfg.RemoveChatSettings(currentChatID)

			availableChats = chat.ListChats()
			chatCombo.SetModel(availableChats)
			chatCombo.SetText("default")
			loadSelectedChat()

			walk.MsgBox(mainWindow, "Успех", "Чат удален.", walk.MsgBoxIconInformation)
		}
	}

	clearHistory := func() {
		currentChatID := chatCombo.Text()
		res := walk.MsgBox(mainWindow, "Очистка", "Очистить историю переписки?", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)

		if res == walk.DlgCmdYes {
			_ = chat.DeleteChat(currentChatID)
			historyTE.SetText("")
			appendHistory("Система", "История очищена.")
		}
	}

	openSettings := func() {
		currentChatID := chatCombo.Text()
		if currentChatID == "" {
			return
		}

		settings := cfg.GetChatSettings(currentChatID)

		ok, err := RunSettingsDialog(mainWindow, &settings)
		if err != nil {
			walk.MsgBox(mainWindow, "Ошибка", err.Error(), walk.MsgBoxIconError)
			return
		}

		if ok {
			cfg.SetChatSettings(currentChatID, settings)
			cfg.Save()
			walk.MsgBox(mainWindow, "Настройки", "Настройки чата сохранены.", walk.MsgBoxIconInformation)
		}
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

		chatSettings := cfg.GetChatSettings(currentChatID)

		go func() {
			opts := mistral.RunOptions{
				Prompt:       prompt,
				ChatID:       currentChatID,
				Files:        filesToSend,
				SystemPrompt: chatSettings.SystemPrompt,
				Temperature:  chatSettings.Temperature,
				ModelMode:    chatSettings.ModelMode,
			}

			answer, err := mistral.Run(opts)
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
		Bounds:   Rectangle{X: cfg.X, Y: cfg.Y, Width: cfg.Width, Height: cfg.Height},
		Layout:   VBox{},
		Children: []Widget{

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
						MinSize:               Size{Width: 150},
					},

					// Кнопка удаления
					PushButton{
						Text: "Del",
						// Image:     loadIcon("delete.ico"), <--- ЗАКОММЕНТИРОВАНО
						OnClicked:   deleteCurrentChat,
						ToolTipText: "Удалить чат навсегда",
						MaxSize:     Size{Width: 40},
					},

					// Кнопка очистки
					PushButton{
						Text: "Clr",
						// Image:     loadIcon("clean.ico"), <--- ЗАКОММЕНТИРОВАНО
						OnClicked:   clearHistory,
						ToolTipText: "Очистить историю сообщений",
						MaxSize:     Size{Width: 40},
					},

					VSpacer{Size: 10},

					// Кнопка файла
					PushButton{
						AssignTo: &attachBtn,
						Text:     "Файл",
						// Image:     loadIcon("file.ico"), <--- ЗАКОММЕНТИРОВАНО
						OnClicked:   selectFiles,
						ToolTipText: "Прикрепить файл",
					},

					CheckBox{
						AssignTo: &chkCtrlEnter,
						Text:     "Ctrl+Enter",
						Checked:  cfg.SendCtrlEnter,
					},

					HSpacer{},

					// Кнопка настроек
					PushButton{
						AssignTo: &settingsBtn,
						Text:     "Настройки",
						// Image:     loadIcon("settings.ico"), <--- ЗАКОММЕНТИРОВАНО
						OnClicked: openSettings,
					},
				},
			},

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
