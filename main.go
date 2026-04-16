package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	maxFileSizeMB          = 50
	maxCaptionLen          = 1024
	downloadTimeout        = 10 * time.Minute
	maxConcurrentDownloads = 3
	maxLogSize             = 5 * 1024 * 1024
)

var (
	urlRegex     = regexp.MustCompile(`https?://[^\s]+`)
	startTime    time.Time
	adminChatID  int64
	shutdownFunc context.CancelFunc
	logPath      = "/tmp/bot.log"

	statsDownloads int64
	statsErrors    int64
)

type downloadResult struct {
	filePath string
	duration time.Duration
	width    int
	height   int
}

type videoInfo struct {
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	Height   int     `json:"height"`
	Width    int     `json:"width"`
}

var downloadSem = make(chan struct{}, maxConcurrentDownloads)

type rotatingLogWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func newRotatingLogWriter(path string) (*rotatingLogWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &rotatingLogWriter{file: f, path: path}, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	info, err := w.file.Stat()
	if err != nil {
		return w.file.Write(p)
	}

	if info.Size()+int64(len(p)) > maxLogSize {
		w.file.Close()
		data, err := os.ReadFile(w.path)
		if err != nil {
			w.file, _ = os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			return w.file.Write(p)
		}
		keep := len(data) / 2
		for keep < len(data) && data[keep] != '\n' {
			keep++
		}
		if keep < len(data) {
			keep++
		}
		w.file, _ = os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		w.file.Write(data[keep:])
	}

	return w.file.Write(p)
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func loadEnvFile() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)

		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func ytDlpBin() string {
	if bin := os.Getenv("YT_DLP_BIN"); bin != "" {
		return bin
	}
	return "yt-dlp"
}

func isAdmin(userID int64) bool {
	return adminChatID != 0 && userID == adminChatID
}

func currentDownloads() int {
	return len(downloadSem)
}

func main() {
	loadEnvFile()

	logWriter, err := newRotatingLogWriter(logPath)
	if err != nil {
		log.Fatalf("Ошибка создания лог-файла: %v", err)
	}
	defer logWriter.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logWriter))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("Установите TELEGRAM_BOT_TOKEN в файле .env или в переменной окружения")
	}

	adminChatID, _ = strconv.ParseInt(os.Getenv("ADMIN_CHAT_ID"), 10, 64)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownFunc = cancel

	opts := []bot.Option{
		bot.WithDefaultHandler(defaultHandler),
		bot.WithWorkers(10),
	}

	if os.Getenv("BOT_DEBUG") == "1" {
		opts = append(opts, bot.WithDebug())
	}

	b, err := bot.New(token, opts...)
	if err != nil {
		log.Fatalf("Ошибка создания бота: %v", err)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, startCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, helpCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/myid", bot.MatchTypeExact, myidCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/uptime", bot.MatchTypeExact, uptimeCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, statsCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypeExact, statusCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/shutdown", bot.MatchTypeExact, shutdownCmd)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/log", bot.MatchTypeExact, logCmd)

	startTime = time.Now()
	log.Printf("Бот запущен (admin=%d, yt-dlp=%s)", adminChatID, ytDlpVersion())

	b.Start(ctx)
	log.Println("Бот остановлен")
}

func senderID(update *models.Update) int64 {
	if update.Message != nil && update.Message.From != nil {
		return update.Message.From.ID
	}
	return 0
}

func senderName(update *models.Update) string {
	if update.Message != nil && update.Message.From != nil {
		name := update.Message.From.FirstName
		if update.Message.From.LastName != "" {
			name += " " + update.Message.From.LastName
		}
		return name
	}
	return "unknown"
}

func startCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[cmd] /start от %s (id=%d)", senderName(update), senderID(update))
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Привет! Я умею скачивать видео. Отправь ссылку на YouTube, VK, Instagram, TikTok или другой сайт — пришлю видео.\n\n/help — справка",
	})
}

func helpCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	text := "Доступные команды:\n/help — справка\n/myid — узнать свой Telegram ID\n/start — приветствие\n\nИли отправь ссылку на видео — скачаю и пришлю файл."
	if isAdmin(senderID(update)) {
		text += "\n\nАдмин-команды:\n/log — получить лог-файл\n/shutdown — остановить бота\n/stats — статистика скачиваний\n/status — текущее состояние\n/uptime — время работы бота"
	}
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   text,
	})
}

func uptimeCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !isAdmin(senderID(update)) {
		return
	}
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   formatUptime(time.Since(startTime)),
	})
}

func myidCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[cmd] /myid от %s (id=%d)", senderName(update), senderID(update))
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Твой Telegram ID: %d", senderID(update)),
	})
}

func statsCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !isAdmin(senderID(update)) {
		return
	}
	dl := atomic.LoadInt64(&statsDownloads)
	errs := atomic.LoadInt64(&statsErrors)
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text: fmt.Sprintf("Статистика:\nСкачиваний: %d\nОшибок: %d\nУспешность: %d%%\nВремя работы: %s",
			dl, errs, successRate(dl, errs), formatUptimeShort(time.Since(startTime))),
	})
}

func statusCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !isAdmin(senderID(update)) {
		return
	}
	active := currentDownloads()
	ytVer := ytDlpVersion()
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text: fmt.Sprintf("Состояние:\nАктивных скачиваний: %d/%d\nyt-dlp: %s\nUptime: %s",
			active, maxConcurrentDownloads, ytVer, formatUptimeShort(time.Since(startTime))),
	})
}

func shutdownCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !isAdmin(senderID(update)) {
		return
	}
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Бот останавливается…",
	})
	log.Println("Запрошена остановка через /shutdown")
	shutdownFunc()
}

func logCmd(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !isAdmin(senderID(update)) {
		return
	}
	log.Printf("[cmd] /log запрошен админом")

	content, err := os.ReadFile(logPath)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Ошибка чтения лога: %v", err),
		})
		return
	}

	if len(content) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Лог-файл пуст.",
		})
		return
	}

	filename := fmt.Sprintf("bot-%s.log", time.Now().Format("2006-01-02_15-04-05"))
	_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   update.Message.Chat.ID,
		Document: &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(content)},
		Caption:  fmt.Sprintf("Лог бота (%s)", formatFileSize(int64(len(content)))),
	})
	if err != nil {
		log.Printf("Ошибка отправки лога: %v", err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Не удалось отправить лог-файл: %v", err),
		})
	}
}

func defaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	text := strings.TrimSpace(update.Message.Text)
	if text == "" {
		return
	}

	if url := extractFirstURL(text); url != "" {
		log.Printf("[url] %s запросил скачивание: %s", senderName(update), url)
		go handleVideoURL(ctx, b, update.Message.Chat.ID, url)
		return
	}

	log.Printf("[msg] от %s (id=%d): %q", senderName(update), senderID(update), text)
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Отправь ссылку на видео (YouTube, VK, Instagram, TikTok и др.) — я скачаю и пришлю файл. Или используй /help.",
	})
}

func successRate(dl, errs int64) int64 {
	if dl == 0 {
		return 100
	}
	return (dl - errs) * 100 / dl
}

func ytDlpVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ytDlpBin(), "--version").Output()
	if err != nil {
		return "неизвестно"
	}
	return strings.TrimSpace(string(out))
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("Бот работает: %dч %dм %dс", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("Бот работает: %dм %dс", m, s)
	}
	return fmt.Sprintf("Бот работает: %dс", s)
}

func formatUptimeShort(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dч %dм", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dм %dс", m, s)
	}
	return fmt.Sprintf("%dс", s)
}

func extractFirstURL(text string) string {
	raw := urlRegex.FindString(text)
	raw = strings.TrimRight(raw, ".,;:!?)")
	return raw
}

func handleVideoURL(ctx context.Context, b *bot.Bot, chatID int64, url string) {
	select {
	case downloadSem <- struct{}{}:
	default:
		log.Printf("[download] отклонён: semaphore полон (url=%s)", url)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Слишком много одновременных скачиваний, попробуй позже.",
		})
		return
	}
	defer func() { <-downloadSem }()

	log.Printf("[download] начало: url=%s", url)

	statusMsg, _ := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Скачиваю видео…",
	})

	dlStart := time.Now()
	result, err := downloadVideo(url)
	downloadDuration := time.Since(dlStart)

	atomic.AddInt64(&statsDownloads, 1)

	if err != nil {
		atomic.AddInt64(&statsErrors, 1)
		log.Printf("[download] ошибка: url=%s за=%s err=%v", url, downloadDuration.Round(time.Millisecond), err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Не удалось скачать видео. Проверь ссылку и доступность ролика (приватное, регион и т.д.).",
		})
		if statusMsg != nil {
			b.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    chatID,
				MessageID: statusMsg.ID,
			})
		}
		return
	}

	defer os.RemoveAll(filepath.Dir(result.filePath))

	if statusMsg != nil {
		b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatID,
			MessageID: statusMsg.ID,
		})
	}

	info, err := os.Stat(result.filePath)
	if err != nil {
		log.Printf("[download] ошибка stat: url=%s err=%v", url, err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Не удалось получить информацию о файле.",
		})
		return
	}

	log.Printf("[download] файл: url=%s name=%s size=%s duration=%s за=%s",
		url, filepath.Base(result.filePath), formatFileSize(info.Size()),
		formatDuration(result.duration), downloadDuration.Round(time.Millisecond))

	if info.Size() > maxFileSizeMB*1024*1024 {
		log.Printf("[download] файл слишком большой: url=%s size=%s", url, formatFileSize(info.Size()))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Файл получился больше 50 МБ — Telegram не примет такой размер. Попробуй другую ссылку или качество.",
		})
		return
	}

	duration := result.duration
	if duration == 0 {
		duration = getVideoDuration(result.filePath)
	}

	filename := filepath.Base(result.filePath)
	sizeStr := formatFileSize(info.Size())
	durationStr := formatDuration(duration)
	downloadTimeStr := formatDuration(downloadDuration)

	resStr := ""
	if result.width > 0 && result.height > 0 {
		resStr = fmt.Sprintf("\n🖥 Разрешение: %dx%d", result.width, result.height)
	}

	caption := fmt.Sprintf("📹 %s\n⏱ Длительность: %s%s\n💾 Размер: %s\n⚡ Обработано за: %s",
		filename, durationStr, resStr, sizeStr, downloadTimeStr)
	if len(caption) > maxCaptionLen {
		caption = caption[:maxCaptionLen]
	}

	fileContent, err := os.ReadFile(result.filePath)
	if err != nil {
		log.Printf("[download] ошибка чтения файла: url=%s err=%v", url, err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Не удалось прочитать скачанный файл.",
		})
		return
	}

	_, sendErr := b.SendVideo(ctx, &bot.SendVideoParams{
		ChatID:  chatID,
		Video:   &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(fileContent)},
		Caption: caption,
	})

	if sendErr != nil {
		log.Printf("[send] видео не ушло: url=%s err=%v, пробую документом", url, sendErr)
		_, docErr := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:   chatID,
			Document: &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(fileContent)},
			Caption:  caption,
		})
		if docErr != nil {
			log.Printf("[send] документ не ушёл: url=%s err=%v", url, docErr)
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   "Скачал файл, но не получилось отправить.",
			})
		}
	} else {
		log.Printf("[send] успешно: url=%s size=%s", url, sizeStr)
	}
}

func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div := int64(unit)
	exp := 0
	for bytes >= div*unit && exp < 3 {
		div *= unit
		exp++
	}

	whole := bytes / div
	frac := (bytes % div) * 10 / div
	units := []string{"KB", "MB", "GB", "TB"}

	if frac > 0 {
		return fmt.Sprintf("%d.%d %s", whole, frac, units[exp])
	}
	return fmt.Sprintf("%d %s", whole, units[exp])
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func getVideoDuration(filePath string) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[ffprobe] ошибка: file=%s err=%v", filePath, err)
		return 0
	}

	var seconds float64
	if _, err := fmt.Sscanf(string(output), "%f", &seconds); err != nil {
		log.Printf("[ffprobe] ошибка парсинга: file=%s output=%q err=%v", filePath, strings.TrimSpace(string(output)), err)
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}

func downloadVideo(url string) (*downloadResult, error) {
	ytDlp := ytDlpBin()

	dir, err := os.MkdirTemp("", "tg-video-*")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	log.Printf("[yt-dlp] получение информации: url=%s", url)

	cmdInfo := exec.CommandContext(ctx, ytDlp,
		"--no-playlist",
		"--dump-json",
		"--no-warnings",
		"--no-call-home",
		url,
	)
	infoOutput, err := cmdInfo.Output()
	if err != nil {
		os.RemoveAll(dir)
		log.Printf("[yt-dlp] dump-json ошибка: url=%s err=%v", url, err)
		return nil, fmt.Errorf("не удалось получить информацию о видео: %v", err)
	}

	var vi videoInfo
	if err := json.Unmarshal(infoOutput, &vi); err != nil {
		log.Printf("[yt-dlp] парсинг JSON ошибка: url=%s err=%v", url, err)
		vi.Title = ""
		vi.Duration = 0
	}

	log.Printf("[yt-dlp] информация: url=%s title=%q duration=%.1fs", url, vi.Title, vi.Duration)

	title := vi.Title
	if title == "" {
		title = "video"
	}
	title = sanitizeFilename(title)

	var duration time.Duration
	if vi.Duration > 0 {
		duration = time.Duration(vi.Duration * float64(time.Second))
	}

	formats := []string{
		"best[ext=mp4][filesize<50M]/best[filesize<50M]",
		"best[ext=mp4][height<=720][filesize<50M]/best[height<=720][filesize<50M]",
		"best[ext=mp4][height<=480]/best[height<=480]",
		"best[ext=mp4][height<=360]/best[height<=360]",
		"best[ext=mp4]/best",
	}

	var lastErr error
	for i, format := range formats {
		os.RemoveAll(dir)
		dir, _ = os.MkdirTemp("", "tg-video-*")
		outPath := filepath.Join(dir, "%(title).100s.%(ext)s")

		log.Printf("[yt-dlp] попытка %d/%d: url=%s format=%s", i+1, len(formats), url, format)

		cmd := exec.CommandContext(ctx, ytDlp,
			"--no-playlist",
			"-f", format,
			"--merge-output-format", "mp4",
			"--max-filesize", "50M",
			"-o", outPath,
			"--no-warnings",
			"--no-call-home",
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; rv:131.0) Gecko/20100101 Firefox/131.0",
			url,
		)
		cmd.Dir = dir

		out, err := cmd.CombinedOutput()
		if err != nil {
			raw := strings.TrimSpace(string(out))
			log.Printf("[yt-dlp] попытка %d неудача: url=%s format=%s err=%v вывод=%q", i+1, url, format, err, truncateStr(raw, 500))
			lastErr = err
			continue
		}

		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				fi, _ := e.Info()
				log.Printf("[yt-dlp] попытка %d успех: url=%s format=%s file=%s size=%s",
					i+1, url, format, e.Name(), formatFileSize(fi.Size()))
				return &downloadResult{filePath: filepath.Join(dir, e.Name()), duration: duration, width: vi.Width, height: vi.Height}, nil
			}
		}

		log.Printf("[yt-dlp] попытка %d: файл не найден в dir (url=%s)", i+1, url)
	}

	os.RemoveAll(dir)
	if lastErr != nil {
		return nil, fmt.Errorf("ошибка скачивания (все качества)")
	}
	return nil, os.ErrNotExist
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func sanitizeFilename(name string) string {
	result := strings.Map(func(r rune) rune {
		if isFilenameSafe(r) {
			return r
		}
		return -1
	}, name)
	result = strings.TrimSpace(result)
	if result == "" {
		return "video"
	}
	return result
}

func isFilenameSafe(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == ' ' || r == '-' || r == '_' || r == '.' ||
		(unicode.IsLetter(r) && unicode.IsPrint(r))
}
