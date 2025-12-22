// file: clipgen-m-chatui/main.go
package main

import (
	// Импортируем наш собственный пакет ui
	"clipgen-m-chatui/internal/ui"
)

func main() {
	// Инициализируем приложение с поддержкой иконки
	ui.Initialize()
	defer ui.Terminate()

	// Вся логика создания и запуска окна инкапсулирована
	// внутри пакета ui. Это делает main.go чистым.
	ui.CreateAndRunMainWindow()
}
