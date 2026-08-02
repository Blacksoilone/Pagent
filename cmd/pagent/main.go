// Command pagent：Pagent 的 CLI 入口。
//
// 里程碑0：命令行输入 → agentic loop → 节点落盘 SQLite。
// 用法：
//
//	pagent init --dir <工作区路径>          # 初始化工作区/项目/链
//	pagent chat --chain <链ID> "你的消息"    # 对话
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pagentctx "pagent/internal/context"
	"pagent/internal/db"
	"pagent/internal/model"
	"pagent/internal/permission"
	"pagent/internal/provider"
	"pagent/internal/runtime"
	"pagent/internal/tools"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("pagent", flag.ExitOnError)
	workDir := fs.String("dir", "", "工作区路径（默认 $HOME/.pagent）")
	modelName := fs.String("model", "deepseek-chat", "模型名称")
	baseURL := fs.String("base-url", "https://api.deepseek.com/v1", "OpenAI 兼容接口地址")
	chainID := fs.String("chain", "", "目标链 ID（默认第一条链）")

	if err := fs.Parse(args); err != nil {
		return err
	}
	sub := fs.Arg(0)

	// flag 包遇到第一个非 flag 参数即停止解析，
	// 因此子命令之后的 --base-url/--model 需手动提取
	rest := fs.Args()[1:]
	var msg string
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--base-url" && i+1 < len(rest):
			*baseURL = rest[i+1]
			i++
		case rest[i] == "--model" && i+1 < len(rest):
			*modelName = rest[i+1]
			i++
		case rest[i] == "--chain" && i+1 < len(rest):
			*chainID = rest[i+1]
			i++
		case strings.HasPrefix(rest[i], "--"):
			return fmt.Errorf("未知参数 %s", rest[i])
		default:
			if msg == "" {
				msg = rest[i]
			}
		}
	}

	apiKey := os.Getenv("PAGENT_API_KEY")

	if *workDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		*workDir = filepath.Join(home, ".pagent")
	}
	if err := os.MkdirAll(*workDir, 0o755); err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("环境变量 PAGENT_API_KEY 未设置")
	}

	store, err := db.Open(filepath.Join(*workDir, "pagent.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	switch sub {
	case "init":
		return cmdInit(ctx, store, *workDir)
	case "chains":
		return cmdListChains(ctx, store)
	case "chat":
		if msg == "" {
			return fmt.Errorf("chat 需要一个消息参数")
		}
		return cmdChat(ctx, store, msg, *chainID, *modelName, *baseURL, apiKey, *workDir)
	default:
		return fmt.Errorf("未知命令 %q（可用：init / chains / chat）", sub)
	}
}

func cmdInit(ctx context.Context, store *db.DB, workDir string) error {
	// 幂等：已存在则直接列出
	projects, err := store.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) > 0 {
		fmt.Println("工作区已初始化：")
		for _, p := range projects {
			fmt.Printf("  项目 %s（%s）\n", p.Name, p.ID)
		}
		return nil
	}

	ws := model.NewWorkspace("默认工作区")
	if err := store.CreateWorkspace(ws); err != nil {
		return err
	}
	p := model.NewProject("默认项目", []string{workDir})
	if err := store.CreateProject(p); err != nil {
		return err
	}
	ch := model.NewChain(p.ID, "主链")
	if err := store.CreateChain(ch); err != nil {
		return err
	}
	fmt.Printf("初始化完成：项目 %s，链 %s\n", p.ID, ch.ID)
	return nil
}

// cmdListChains 列出所有链及其节点数。
func cmdListChains(ctx context.Context, store *db.DB) error {
	chains, err := store.ListChains()
	if err != nil {
		return err
	}
	if len(chains) == 0 {
		fmt.Println("（没有链，先运行 pagent init）")
		return nil
	}
	for _, ch := range chains {
		nodes, err := store.ListChainNodes(ch.ID)
		if err != nil {
			return err
		}
		status := ""
		if ch.Status != "active" {
			status = " [" + ch.Status + "]"
		}
		fmt.Printf("%s  %s（%d 节点）%s\n", ch.ID, ch.Name, len(nodes), status)
	}
	return nil
}

func cmdChat(ctx context.Context, store *db.DB, msg, chainID, modelName, baseURL, apiKey, workDir string) error {
	chains, err := store.ListChains()
	if err != nil {
		return err
	}
	if len(chains) == 0 {
		return fmt.Errorf("没有链，先运行 pagent init")
	}
	ch := chains[0]
	if chainID != "" {
		found := false
		for _, cand := range chains {
			if cand.ID == chainID {
				ch = cand
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("链 %s 不存在（可用 chains 命令查看）", chainID)
		}
	}

	// 加载历史（若有）
	hist, err := store.ListChainNodes(ch.ID)
	if err != nil {
		return err
	}
	var history []model.NodePart
	for _, n := range hist {
		for _, p := range n.Parts {
			// 跳过空 content 的 part（历史脏数据，OpenAI 协议不允许空 user 消息）
			if strings.TrimSpace(p.Content) == "" {
				continue
			}
			history = append(history, p)
		}
	}

	// 取出待投递的系统注入消息（4.5）
	injections, err := store.DrainInjections(ch.ID)
	if err != nil {
		return err
	}

	// 用 context 组装器构造 messages（system 数据边界声明 + L1 目录 + 注入 + 历史 + 用户输入）
	assembled, err := pagentctx.Assemble(pagentctx.AssembleInput{
		History:     history,
		Injections:  injections,
		Backgrounds: nil, // 里程碑0 无背景节点
		UserInput:   msg,
	})
	if err != nil {
		return err
	}

	// 权限引擎：从链所属项目的挂载目录构建（3.2.1）
	permEng := permission.NewEngine(projectMounts(store, ch.ProjectID))
	// CLI 场景默认：项目内 T1（读/写文件）自动允许——用户通过 CLI 启动即视为
	// 已确认项目内文件操作；T2（命令/网络）与 T3（危险）仍需确认。
	permEng.AddRule(permission.Rule{
		Scope:   permission.ScopeProject,
		Action:  permission.ActionAllow,
		MaxTier: permission.Tier1,
	})

	cl := provider.New(baseURL, apiKey, modelName)
	eng := runtime.NewEngine(cl)
	eng.Sink = store // db.DB 实现 PartSink（增量落盘，WAL 语义）
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
		// 无文件变更的事务不产生横线（10.1.1：仅提交且有差异时）
		dirty := tx.DirtyFiles()
		if len(dirty) == 0 {
			return nil
		}
		diffs := map[string]string{}
		for _, p := range dirty {
			diffs[p] = "modified"
		}
		// 10.1.1 规则 2：虚横线存在期间并入，不新建
		if pending, err := store.GetPendingStateline(ch.ProjectID); err == nil {
			for k, v := range pending.FileDiffs {
				if _, exists := diffs[k]; !exists {
					diffs[k] = v
				}
			}
			pending.FileDiffs = diffs
			return store.UpdateStateline(pending)
		}
		sl := model.NewStateline(ch.ProjectID, ch.ID, diffs)
		return store.InsertStateline(sl)
	}
	registerTools(eng, permEng, fileTx)
	// 10.1.1 规则 3：横线下方的节点创建即消费 pending 虚横线 → 实化。
	// 产生变更的节点是横线上方的创建者，不消费自己创建的横线。
	eng.OnNodeStart = func(nodeID string) error {
		if pending, err := store.GetPendingStateline(ch.ProjectID); err == nil {
			pending.Consume(nodeID)
			return store.UpdateStateline(pending)
		}
		return nil
	}
	// 流式输出
	eng.OnStreamPart = func(p provider.StreamPart) error {
		if p.Type == provider.PartText {
			fmt.Print(p.Text)
		}
		return nil
	}

	// 节点落盘：ChainID/ParentID 传给 Run（Sink 模式在 Run 开始时 InsertNodeStart）
	parentID := ""
	if len(hist) > 0 {
		parentID = hist[len(hist)-1].ID
	}
	done, err := eng.Run(ctx, runtime.RunInput{
		Prebuilt:  assembled,
		UserInput: msg,
		ChainID:   ch.ID,
		ParentID:  parentID,
	})
	fmt.Println()
	if err != nil {
		return err
	}
	if !done {
		fmt.Println("（未完成）")
	}

	node := eng.Node()
	fmt.Printf("\n节点 %s 已保存（状态 %s）\n", node.ID, node.Status)
	return nil
}

// registerTools 注册基础工具（里程碑0 子集：read_file / list_dir / echo）。
// 权限模型（3.2.1）尚未完整接入，read_file/list_dir 先限制在项目工作区内（S7 安全边界）。
// resolveProjectPath 把模型给的路径规范化到挂载目录内（3.2.1 工作目录 = 项目目录）。
// - 相对路径/裸文件名 → join 到第一个挂载目录
// - 绝对路径在挂载内 → 直接用
// - 绝对路径不在挂载内 → strip 前导 / 再 join（模型常传 /main.go）
// - 仍不在挂载目录 → 拒绝
func resolveProjectPath(permEng *permission.Engine, p string) (string, error) {
	mounts := permEng.Mounts()
	if len(mounts) == 0 {
		return "", fmt.Errorf("无挂载目录")
	}
	root := mounts[0]
	var candidates []string
	if !filepath.IsAbs(p) {
		candidates = []string{filepath.Join(root, p)}
	} else if permEng.Locate(p) {
		candidates = []string{p}
	} else {
		candidates = []string{p, filepath.Join(root, strings.TrimPrefix(p, string(filepath.Separator)))}
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

// projectMounts 返回项目挂载目录列表。
func projectMounts(store *db.DB, projectID string) []string {
	p, err := store.GetProject(projectID)
	if err != nil {
		return nil
	}
	return p.Mounts
}

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
