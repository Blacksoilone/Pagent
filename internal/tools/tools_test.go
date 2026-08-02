package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ═══════════════ edit_file 精确替换 ═══════════════

func TestEditFile_Should_ReplaceExactString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\nfunc main() { println(\"hi\") }\n"), 0o644)

	err := EditFile(EditFileArgs{
		FilePath:  path,
		OldString: `println("hi")`,
		NewString: `println("hello world")`,
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `println("hello world")`) {
		t.Errorf("content after edit = %q", got)
	}
	if strings.Contains(string(got), `println("hi")`) {
		t.Errorf("old string still present: %q", got)
	}
}

func TestEditFile_Should_Error_WhenOldStringNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello"), 0o644)

	err := EditFile(EditFileArgs{
		FilePath:  path,
		OldString: "not here",
		NewString: "x",
	})
	if err == nil {
		t.Fatal("expected error when old string not found")
	}
	if !strings.Contains(err.Error(), "未找到") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestEditFile_Should_Error_WhenMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("dup dup dup"), 0o644)

	err := EditFile(EditFileArgs{
		FilePath:  path,
		OldString: "dup",
		NewString: "x",
	})
	if err == nil {
		t.Fatal("expected error on ambiguous match")
	}
	if !strings.Contains(err.Error(), "个匹配") {
		t.Errorf("error should mention multiple matches: %v", err)
	}
}

// ═══════════════ write_file 新建/覆盖 ═══════════════

func TestWriteFile_Should_CreateNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	err := WriteFile(WriteFileArgs{FilePath: path, Content: "hello"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestWriteFile_Should_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	err := WriteFile(WriteFileArgs{FilePath: path, Content: "new content"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new content" {
		t.Errorf("content = %q, want new content", got)
	}
}

// ═══════════════ 权限/边界 ═══════════════

func TestEditFile_Should_Error_WhenFileMissing(t *testing.T) {
	err := EditFile(EditFileArgs{
		FilePath:  filepath.Join(t.TempDir(), "missing.txt"),
		OldString: "x",
		NewString: "y",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteFile_Should_RejectPathTraversal(t *testing.T) {
	root := t.TempDir()
	// ../../ 逃逸到 root 外
	err := WriteFileWithin(WriteFileArgs{
		FilePath: filepath.Join(root, "..", "escape.txt"),
		Content:  "x",
	}, root)
	if err == nil {
		t.Fatal("expected error for path escape")
	}
	if !strings.Contains(err.Error(), "超出") {
		t.Errorf("error should mention escape: %v", err)
	}
}
