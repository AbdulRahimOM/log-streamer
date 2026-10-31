package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLocalDisabledSkipsDotenv(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	if err := os.WriteFile(".env", []byte("invalid line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadLocal(false); err != nil {
		t.Fatalf("LoadLocal(false): %v", err)
	}
}

func TestLoadLocalEnabledRequiresFileAndLoadsWithPrecedence(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	if err := LoadLocal(true); err == nil {
		t.Fatal("LoadLocal(true) accepted a missing .env")
	}
	if err := os.Mkdir(".env", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := LoadLocal(true); err == nil {
		t.Fatal("LoadLocal(true) accepted an unreadable .env path")
	}
	if err := os.Remove(".env"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("LOGSTREAMER_ENVFILE_PROCESS=file\nLOGSTREAMER_ENVFILE_FILE=file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGSTREAMER_ENVFILE_PROCESS", "process-value")
	t.Setenv("LOGSTREAMER_ENVFILE_FILE", "")
	if err := LoadLocal(true); err != nil {
		t.Fatalf("LoadLocal(true): %v", err)
	}
	if got := os.Getenv("LOGSTREAMER_ENVFILE_PROCESS"); got != "process-value" {
		t.Fatalf("process value=%q, want process-value", got)
	}
	if got := os.Getenv("LOGSTREAMER_ENVFILE_FILE"); got != "file-value" {
		t.Fatalf("file value=%q, want file-value", got)
	}
	if err := os.WriteFile(".env", []byte("invalid line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadLocal(true); err == nil {
		t.Fatal("LoadLocal(true) accepted invalid .env syntax")
	}
}

func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Clean(dir)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
