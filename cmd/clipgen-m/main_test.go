package main

import (
	"testing"
)

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
