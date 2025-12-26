// file: internal/ui/settings_dialog.go
package ui

import (
	"clipgen-m-chatui/internal/config"
	"fmt"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// RunSettingsDialog открывает модальное окно настроек.
func RunSettingsDialog(owner *walk.MainWindow, settings *config.ChatSettings) (bool, error) {
	var dlg *walk.Dialog
	var db *walk.DataBinder
	var acceptPB, cancelPB *walk.PushButton
	var tempSlider *walk.Slider
	var tempLabel *walk.Label

	// Временная структура для биндинга
	type formBuffer struct {
		SystemPrompt string
		TempInt      int
		ModelMode    string
		LLMProvider  string
	}

	buf := formBuffer{
		SystemPrompt: settings.SystemPrompt,
		TempInt:      int(settings.Temperature * 100),
		ModelMode:    settings.ModelMode,
		LLMProvider:  settings.LLMProvider,
	}
	if buf.ModelMode == "" {
		buf.ModelMode = "auto"
	}
	if buf.LLMProvider == "" {
		buf.LLMProvider = "mistral"
	}

	modes := []string{"auto", "general", "code", "vision", "audio", "ocr"}
	providers := []string{"mistral", "geminillm", "ghllm", "groqllm"}

	// Запускаем диалог и сохраняем результат в переменную
	result, err := Dialog{
		AssignTo:      &dlg,
		Title:         "Настройки чата",
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 400, Height: 450},
		Layout:        VBox{},
		DataBinder: DataBinder{
			AssignTo:   &db,
			Name:       "settings",
			DataSource: &buf,
		},
		Children: []Widget{
			GroupBox{
				Title:  "Параметры модели",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Режим:"},
					ComboBox{
						Value: Bind("ModelMode"),
						Model: modes,
					},

					Label{Text: "Температура:"},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							Slider{
								AssignTo: &tempSlider,
								MinValue: 0,
								MaxValue: 100,
								Value:    Bind("TempInt"),
								OnValueChanged: func() {
									val := float64(tempSlider.Value()) / 100.0
									tempLabel.SetText(fmt.Sprintf("%.2f", val))
								},
							},
							Label{
								AssignTo: &tempLabel,
								Text:     fmt.Sprintf("%.2f", settings.Temperature),
								MinSize:  Size{Width: 30},
							},
						},
					},
					Label{Text: "", ColumnSpan: 2},
					Label{
						Text:       "0.0 = Точный\n1.0 = Креативный",
						ColumnSpan: 2,
						TextColor:  walk.RGB(100, 100, 100),
					},
				},
			},

			GroupBox{
				Title:  "LLM Провайдер",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Провайдер:"},
					ComboBox{
						Value: Bind("LLMProvider"),
						Model: providers,
					},
				},
			},

			GroupBox{
				Title:  "Системный промпт",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{
						Text:    Bind("SystemPrompt"),
						VScroll: true,
						MinSize: Size{Height: 150},
					},
					Label{
						Text:      "Пример: 'Ты опытный Go разработчик.'",
						TextColor: walk.RGB(100, 100, 100),
					},
				},
			},

			VSpacer{},

			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo: &acceptPB,
						Text:     "OK",
						OnClicked: func() {
							if err := db.Submit(); err != nil {
								return
							}
							settings.SystemPrompt = buf.SystemPrompt
							settings.Temperature = float64(buf.TempInt) / 100.0
							settings.ModelMode = buf.ModelMode
							settings.LLMProvider = buf.LLMProvider

							dlg.Accept()
						},
					},
					PushButton{
						AssignTo:  &cancelPB,
						Text:      "Отмена",
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Run(owner) // <-- Теперь мы просто вызываем Run

	// Возвращаем true, если результат OK
	return result == walk.DlgCmdOK, err
}
