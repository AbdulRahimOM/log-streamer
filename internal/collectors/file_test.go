package collectors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailFileStartsAtEOFAndFollowsAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("old line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 4)
	opened := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- tailFile(ctx, path, 5*time.Millisecond, 128, func() { opened <- struct{}{} }, func(line string, _ time.Time) { lines <- line })
	}()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("file tail did not open")
	}
	appendFile(t, path, "new line\n")
	select {
	case line := <-lines:
		if line != "new line" {
			t.Fatalf("received %q, want only appended line", line)
		}
	case <-time.After(time.Second):
		t.Fatal("appended line was not read")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTailFileHandlesTruncationAndReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 8)
	opened := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- tailFile(ctx, path, 5*time.Millisecond, 128, func() { opened <- struct{}{} }, func(line string, _ time.Time) { lines <- line })
	}()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("file tail did not open")
	}
	waitForFileLine(t, lines, func() { appendFile(t, path, "before truncate\n") }, "before truncate")
	if err := os.WriteFile(path, []byte("after truncate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, lines, "after truncate")
	if err := os.Rename(path, filepath.Join(dir, "app.log.1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement first line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, lines, "replacement first line")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTailFileDrainsOversizedLineAndContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 4)
	opened := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- tailFile(ctx, path, 5*time.Millisecond, 4, func() { opened <- struct{}{} }, func(line string, _ time.Time) { lines <- line })
	}()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("file tail did not open")
	}
	appendFile(t, path, "too long\nok\n")
	waitForLine(t, lines, "ok")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTailFileReportsUnavailablePath(t *testing.T) {
	err := tailFile(context.Background(), filepath.Join(t.TempDir(), "missing.log"), time.Millisecond, 10, func() {}, func(string, time.Time) {})
	if err == nil {
		t.Fatal("expected missing file error")
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case got := <-lines:
		if got != want {
			t.Fatalf("line=%q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func waitForFileLine(t *testing.T, lines <-chan string, write func(), want string) {
	t.Helper()
	write()
	waitForLine(t, lines, want)
}
