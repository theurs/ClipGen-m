# Доступ к конфигам и логам утилит

## Описание

В контекстное меню ClipGen-m были добавлены пункты для быстрого доступа к конфигурационным файлам и логам всех LLM-утилит.

## Доступные пункты меню

### Конфигурационные файлы:
- `Настройки (main)` - редактировать основной config.yaml
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

## Расположение файлов

Все конфигурационные файлы и логи хранятся в директории:
`%APPDATA%\clipgen-m\`

## Использование

1. Кликните по иконке ClipGen-m в системном трее
2. Выберите нужный пункт из меню
3. Файл откроется в редакторе, указанном в настройках (по умолчанию Notepad)