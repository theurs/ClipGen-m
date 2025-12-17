// file: internal/ui/mainwindow.go
package ui

import (
	"clipgen-m-chatui/internal/chat"
	"clipgen-m-chatui/internal/config"
	"clipgen-m-chatui/internal/mistral"
	"context"
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

// Простая модель для списка файлов
type FileListModel struct {
	walk.ListModelBase
	items []string
}

func (m *FileListModel) ItemCount() int {
	return len(m.items)
}
func (m *FileListModel) Value(index int) interface{} {
	return filepath.Base(m.items[index]) // Показываем только имя файла
}
func (m *FileListModel) Add(paths []string) {
	m.items = append(m.items, paths...)
	m.PublishItemsReset()
}
func (m *FileListModel) Remove(index int) {
	m.items = append(m.items[:index], m.items[index+1:]...)
	m.PublishItemsReset()
}
func (m *FileListModel) Clear() {
	m.items = []string{}
	m.PublishItemsReset()
}

func CreateAndRunMainWindow() {
	var mainWindow *walk.MainWindow
	var historyTE, inputTE *walk.TextEdit
	var sendBtn *walk.PushButton
	var chatCombo *walk.ComboBox
	var chkCtrlEnter *walk.CheckBox

	var filesListBox *walk.ListBox
	var filesBox *walk.Composite

	cfg := config.Load()
	availableChats := chat.ListChats()

	fileModel := &FileListModel{items: []string{}}

	var cancelGen context.CancelFunc

	// --- ЛОГИКА ---

	appendHistory := func(author, text string) {
		currentTime := time.Now().Format("02.01.2006 15:04")
		msg := fmt.Sprintf("%s [%s]:\r\n%s\r\n\r\n", author, currentTime, text)
		historyTE.AppendText(msg)
		historyTE.SendMessage(277, 7, 0)
	}

	updateFilesVisibility := func() {
		hasFiles := fileModel.ItemCount() > 0
		filesBox.SetVisible(hasFiles)
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
		res := walk.MsgBox(mainWindow, "Удаление", fmt.Sprintf("Удалить чат '%s'?", currentChatID), walk.MsgBoxYesNo|walk.MsgBoxIconWarning)
		if res == walk.DlgCmdYes {
			_ = chat.DeleteChat(currentChatID)
			cfg.RemoveChatSettings(currentChatID)
			availableChats = chat.ListChats()
			chatCombo.SetModel(availableChats)
			chatCombo.SetText("default")
			loadSelectedChat()
		}
	}

	clearHistory := func() {
		currentChatID := chatCombo.Text()
		if walk.MsgBox(mainWindow, "Очистка", "Очистить историю?", walk.MsgBoxYesNo) == walk.DlgCmdYes {
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
		if err == nil && ok {
			cfg.SetChatSettings(currentChatID, settings)
			cfg.Save()
		}
	}

	// Обработка Ctrl+V
	handlePaste := func() {
		if HasClipboardFiles() {
			files, err := GetClipboardFiles()
			if err == nil && len(files) > 0 {
				fileModel.Add(files)
				updateFilesVisibility()
				return
			}
		}

		if HasClipboardImage() {
			path, err := SaveClipboardImageToTemp()
			if err == nil && path != "" {
				fileModel.Add([]string{path})
				updateFilesVisibility()
			} else if err != nil {
				walk.MsgBox(mainWindow, "Ошибка", "Не удалось вставить изображение: "+err.Error(), walk.MsgBoxIconError)
			}
			return
		}

		// Если это просто текст, используем системное сообщение WM_PASTE
		inputTE.SendMessage(0x0302, 0, 0)
	}

	doSendOrStop := func() {
		// STOP
		if cancelGen != nil {
			cancelGen()
			cancelGen = nil
			return
		}

		// SEND
		prompt := inputTE.Text()
		attachedFiles := fileModel.items

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

		sendBtn.SetText("Стоп ⏹")

		filesToSend := make([]string, len(attachedFiles))
		copy(filesToSend, attachedFiles)
		fileModel.Clear()
		updateFilesVisibility()

		chatSettings := cfg.GetChatSettings(currentChatID)

		ctx, cancel := context.WithCancel(context.Background())
		cancelGen = cancel

		go func() {
			defer func() {
				mainWindow.Synchronize(func() {
					sendBtn.SetEnabled(true)
					sendBtn.SetText("Отправить")
					inputTE.SetFocus()
					cancelGen = nil
				})
				cancel()
			}()

			opts := mistral.RunOptions{
				Prompt:       prompt,
				ChatID:       currentChatID,
				Files:        filesToSend,
				SystemPrompt: chatSettings.SystemPrompt,
				Temperature:  chatSettings.Temperature,
				ModelMode:    chatSettings.ModelMode,
			}

			answer, err := mistral.Run(ctx, opts)
			if err != nil {
				answer = "Ошибка: " + err.Error()
			}

			mainWindow.Synchronize(func() {
				appendHistory("AI", answer)
			})
		}()
	}

	selectFiles := func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "Выберите файлы"
		dlg.Filter = "Все файлы (*.*)|*.*"
		if ok, err := dlg.ShowOpen(mainWindow); err == nil && ok {
			fileModel.Add([]string{dlg.FilePath})
			updateFilesVisibility()
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
					PushButton{Text: "Del", OnClicked: deleteCurrentChat, MaxSize: Size{Width: 40}},
					PushButton{Text: "Clr", OnClicked: clearHistory, MaxSize: Size{Width: 40}},
					VSpacer{Size: 10},
					PushButton{Text: "Файл", OnClicked: selectFiles},
					CheckBox{AssignTo: &chkCtrlEnter, Text: "Ctrl+Enter", Checked: cfg.SendCtrlEnter},
					HSpacer{},
					PushButton{Text: "Настройки", OnClicked: openSettings},
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

							Composite{
								AssignTo: &filesBox,
								Visible:  false,
								Layout:   HBox{MarginsZero: true},
								MaxSize:  Size{Height: 60},
								Children: []Widget{
									ListBox{
										AssignTo: &filesListBox,
										Model:    fileModel,
										MinSize:  Size{Width: 200},
									},
									Composite{
										Layout: VBox{MarginsZero: true},
										Children: []Widget{
											PushButton{
												Text: "Удалить выбр.",
												OnClicked: func() {
													idx := filesListBox.CurrentIndex()
													if idx >= 0 {
														fileModel.Remove(idx)
														updateFilesVisibility()
													}
												},
											},
											PushButton{
												Text: "Очистить все",
												OnClicked: func() {
													fileModel.Clear()
													updateFilesVisibility()
												},
											},
										},
									},
								},
							},

							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									TextEdit{
										AssignTo: &inputTE,
										VScroll:  true,
										OnKeyDown: func(key walk.Key) {
											mods := walk.ModifiersDown()

											if key == walk.KeyV && mods == walk.ModControl {
												handlePaste()
												return
											}

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
												doSendOrStop()
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
										OnClicked: doSendOrStop,
										MinSize:   Size{Width: 80},
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
