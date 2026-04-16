# Telegram бот на Go

Бот умеет скачивать видео по ссылкам (YouTube, VK, Instagram, TikTok и др.) и присылать файл в чат. Написан на Go с [go-telegram/bot](https://github.com/go-telegram/bot).

## Требования

- Go 1.26+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — для скачивания видео (установи отдельно)
- **ffmpeg/ffprobe** (опционально) — для определения длительности видео; бот работает и без них
- Токен бота от [@BotFather](https://t.me/BotFather) в Telegram

### Установка yt-dlp

- **macOS:** `brew install yt-dlp`
- **Linux:** скачай бинарник с [релиза](https://github.com/yt-dlp/yt-dlp/releases) или `pip install yt-dlp`
- Кастомный путь: установи переменную `YT_DLP_BIN`

## Как получить токен

1. Открой Telegram и найди [@BotFather](https://t.me/BotFather)
2. Отправь команду `/newbot`
3. Введи имя и username бота
4. Скопируй выданный токен

## Запуск

```bash
go mod tidy

# Токен: положи в файл .env (он в .gitignore, в репозиторий не попадёт)
cp .env.example .env
# Отредактируй .env: вставь TELEGRAM_BOT_TOKEN и ADMIN_CHAT_ID

go run main.go
```

Либо задай переменные окружения:

```bash
TELEGRAM_BOT_TOKEN="твой_токен" ADMIN_CHAT_ID="123456789" go run main.go
```

Узнать свой Chat ID можно командой `/myid` после запуска бота.

## Переменные окружения

| Переменная | Обязательно | Описание |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | да | Токен от @BotFather |
| `ADMIN_CHAT_ID` | нет | Telegram ID админа (для админ-команд) |
| `BOT_DEBUG` | нет | Установи `1` для debug-лога |
| `YT_DLP_BIN` | нет | Кастомный путь к yt-dlp |

## Команды бота

- `/start` — приветствие
- `/help` — справка по командам
- `/hello` — персональное приветствие
- `/uptime` — время работы бота
- `/myid` — узнать свой Telegram ID
- **Ссылка на видео** — бот скачает ролик и пришлёт файлом

### Админ-команды (доступны только если задан `ADMIN_CHAT_ID`)

- `/stats` — статистика скачиваний
- `/status` — текущее состояние (активные скачивания, версия yt-dlp)
- `/shutdown` — остановить бота

### Ограничения

- Файлы больше 50 МБ Telegram не принимает — бот откажет
- До 3 одновременных скачиваний, остальные — «попробуй позже»
- Таймаут скачивания — 10 минут
- Качество: лучший MP4 до 50 МБ

## Сборка

```bash
go build -o telegram-bot
./telegram-bot
```

## Запуск в Docker на VPS

На сервере нужны только Docker и Docker Compose. yt-dlp и ffmpeg уже в образе.

```bash
git clone <url> telegram-bot && cd telegram-bot

# Создай .env с токеном и admin ID
echo 'TELEGRAM_BOT_TOKEN=твой_токен
ADMIN_CHAT_ID=123456789' > .env

# Сборка и запуск
docker-compose up -d --build

# Логи
docker compose logs -f bot

# Перезапуск
docker compose restart bot

# Остановка
docker compose down

# Обновить
git pull && docker compose down && docker-compose up -d --build
```

Версия yt-dlp пиннится в Dockerfile (`YT_DLP_VERSION`), обновляй вручную.
