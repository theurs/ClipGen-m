package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func main() {
	// Настройки аргументов командной строки
	// По умолчанию сканирует текущую папку и сохраняет в context.txt
	rootDirPtr := flag.String("dir", ".", "Папка для сканирования")
	outputFilePtr := flag.String("out", "context.txt", "Имя выходного файла")
	flag.Parse()

	rootDir := *rootDirPtr
	outputFile := *outputFilePtr

	// Открываем (или создаем) файл для записи результата
	f, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Ошибка создания файла %s: %v\n", outputFile, err)
		return
	}
	defer f.Close()

	// 1. Записываем заголовок и дерево файлов
	fmt.Println("Генерация дерева файлов...")
	f.WriteString("PROJECT STRUCTURE:\n")
	f.WriteString(".\n")

	// Строим визуальное дерево
	if err := writeTree(f, rootDir, "", outputFile); err != nil {
		fmt.Printf("Ошибка при построении дерева: %v\n", err)
	}

	f.WriteString("\n\n")

	// 2. Читаем контент файлов и записываем его
	fmt.Println("Сбор содержимого файлов...")
	err = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Пропускаем директории (они уже в дереве)
		if d.IsDir() {
			// Игнорируем .git
			if d.Name() == ".git" || d.Name() == ".idea" || d.Name() == ".vscode" {
				return filepath.SkipDir
			}
			return nil
		}

		// Пропускаем сам выходной файл, чтобы не читать то, что пишем
		if filepath.Base(path) == outputFile {
			return nil
		}

		// Пропускаем бинарники (картинки, exe и т.д.), если нужно
		// Здесь простая проверка по расширению, можно расширить
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".exe" || ext == ".dll" || ext == ".png" || ext == ".jpg" || ext == ".zip" {
			return nil
		}

		// Читаем файл
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("ошибка чтения %s: %v", path, err)
		}

		// Простая проверка: если файл не текстовый (содержит "битые" utf8), лучше пропустить
		if !utf8.Valid(content) {
			return nil
		}

		// Формируем путь относительно корня сканирования для красоты
		relPath, _ := filepath.Rel(rootDir, path)

		// Записываем разделитель и имя файла
		separator := strings.Repeat("=", 80)
		header := fmt.Sprintf("%s\nFILE: %s\n%s\n", separator, relPath, separator)

		if _, err := f.WriteString(header); err != nil {
			return err
		}

		// Записываем контент
		if _, err := f.Write(content); err != nil {
			return err
		}

		// Добавляем отступ после файла
		f.WriteString("\n\n")

		return nil
	})

	if err != nil {
		fmt.Printf("Ошибка при обходе файлов: %v\n", err)
	} else {
		fmt.Printf("Готово! Результат сохранен в %s\n", outputFile)
	}
}

// writeTree рекурсивно строит дерево папок и записывает в writer
func writeTree(w *os.File, dir string, prefix string, ignoreFile string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Фильтруем список, убирая .git и выходной файл
	var filtered []fs.DirEntry
	for _, e := range entries {
		if e.Name() == ".git" || e.Name() == ".idea" || e.Name() == ".vscode" || e.Name() == ignoreFile {
			continue
		}
		filtered = append(filtered, e)
	}

	for i, entry := range filtered {
		isLast := i == len(filtered)-1

		connector := "├── "
		if isLast {
			connector = "└── "
		}

		line := prefix + connector + entry.Name() + "\n"
		w.WriteString(line)

		if entry.IsDir() {
			newPrefix := prefix
			if isLast {
				newPrefix += "    "
			} else {
				newPrefix += "│   "
			}
			// Рекурсивный вызов для подпапок
			err := writeTree(w, filepath.Join(dir, entry.Name()), newPrefix, ignoreFile)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
