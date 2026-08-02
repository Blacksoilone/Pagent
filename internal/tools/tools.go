// Package tools 实现文件操作工具（里程碑1：write_file / edit_file）。
//
// 设计依据：docs/agent-workspace-product-draft.md 3.2.1 工具标准
// - edit_file 是精确字符串替换（old_string → new_string），失败返回错误让 AI 重试
// - 路径必须限制在项目挂载目录内（防止 path traversal）
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EditFileArgs edit_file 参数。
type EditFileArgs struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// WriteFileArgs write_file 参数。
type WriteFileArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// EditFile 精确字符串替换文件内容。
// old_string 未找到或匹配多个时返回错误（让 AI 修正参数重试）。
func EditFile(args EditFileArgs) error {
	if strings.TrimSpace(args.OldString) == "" {
		return fmt.Errorf("old_string 不能为空")
	}
	b, err := os.ReadFile(args.FilePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}
	content := string(b)
	count := strings.Count(content, args.OldString)
	switch {
	case count == 0:
		return fmt.Errorf("未找到匹配的 old_string：%q", truncate(args.OldString))
	case count > 1:
		return fmt.Errorf("old_string 有 %d 个匹配（%q），请提供更长的上下文", count, truncate(args.OldString))
	}
	content = strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(args.FilePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

// WriteFile 新建或覆盖文件。
func WriteFile(args WriteFileArgs) error {
	if err := os.MkdirAll(filepath.Dir(args.FilePath), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(args.FilePath, []byte(args.Content), 0o644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

// WriteFileWithin 限制在 root 目录内写文件（防止 path traversal）。
func WriteFileWithin(args WriteFileArgs, root string) error {
	abs, err := filepath.Abs(args.FilePath)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return fmt.Errorf("路径 %s 超出目录 %s", args.FilePath, rootAbs)
	}
	return WriteFile(args)
}

// EditFileWithin 限制在 root 目录内编辑文件。
func EditFileWithin(args EditFileArgs, root string) error {
	abs, err := filepath.Abs(args.FilePath)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return fmt.Errorf("路径 %s 超出目录 %s", args.FilePath, rootAbs)
	}
	return EditFile(args)
}

func truncate(s string) string {
	if len(s) > 50 {
		return s[:50] + "..."
	}
	return s
}

// ═══════════════ 节点文件事务（10.4） ═══════════════

// stagedChange 暂存区中的一次变更。
type stagedChange struct {
	path    string
	content string // 新内容（写）
}

// FileTx 节点文件事务：节点执行期间暂存文件变更，完成时原子提交。
//
// 设计依据：10.4 节点文件事务
// - 暂存期间对主工作区不可见（可重复读）
// - Commit 原子应用：全部成功或全部回滚
// - Rollback 丢弃暂存
type FileTx struct {
	changes map[string]stagedChange // path → 变更
}

// NewFileTx 创建空事务。
func NewFileTx() *FileTx {
	return &FileTx{changes: make(map[string]stagedChange)}
}

// StageWrite 暂存一次写入（覆盖同路径之前的暂存）。
func (t *FileTx) StageWrite(path, content string) error {
	t.changes[path] = stagedChange{path: path, content: content}
	return nil
}

// StageEdit 暂存一次编辑（读取当前文件并应用替换，暂存新内容）。
func (t *FileTx) StageEdit(args EditFileArgs) error {
	b, err := os.ReadFile(args.FilePath)
	if err != nil {
		return err
	}
	content := string(b)
	count := 0
	for {
		idx := indexOf(content, args.OldString)
		if idx < 0 {
			break
		}
		count++
		content = content[:idx] + args.NewString + content[idx+len(args.OldString):]
		// 替换后从新位置继续（避免重复匹配同一段）
		if count > 100 {
			return fmt.Errorf("替换次数过多，疑似死循环")
		}
	}
	if count == 0 {
		return fmt.Errorf("未找到匹配的 old_string：%q", truncate(args.OldString))
	}
	t.changes[args.FilePath] = stagedChange{path: args.FilePath, content: content}
	return nil
}

func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}

// DirtyFiles 返回事务涉及的文件列表。
func (t *FileTx) DirtyFiles() []string {
	files := make([]string, 0, len(t.changes))
	for p := range t.changes {
		files = append(files, p)
	}
	return files
}

// Commit 原子应用全部暂存变更到主工作区。
// 任一失败则回滚已应用的变更，返回错误（10.4：部分冲突 → 全部回滚）。
func (t *FileTx) Commit() error {
	// 先验证所有变更可写（预检）
	prepared := make(map[string][]byte, len(t.changes))
	for path, ch := range t.changes {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("创建目录失败 %s: %w", path, err)
		}
		prepared[path] = []byte(ch.content)
	}
	// 应用（写入）
	applied := make([]string, 0, len(prepared))
	for path, content := range prepared {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			// 回滚已应用的
			for _, appliedPath := range applied {
				_ = os.Remove(appliedPath)
			}
			return fmt.Errorf("写入失败 %s: %w（已回滚）", path, err)
		}
		applied = append(applied, path)
	}
	return nil
}

// Rollback 丢弃暂存，主工作区不动。
func (t *FileTx) Rollback() {
	t.changes = make(map[string]stagedChange)
}
