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

	cl := provider.New(baseURL, apiKey, modelName)
	eng := runtime.NewEngine(cl)
	eng.Sink = store // db.DB 实现 PartSink（增量落盘，WAL 语义）
	eng.CheckPermission = func(name string, args json.RawMessage) error {
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &a)
		op := permission.Operation{Tool: name, Path: a.Path}
		switch permEng.Decide(op) {
		case permission.DecisionAllow:
			return nil
		case permission.DecisionDeny:
			return fmt.Errorf("已禁止")
		default:
			return fmt.Errorf("需要用户确认")
		}
	}
	registerTools(eng, workDir, permEng)
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
// projectMounts 返回项目挂载目录列表。
func projectMounts(store *db.DB, projectID string) []string {
	p, err := store.GetProject(projectID)
	if err != nil {
		return nil
	}
	return p.Mounts
}

func registerTools(eng *runtime.Engine, workDir string, permEng *permission.Engine) {
	// 路径必须在挂载目录内（与权限引擎同边界，3.2.1）
	within := func(p string) (string, error) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		if !permEng.Locate(abs) {
			return "", fmt.Errorf("路径 %s 超出项目挂载目录", p)
		}
		return abs, nil
	}

	eng.RegisterToolSpec("read_file", "读取文件内容（仅项目目录内）",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"项目内文件的绝对路径"}},"required":["path"]}`),
		func(args json.RawMessage) (string, error) {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			abs, err := within(a.Path)
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
			abs, err := within(a.Path)
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
}
