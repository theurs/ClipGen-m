# OpenAI-совместимый бэкенд (openailike)

Консольная утилита для **любого OpenAI-совместимого бэкенда**: сам OpenAI, Ollama, LM Studio, vLLM, OpenRouter, Together, Groq, LiteLLM или ваш собственный прокси. Адрес API задается в конфиге, поэтому один и тот же бинарник работает и с облачными сервисами, и с локальными моделями.

## Возможности

*   **Универсальная совместимость**: Работает с любым сервером, реализующим `POST /v1/chat/completions` и `GET /v1/models`.
*   **Локальные модели**: Ключ API не нужен (Ollama, LM Studio) — заголовок `Authorization` просто не отправляется.
*   **Авто-обнаружение моделей**: Если в конфиге модели не перечислены, они запрашиваются у сервера через `GET /v1/models`.
*   **Единые флаги**: Тот же интерфейс командной строки, что у `geminillm` и `groqllm` (см. [UNIFIED_FLAGS_RU.md](../../UNIFIED_FLAGS_RU.md)).
*   **Ротация ключей**: Несколько API-ключей с автоматическим переключением при ошибках `401`/`429`.
*   **Мультимодальность**: Изображения отправляются как `image_url` (data URI), текстовые файлы встраиваются в запрос, аудио транскрибируется через `/v1/audio/transcriptions`.
*   **Общая история чатов**: История хранится в общем каталоге `%AppData%\clipgen-m\llm_chats\`, поэтому диалог можно продолжить в другой утилите.

## Сборка

```bash
cd cmd/openailike
call build.bat
```

## Настройка

Файл конфигурации `%AppData%\clipgen-m\openai.conf` создается автоматически при первом запуске:

```json
{
  "api_keys": [
    "sk-..."
  ],
  "base_url": "https://api.openai.com/v1",
  "system_prompt": "Ты полезный ассистент.",
  "temperature": 0.6,
  "models": {
    "general": [],
    "code": [],
    "vision": [],
    "ocr": [],
    "audio": ["whisper-1"]
  },
  "chat_history_max_messages": 30
}
```

*   `base_url` — адрес OpenAI-совместимого API (без завершающего слеша).
*   `api_keys` — список ключей. Оставьте пустым (`[""]`) для локальных серверов без авторизации.
*   `models` — модели по режимам. Пустой список означает авто-обнаружение через `GET /v1/models`.

### Популярные бэкенды

| Бэкенд | `base_url` |
|---|---|
| OpenAI | `https://api.openai.com/v1` |
| Ollama | `http://localhost:11434/v1` |
| LM Studio | `http://localhost:1234/v1` |
| vLLM | `http://localhost:8000/v1` |
| OpenRouter | `https://openrouter.ai/api/v1` |
| Together | `https://api.together.xyz/v1` |
| Groq | `https://api.groq.com/openai/v1` |

### Сохранение ключа

```cmd
openailike.exe --save-key sk-xxxxxxxxxxxxxxxx
```

## Использование

```bash
# Простой запрос
echo "Как работает Go?" | openailike.exe

# С системным промптом и температурой
echo "Переведи на английский" | openailike.exe -s "Ты переводчик" -t 0.3

# JSON-режим
echo "Назови 3 факта о Марсе" | openailike.exe -j

# С изображением
openailike.exe -f "chart.png" -s "Опиши график"

# Чат с историей
echo "Меня зовут Алекс" | openailike.exe -chat session_1
echo "Как меня зовут?" | openailike.exe -chat session_1

# Список моделей на сервере
openailike.exe --list-models

# Конкретная модель
echo "Привет" | openailike.exe --model gpt-4o-mini

# Переопределить адрес для одного запуска
echo "Привет" | openailike.exe --base-url http://localhost:11434/v1
```

## Флаги

| Флаг | Описание |
|---|---|
| `-f` / `--file` | Прикрепить файл (можно указывать несколько раз) |
| `-s` / `--system` / `--system-prompt` | Системный промпт |
| `-j` / `--json` | JSON-режим ответа |
| `-m` / `--mode` | `auto`, `general`, `code`, `vision`, `ocr`, `audio` |
| `-t` / `--temp` / `--temperature` | Температура генерации |
| `-v` / `--verbose` | Подробный вывод в `stderr` |
| `--save-key` | Сохранить API-ключ в конфиг и выйти |
| `-chat` / `--chat-id` | ID чата (история) |
| `--base-url` / `--url` | Переопределить адрес API |
| `--model` | Использовать конкретную модель (приоритетнее конфига) |
| `--list-models` | Показать доступные на сервере модели |

## Файлы и логи

*   **Конфиг**: `%AppData%\clipgen-m\openai.conf`
*   **Лог ошибок**: `%AppData%\clipgen-m\openai_err.log`
*   **История чатов**: `%AppData%\clipgen-m\llm_chats\` (общая с другими модулями)

---

**Часть проекта ClipGen-m**
