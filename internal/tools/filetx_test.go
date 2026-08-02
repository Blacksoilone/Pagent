package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileTx_Should_StageChangesAndCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tx := NewFileTx()
	// 暂存一个写操作（不直接改文件）
	if err := tx.StageWrite(path, "new content"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// 暂存期间主工作区不变（可重复读）
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("main workspace changed during staging: %q", got)
	}

	// 提交：原子应用到主工作区
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "new content" {
		t.Errorf("after commit = %q, want new content", got)
	}
}

func TestFileTx_Should_CommitAtomically_WhenOneFails(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "ok.txt")
	// 目录占位：把 p1 的"父目录"先建成一个文件，使 MkdirAll 失败
	blocker := filepath.Join(dir, "blocked")
	os.WriteFile(blocker, []byte("im a file not dir"), 0o644)
	bad := filepath.Join(blocker, "x.txt") // 父路径是文件 → 写入失败

	tx := NewFileTx()
	_ = tx.StageWrite(p1, "good")
	_ = tx.StageWrite(bad, "x")

	err := tx.Commit()
	if err == nil {
		t.Fatal("expected commit error")
	}
	// 原子性：p1 不应被写入（全部回滚）
	if _, statErr := os.Stat(p1); statErr == nil {
		t.Error("p1 should not exist after failed atomic commit")
	}
}

func TestFileTx_Should_Discard_WhenRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tx := NewFileTx()
	_ = tx.StageWrite(path, "new")
	tx.Rollback()

	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("after rollback = %q, want old", got)
	}
}

func TestFileTx_Should_StageEdit_WithOriginalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tx := NewFileTx()
	if err := tx.StageEdit(EditFileArgs{FilePath: path, OldString: "world", NewString: "pagent"}); err != nil {
		t.Fatalf("stage edit: %v", err)
	}
	// 主工作区不变
	got, _ := os.ReadFile(path)
	if string(got) != "hello world" {
		t.Errorf("main workspace changed: %q", got)
	}
	// 提交后生效
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "hello pagent" {
		t.Errorf("after commit = %q, want hello pagent", got)
	}
}

func TestFileTx_Should_ReportDirtyFiles(t *testing.T) {
	tx := NewFileTx()
	_ = tx.StageWrite("/x/a.txt", "1")
	_ = tx.StageWrite("/x/b.txt", "2")

	files := tx.DirtyFiles()
	if len(files) != 2 {
		t.Errorf("dirty files = %v, want 2", files)
	}
}
