// Package agent 提供会话运行的共享组装逻辑（CLI 与 Web server 复用）。
//
// 包含：权限引擎构建、工具注册、文件事务、横线回调、历史加载、上下文组装。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pagent/internal/db"
	"pagent/internal/model"
	"pagent/internal/permission"
	"pagent/internal/provider"
	"pagent/internal/runtime"
	"pagent/internal/tools"

	pagentctx "pagent/internal/context"
)

// Runner 一次会话运行器的工厂：根据链构建配置好的引擎。
type Runner struct {
	Store   *db.DB
	Client  *provider.Client
	WorkDir string
}

// NewRunner 创建会话运行器。
func NewRunner(store *db.DB, cl *provider.Client, workDir string) *Runner {
	return &Runner{Store: store, Client: cl, WorkDir: workDir}
}

// ChainOption 链配置（用于构建权限引擎）。
type ChainOption struct {
	ChainID   string
	ProjectID string
}

// BuildEngine 构建配置好的 runtime.Engine。
//
// onStream 用于流式输出（CLI 打 stdout，Web 写 SSE）。
// onDone 在节点完成后回调（可选，传入节点 ID 和状态）。
func (r *Runner) BuildEngine(opt ChainOption, onStream func(provider.StreamPart), onDone func(nodeID string, status model.NodeStatus)) (*runtime.Engine, error) {
	// 权限引擎（3.2.1）
	permEng := permission.NewEngine(r.projectMounts(opt.ProjectID))
	permEng.AddRule(permission.Rule{
		Scope:   permission.ScopeProject,
		Action:  permission.ActionAllow,
		MaxTier: permission.Tier1,
	})

	eng := runtime.NewEngine(r.Client)
	eng.Sink = r.Store
	eng.CheckPermission = func(name string, args json.RawMessage) error {
		var a struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
			Command  string `json:"command"`
		}
		_ = json.Unmarshal(args, &a)
		target := a.Path
		if target == "" {
			target = a.FilePath
		}
		if target != "" {
			resolved, err := resolveProjectPath(permEng, target)
			if err != nil {
				return err
			}
			target = resolved
		}
		op := permission.Operation{Tool: name, Path: target, Command: a.Command}
		switch permEng.Decide(op) {
		case permission.DecisionAllow:
			return nil
		case permission.DecisionDeny:
			return fmt.Errorf("已禁止")
		default:
			return fmt.Errorf("需要用户确认")
		}
	}

	fileTx := tools.NewFileTx()
	eng.FileTx = fileTx
	eng.CommitFileTx = func(tx *tools.FileTx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		dirty := tx.DirtyFiles()
		if len(dirty) == 0 {
			return nil
		}
		diffs := map[string]string{}
		for _, p := range dirty {
			diffs[p] = "modified"
		}
		// 10.1.1 规则 2：虚横线存在期间并入，不新建
		if pending, err := r.Store.GetPendingStateline(opt.ProjectID); err == nil {
			for k, v := range pending.FileDiffs {
				if _, exists := diffs[k]; !exists {
					diffs[k] = v
				}
			}
			pending.FileDiffs = diffs
			return r.Store.UpdateStateline(pending)
		}
		sl := model.NewStateline(opt.ProjectID, opt.ChainID, diffs)
		return r.Store.InsertStateline(sl)
	}
	// 10.1.1 规则 3：横线下方的节点创建即消费 pending 虚横线 → 实化
	eng.OnNodeStart = func(nodeID string) error {
		if pending, err := r.Store.GetPendingStateline(opt.ProjectID); err == nil {
			pending.Consume(nodeID)
			return r.Store.UpdateStateline(pending)
		}
		return nil
	}
	eng.OnStreamPart = func(p provider.StreamPart) error {
		if onStream != nil {
			onStream(p)
		}
		return nil
	}
	eng.RecordEvent = func(eventType string, detail any) {
		// TODO: 写入 run_event 表（11.4 可观测性）
	}

	registerTools(eng, permEng, fileTx)
	return eng, nil
}

// LoadHistory 加载链的历史 parts（跳过空 content）。
func (r *Runner) LoadHistory(chainID string) ([]model.NodePart, error) {
	hist, err := r.Store.ListChainNodes(chainID)
	if err != nil {
		return nil, err
	}
	var history []model.NodePart
	for _, n := range hist {
		for _, p := range n.Parts {
			if strings.TrimSpace(p.Content) == "" {
				continue
			}
			history = append(history, p)
		}
	}
	return history, nil
}

// AssembleMessages 组装发送给模型的 messages（system + L1 + 注入 + 历史 + 用户输入）。
func (r *Runner) AssembleMessages(history []model.NodePart, userInput string) ([]provider.Message, error) {
	injections, err := r.Store.DrainInjections("") // chainID 由调用方注入
	if err != nil {
		return nil, err
	}
	_ = injections
	// 注入消息需要按链取，但此处不知道链 ID；由调用方传入
	return pagentctx.Assemble(pagentctx.AssembleInput{
		History:   history,
		UserInput: userInput,
	})
}

// Run 执行一次会话（加载历史 → 组装 → 运行）。
func (r *Runner) Run(ctx context.Context, chainID, userInput string, onStream func(provider.StreamPart), onDone func(nodeID string, status model.NodeStatus)) error {
	ch, err := r.Store.GetChain(chainID)
	if err != nil {
		return err
	}
	history, err := r.LoadHistory(chainID)
	if err != nil {
		return err
	}
	assembled, err := pagentctx.Assemble(pagentctx.AssembleInput{
		History:   history,
		UserInput: userInput,
	})
	if err != nil {
		return err
	}
	eng, err := r.BuildEngine(ChainOption{ChainID: chainID, ProjectID: ch.ProjectID}, onStream, onDone)
	if err != nil {
		return err
	}
	parentID := ""
	if len(history) > 0 {
		parentID = history[len(history)-1].NodeID
	}
	_, err = eng.Run(ctx, runtime.RunInput{
		Prebuilt:  assembled,
		UserInput: userInput,
		ChainID:   chainID,
		ParentID:  parentID,
	})
	if onDone != nil && eng.Node() != nil {
		onDone(eng.Node().ID, eng.Node().Status)
	}
	return err
}

// projectMounts 返回项目的挂载目录列表。
func (r *Runner) projectMounts(projectID string) []string {
	p, err := r.Store.GetProject(projectID)
	if err != nil {
		return nil
	}
	return p.Mounts
}

// resolveProjectPath 把模型给的路径规范化到挂载目录内（3.2.1 工作目录 = 项目目录）。
// - 相对路径/裸文件名 → join 到第一个挂载目录
// - 绝对路径在挂载内 → 直接用
// - 绝对路径不在挂载内且是裸文件名（如 /main.go，模型幻觉）→ strip 前导 / 后 join
// - 其他绝对路径（如 /tmp/x.go）→ 拒绝（可能是越界访问，不宽容）
func resolveProjectPath(permEng *permission.Engine, p string) (string, error) {
	mounts := permEng.Mounts()
	if len(mounts) == 0 {
		return "", fmt.Errorf("无挂载目录")
	}
	root := mounts[0]
	var candidates []string
	switch {
	case !filepath.IsAbs(p):
		candidates = []string{filepath.Join(root, p)}
	case permEng.Locate(p):
		candidates = []string{p}
	case isBareFileName(p):
		// 模型幻觉的根路径：/main.go → root/main.go
		candidates = []string{filepath.Join(root, strings.TrimPrefix(p, string(filepath.Separator)))}
	default:
		// /tmp/evil.txt 这类绝对路径 → 拒绝（越界）
		return "", fmt.Errorf("路径 %s 超出项目挂载目录", p)
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err != nil {
			continue
		}
		if permEng.Locate(abs) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("路径 %s 超出项目挂载目录", p)
}

// isBareFileName 判断绝对路径是否只是一个裸文件名（无目录分隔符），
// 如 /main.go 是模型把"main.go"误拼成根路径的幻觉。
func isBareFileName(p string) bool {
	trimmed := strings.TrimPrefix(p, string(filepath.Separator))
	return trimmed != "" && !strings.Contains(trimmed, string(filepath.Separator))
}

// registerTools 注册基础工具（里程碑1：read_file/list_dir/echo/write_file/edit_file）。
// 所有路径操作经 resolveProjectPath 限制在挂载目录内（S7 安全边界）。
// 写类工具通过 fileTx 暂存（10.4 节点文件事务），节点完成时统一提交。
func registerTools(eng *runtime.Engine, permEng *permission.Engine, fileTx *tools.FileTx) {
	eng.RegisterToolSpec("read_file", "读取文件内容（仅项目目录内）",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"项目内文件的绝对路径"}},"required":["path"]}`),
		func(args json.RawMessage) (string, error) {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			abs, err := resolveProjectPath(permEng, a.Path)
			if err != nil {
				return "", err
			}
			if d := permEng.Decide(permission.Operation{Tool: "read_file", Path: abs}); d != permission.DecisionAllow {
				return "", fmt.Errorf("权限不足：读取 %s（需用户确认）", abs)
			}
			b, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			return string(b), nil
		})
	eng.RegisterToolSpec("list_dir", "列出目录内容（仅项目目录内）",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"项目内目录的绝对路径"}},"required":["path"]}`),
		func(args json.RawMessage) (string, error) {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			abs, err := resolveProjectPath(permEng, a.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				return "", err
			}
			out := ""
			for _, e := range entries {
				kind := "文件"
				if e.IsDir() {
					kind = "目录"
				}
				out += fmt.Sprintf("%s %s\n", kind, e.Name())
			}
			return out, nil
		})
	eng.RegisterToolSpec("echo", "回显参数",
		json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		func(args json.RawMessage) (string, error) {
			return string(args), nil
		})
	eng.RegisterToolSpec("write_file", "写入或覆盖文件（仅项目挂载目录内）",
		json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`),
		func(args json.RawMessage) (string, error) {
			var a tools.WriteFileArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			abs, err := resolveProjectPath(permEng, a.FilePath)
			if err != nil {
				return "", err
			}
			if d := permEng.Decide(permission.Operation{Tool: "write_file", Path: abs}); d != permission.DecisionAllow {
				return "", fmt.Errorf("权限不足：写入 %s 需要用户确认", abs)
			}
			if err := fileTx.StageWrite(abs, a.Content); err != nil {
				return "", err
			}
			return "已暂存写入 " + abs + "（节点完成时生效）", nil
		})
	eng.RegisterToolSpec("edit_file", "精确替换文件中的字符串（仅项目挂载目录内，old_string 必须唯一匹配）",
		json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["file_path","old_string","new_string"]}`),
		func(args json.RawMessage) (string, error) {
			var a tools.EditFileArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			abs, err := resolveProjectPath(permEng, a.FilePath)
			if err != nil {
				return "", err
			}
			if d := permEng.Decide(permission.Operation{Tool: "edit_file", Path: abs}); d != permission.DecisionAllow {
				return "", fmt.Errorf("权限不足：修改 %s 需要用户确认", abs)
			}
			if err := fileTx.StageEdit(tools.EditFileArgs{FilePath: abs, OldString: a.OldString, NewString: a.NewString}); err != nil {
				return "", err
			}
			return "已暂存修改 " + abs + "（节点完成时生效）", nil
		})
}
