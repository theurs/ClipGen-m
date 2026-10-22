package main

import (
	"testing"
	"time"

	"golang.design/x/clipboard"
)

// TestClipboardSnapshotRestore проверяет, что содержимое буфера обмена
// можно сохранить и вернуть без потерь (сценарий смены раскладки).
func TestClipboardSnapshotRestore(t *testing.T) {
	if err := clipboard.Init(); err != nil {
		t.Skipf("буфер обмена недоступен: %v", err)
	}

	// Сохраняем пользовательский буфер и возвращаем его после теста.
	userSnapshot, err := captureClipboardSnapshot()
	if err != nil {
		t.Skipf("не удалось прочитать буфер обмена: %v", err)
	}
	defer func() {
		if err := restoreClipboardSnapshot(userSnapshot); err != nil {
			t.Logf("не удалось вернуть буфер обмена пользователя: %v", err)
		}
	}()

	const original = "ClipGen-m test clipboard content"

	clipboard.Write(clipboard.FmtText, []byte(original))
	time.Sleep(50 * time.Millisecond)

	snapshot, err := captureClipboardSnapshot()
	if err != nil {
		t.Fatalf("ошибка снимка буфера: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatal("снимок буфера пуст")
	}

	// Имитируем вставку переведенного текста — буфер меняется.
	clipboard.Write(clipboard.FmtText, []byte("Ghbdtn"))
	time.Sleep(50 * time.Millisecond)

	if err := restoreClipboardSnapshot(snapshot); err != nil {
		t.Fatalf("ошибка восстановления буфера: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	got := string(clipboard.Read(clipboard.FmtText))
	if got != original {
		t.Errorf("буфер обмена не восстановлен: получено %q, ожидалось %q", got, original)
	}
}

// TestLayoutSwitchKeepsClipboard проверяет сценарий смены раскладки целиком:
// буфер снимается ДО копирования, переведенный текст попадает в буфер при вставке,
// а после вставки буфер возвращается к исходному содержимому.
func TestLayoutSwitchKeepsClipboard(t *testing.T) {
	if err := clipboard.Init(); err != nil {
		t.Skipf("буфер обмена недоступен: %v", err)
	}

	// Сохраняем пользовательский буфер и возвращаем его после теста.
	userSnapshot, err := captureClipboardSnapshot()
	if err != nil {
		t.Skipf("не удалось прочитать буфер обмена: %v", err)
	}
	defer func() {
		if err := restoreClipboardSnapshot(userSnapshot); err != nil {
			t.Logf("не удалось вернуть буфер обмена пользователя: %v", err)
		}
	}()

	const original = "ORIGINAL-CLIPBOARD-CONTENT"

	clipboard.Write(clipboard.FmtText, []byte(original))
	time.Sleep(50 * time.Millisecond)

	// Шаг 1: снимок буфера до копирования выделения.
	snapshot, err := captureClipboardSnapshot()
	if err != nil {
		t.Fatalf("ошибка снимка буфера: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatal("снимок буфера пуст")
	}

	// Шаг 2: буфер меняется (копирование выделения и вставка перевода).
	clipboard.Write(clipboard.FmtText, []byte("Ghbdtn"))
	time.Sleep(50 * time.Millisecond)

	// Шаг 3: восстановление исходного содержимого после вставки.
	restoreClipboardAfterPaste(snapshot, true, true)

	got := string(clipboard.Read(clipboard.FmtText))
	if got != original {
		t.Errorf("буфер обмена не восстановлен: получено %q, ожидалось %q", got, original)
	}
}

func TestSwitchLayoutPureRussian(t *testing.T) {
	// Каждое слово кириллицы переводится в латиницу по раскладке ЙЦУКЕН.
	got := switchLayout("привет мир")
	want := "ghbdtn vbh"
	if got != want {
		t.Errorf("switchLayout(%q) = %q, ожидалось %q", "привет мир", got, want)
	}
}

func TestSwitchLayoutPureLatin(t *testing.T) {
	got := switchLayout("hello world")
	want := "руддщ цщкдв"
	if got != want {
		t.Errorf("switchLayout(%q) = %q, ожидалось %q", "hello world", got, want)
	}
}

func TestSwitchLayoutMixedText(t *testing.T) {
	// Смешанный текст: каждое слово конвертируется независимо.
	got := switchLayout("Привет, мир! Hello")
	want := "Ghbdtn, vbh! Руддщ"
	if got != want {
		t.Errorf("switchLayout(%q) = %q, ожидалось %q", "Привет, мир! Hello", got, want)
	}
}

func TestSwitchLayoutKeepsSeparators(t *testing.T) {
	// Пробелы, переводы строк, знаки препинания и цифры остаются без изменений.
	in := "Привет, мир!\nSecond line - 100% (done) [ok]"
	got := switchLayout(in)
	want := "Ghbdtn, vbh!\nЫусщтв дшту - 100% (вщту) [щл]"
	if got != want {
		t.Errorf("switchLayout(%q) = %q, ожидалось %q", in, got, want)
	}
}

func TestSwitchLayoutEmpty(t *testing.T) {
	if got := switchLayout(""); got != "" {
		t.Errorf("switchLayout(\"\") = %q, ожидалось пустую строку", got)
	}
}
