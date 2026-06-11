package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateUTF8(t *testing.T) {
	cases := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
		{strings.Repeat("я", 10), 7, strings.Repeat("я", 3)}, // 7 байт — середина 4-й буквы
		{strings.Repeat("👍", 3), 6, "👍"},                     // 6 байт — середина 2-го эмодзи
		{strings.Repeat("я", 5), 10, strings.Repeat("я", 5)}, // ровно по границе
	}
	for _, c := range cases {
		got := truncateUTF8(c.s, c.max)
		if got != c.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
		}
		if len(got) > c.max {
			t.Errorf("truncateUTF8(%q, %d): длина %d больше лимита", c.s, c.max, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%q, %d) = %q: невалидный UTF-8", c.s, c.max, got)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	got := truncateStr(strings.Repeat("я", 300), 500)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("got %q: ожидался валидный UTF-8 с многоточием", got)
	}
}

func TestExtractFirstURL(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"смотри (https://youtu.be/abc).", "https://youtu.be/abc"},
		{"http://a.b/c?d=e дальше текст", "http://a.b/c?d=e"},
		{"ссылок нет", ""},
		{"https://vk.com/video-1_2", "https://vk.com/video-1_2"},
	}
	for _, c := range cases {
		if got := extractFirstURL(c.text); got != c.want {
			t.Errorf("extractFirstURL(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestExtractYtDlpError(t *testing.T) {
	out := "WARNING: что-то\nERROR: Private video. Sign in\nостальное"
	if got := extractYtDlpError(out); got != "Private video. Sign in" {
		t.Errorf("got %q", got)
	}
	if got := extractYtDlpError("всё хорошо"); got != "" {
		t.Errorf("got %q, want пустую строку", got)
	}
}

func TestIsTransientErr(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("read tcp 1.2.3.4:443: connection reset by peer"), true},
		{errors.New("unexpected EOF"), true},
		{errors.New("Requested format is not available"), false},
	}
	for _, c := range cases {
		if got := isTransientErr(c.err); got != c.want {
			t.Errorf("isTransientErr(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{50 * 1024 * 1024, "50 MB"},
		{1 << 40, "1 TB"},
	}
	for _, c := range cases {
		if got := formatFileSize(c.n); got != c.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{65 * time.Second, "1:05"},
		{3661 * time.Second, "1:01:01"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestSuccessRate(t *testing.T) {
	cases := []struct {
		dl, errs, want int64
	}{
		{0, 0, 100},
		{10, 3, 70},
		{3, 3, 0},
	}
	for _, c := range cases {
		if got := successRate(c.dl, c.errs); got != c.want {
			t.Errorf("successRate(%d, %d) = %d, want %d", c.dl, c.errs, got, c.want)
		}
	}
}

func TestUserAllowed(t *testing.T) {
	oldAdmin, oldAllowed := adminChatID, allowedUserIDs
	t.Cleanup(func() { adminChatID, allowedUserIDs = oldAdmin, oldAllowed })

	adminChatID = 0
	allowedUserIDs = nil
	if !userAllowed(42) {
		t.Error("без белого списка должно быть разрешено всем")
	}

	allowedUserIDs = map[int64]struct{}{}
	if userAllowed(42) {
		t.Error("заданный, но пустой белый список должен закрывать доступ (fail closed)")
	}

	allowedUserIDs = map[int64]struct{}{5: {}}
	if !userAllowed(5) {
		t.Error("пользователь из списка должен быть разрешён")
	}
	if userAllowed(6) {
		t.Error("пользователь не из списка должен быть отклонён")
	}
	if userAllowed(0) {
		t.Error("неизвестный отправитель (id=0) должен быть отклонён")
	}

	adminChatID = 7
	if !userAllowed(7) {
		t.Error("админ должен быть разрешён даже вне списка")
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	lw := &lineWriter{emit: func(s string) { lines = append(lines, s) }}

	lw.Write([]byte("PROG:1|2|3|4\npar"))
	lw.Write([]byte("tial\r\nrest"))
	lw.flush()

	want := []string{"PROG:1|2|3|4", "partial", "rest"}
	if len(lines) != len(want) {
		t.Fatalf("получено %d строк (%q), ожидалось %d", len(lines), lines, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("строка %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestRotatingLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.log")
	w, err := newRotatingLogWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var orig bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&orig, "line %03d\n", i)
	}
	if _, err := w.Write(orig.Bytes()); err != nil {
		t.Fatal(err)
	}

	if err := w.rotateLocked(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) > orig.Len()/2+16 {
		t.Errorf("после ротации осталось %d байт из %d, ожидалась примерно половина", len(data), orig.Len())
	}
	if !bytes.HasSuffix(orig.Bytes(), data) {
		t.Error("после ротации содержимое должно быть суффиксом исходного")
	}
	if !bytes.HasPrefix(data, []byte("line ")) {
		t.Errorf("после ротации файл должен начинаться с границы строки, получено %q", data[:min(8, len(data))])
	}

	if _, err := w.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data2, []byte("after\n")) {
		t.Error("запись после ротации должна добавляться в конец файла")
	}
}
