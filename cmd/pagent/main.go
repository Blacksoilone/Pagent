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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pagent/internal/agent"
	"pagent/internal/config"
	"pagent/internal/db"
	"pagent/internal/model"
	"pagent/internal/provider"
	"pagent/internal/server"
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
	modelName := fs.String("model", "", "模型名称（默认取配置文件）")
	baseURL := fs.String("base-url", "", "OpenAI 兼容接口地址（默认取配置文件）")
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
		case rest[i] == "--dir" && i+1 < len(rest):
			*workDir = rest[i+1]
			i++
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

	// 配置文件（config.json）加载；优先级：命令行 > 配置文件 > 环境变量 > 默认值
	cfg, err := config.Load(*workDir)
	if err != nil {
		return fmt.Errorf("读取配置 %s: %w", config.Path(*workDir), err)
	}
	if *baseURL == "" {
		*baseURL = cfg.BaseURL
	}
	if *modelName == "" {
		*modelName = cfg.Model
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("PAGENT_API_KEY")
	}

	if sub == "init" {
		// init 也需要 store；api_key 不强制（配置阶段允许为空）
		store, err := db.Open(filepath.Join(*workDir, "pagent.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		ctx := context.Background()
		return cmdInit(ctx, store, *workDir)
	}

	if apiKey == "" {
		return fmt.Errorf("未配置 API Key（config.json 的 api_key 或环境变量 PAGENT_API_KEY）")
	}

	store, err := db.Open(filepath.Join(*workDir, "pagent.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	switch sub {
	case "chains":
		return cmdListChains(ctx, store)
	case "chat":
		if msg == "" {
			return fmt.Errorf("chat 需要一个消息参数")
		}
		return cmdChat(ctx, store, msg, *chainID, *modelName, *baseURL, apiKey, *workDir)
	case "serve":
		return cmdServe(ctx, store, *modelName, *baseURL, apiKey, *workDir, cfg.TestMode)
	default:
		return fmt.Errorf("未知命令 %q（可用：init / chains / chat / serve）", sub)
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
	// 生成配置文件（已存在则保留用户配置）
	if _, err := os.Stat(config.Path(workDir)); os.IsNotExist(err) {
		cfg := config.Default()
		cfg.APIKey = os.Getenv("PAGENT_API_KEY")
		if err := config.Save(workDir, cfg); err != nil {
			return err
		}
		fmt.Printf("已生成配置文件 %s（编辑 api_key / base_url / model 后即可使用）\n", config.Path(workDir))
	}
	fmt.Printf("初始化完成：项目 %s（链将在首次对话时创建）\n", p.ID)
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
		// 首次对话：自动创建一条链（3.3 链由对话产生）
		projects, perr := store.ListProjects()
		if perr != nil || len(projects) == 0 {
			return fmt.Errorf("没有项目，先运行 pagent init")
		}
		ch := model.NewChain(projects[0].ID, "对话 1")
		if cerr := store.CreateChain(ch); cerr != nil {
			return cerr
		}
		fmt.Printf("已创建链 %s（%s）\n", ch.ID, ch.Name)
		chains, _ = store.ListChains()
	}
	target := chains[0]
	if chainID != "" {
		found := false
		for _, cand := range chains {
			if cand.ID == chainID {
				target = cand
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("链 %s 不存在（可用 chains 命令查看）", chainID)
		}
	}

	cl := provider.New(baseURL, apiKey, modelName)
	runner := agent.NewRunner(store, cl, workDir)
	err = runner.Run(ctx, target.ID, msg, "", func(p provider.StreamPart) {
		if p.Type == provider.PartText {
			fmt.Print(p.Text)
		}
	}, func(nodeID string, status model.NodeStatus) {
		fmt.Printf("\n节点 %s 已保存（状态 %s）\n", nodeID, status)
	})
	return err
}

// cmdServe 启动本地 Web 服务（里程碑1：浏览器前端）。
func cmdServe(ctx context.Context, store *db.DB, modelName, baseURL, apiKey, workDir string, testMode bool) error {
	if apiKey == "" {
		return fmt.Errorf("未配置 API Key（config.json 的 api_key 或环境变量 PAGENT_API_KEY）")
	}
	cl := provider.New(baseURL, apiKey, modelName)
	srv := server.New(store, cl, workDir, testMode)
	return srv.Listen(":8080")
}
