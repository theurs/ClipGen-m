# ClipGen-M - Универсальный интерфейс для LLM

[![Go Version](https://img.shields.io/github/go-mod/go-version/Vladimir37/clipgen-m)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows-blue)]()

## Описание

ClipGen-M - это набор утилит для взаимодействия с различными LLM (Large Language Models), включая Mistral, Google Gemini, GitHub Copilot и Groq. Проект предоставляет унифицированный интерфейс командной строки для работы с различными моделями искусственного интеллекта.

## Особенности

- **Унифицированный интерфейс**: Все утилиты используют одинаковые флаги командной строки
- **Поддержка мультимедиа**: Работа с изображениями, аудио, PDF и текстовыми файлами
- **Поддержка чатов**: Сохранение истории чатов и контекста
- **Автоматическая конвертация аудио**: Поддержка редких форматов (AMR и др.) через ffmpeg
- **Графический интерфейс**: Встроенный ChatUI для удобной работы
- **Системный трей**: Управление через иконку в трее Windows

## Структура проекта

- `cmd/clipgen-m` - основное приложение с системным треем
- `cmd/chatui` - графический интерфейс для чатов
- `cmd/geminillm` - утилита для Google Gemini
- `cmd/ghllm` - утилита для GitHub Copilot
- `cmd/groqllm` - утилита для Groq
- `cmd/mistral` - утилита для Mistral (оригинальная)

## Унифицированные флаги командной строки

Все LLM-утилиты поддерживают унифицированный набор флагов с поддержкой как одинарных (-), так и двойных (--) дефисов:

- `-f` / `--f` / `--file` - файлы (поддерживаются изображения, аудио и текстовые файлы)
- `-s` / `--s` / `--system` / `--system-prompt` - системный промпт
- `-j` / `--j` / `--json` - JSON режим
- `-m` / `--m` / `--mode` - режим модели (auto, general, code, ocr, audio, vision)
- `-t` / `--t` / `--temp` / `--temperature` - температура
- `-v` / `--v` / `--verbose` - подробный вывод
- `--save-key` - сохранение ключа
- `-chat` / `--chat` / `--chat-id` - ID чата

## Быстрый доступ к конфигам и логам

В контекстное меню ClipGen-m добавлены пункты для быстрого доступа:

### Конфигурационные файлы:
- `Настройки (main)` - редактировать config.yaml
- `Mistral Config` - редактировать mistral.conf
- `Tavily Config` - редактировать tavily.conf
- `Geminillm Config` - редактировать gemini.conf
- `Ghllm Config` - редактировать github.conf
- `Groqllm Config` - редактировать groq.conf

### Файлы логов:
- `Mistral Log` - просмотр mistral_err.log
- `Geminillm Log` - просмотр gemini_err.log
- `Ghllm Log` - просмотр github_err.log
- `Groqllm Log` - просмотр groq_err.log
- `ClipGen Log` - просмотр ошибок программы

## Сборка

Каждая утилита имеет собственный `build.bat` файл для простой сборки:

- `mistral/build.bat`
- `geminillm/build.bat`
- `ghllm/build.bat`
- `groqllm/build.bat`
- `clipgen-m/build.bat`
- `chatui/build.bat`

Для сборки выполните:
```
cd cmd\[утилита]
call build.bat
```

## Использование

Все утилиты принимают входные данные через stdin и поддерживают работу с файлами, изображениями и аудио (в зависимости от модели и API возможностей).

Поддержка файлов:
- `mistral.exe` - поддерживает изображения, аудио, текстовые файлы, PDF (через OCR)
- `geminillm.exe` - поддерживает изображения, текстовые файлы, аудио (включая автоматическую конвертацию неподдерживаемых форматов с помощью ffmpeg)
- `ghllm.exe` - поддерживает изображения, текстовые файлы, аудио
- `groqllm.exe` - поддерживает изображения, аудио, текстовые файлы

Примеры:
```
echo "Привет" | mistral.exe --system "Ты помощник" --temperature 0.7
echo "Привет" | geminillm.exe --system "Ты помощник" --temperature 0.7
echo "Привет" | ghllm.exe --mode general --json
echo "Привет" | groqllm.exe --chat mychat --temperature 0.5
```

## Расположение файлов

Конфигурационные файлы и логи хранятся в:
`%APPDATA%\clipgen-m\`

## Требования

- Windows 10/11
- Go 1.25+
- (Опционально) FFmpeg для поддержки редких аудио форматов

## Вклад в проект

См. [CONTRIBUTING.md](CONTRIBUTING.md) для подробной информации о том, как внести свой вклад в проект.

## Кодекс поведения

См. [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) для ознакомления с нашими правилами поведения.

## Лицензия

Этот проект лицензирован под [MIT License](LICENSE). См. файл [LICENSE](LICENSE) для получения дополнительной информации.

## Авторы

См. [AUTHORS](AUTHORS) для списка участников проекта.