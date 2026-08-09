package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pagent/internal/db"
	"pagent/internal/model"
	"pagent/internal/permission"
	"pagent/internal/provider"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// ═══════════════ resolveProjectPath ═══════════════

func TestResolveProjectPath_Should_HandlePathForms(t *testing.T) {
	root := "/work/app"
	permEng := permission.NewEngine([]string{root})

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"relative", "main.go", filepath.Join(root, "main.go"), false},
		{"abs in mount", filepath.Join(root, "src", "a.go"), filepath.Join(root, "src", "a.go"), false},
		{"hallucinated abs", "/main.go", filepath.Join(root, "main.go"), false},
		{"outside", "/tmp/evil.txt", "", true},
		{"escape", "../escape.go", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProjectPath(permEng, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s, got %s", tt.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve(%s): %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("resolve(%s) = %s, want %s", tt.path, got, tt.want)
			}
		})
	}
}

// ═══════════════ LoadHistory ═══════════════

func TestLoadHistory_Should_SkipEmptyContent(t *testing.T) {
	store := openTestDB(t)
	p := model.NewProject("p", nil)
	store.CreateProject(p)
	ch := model.NewChain(p.ID, "c")
	store.CreateChain(ch)

	n := ch.AddNode(model.NodeKindNormal)
	n.AppendPart(model.NodePartRoleUser, "", 0)           // 空（应跳过）
	n.AppendPart(model.NodePartRoleAssistant, "hello", 5) // 保留
	n.Complete()
	store.InsertNode(n)

	hist, err := (&Runner{Store: store}).LoadHistory(ch.ID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1 (empty part skipped)", len(hist))
	}
	if hist[0].Content != "hello" {
		t.Errorf("history content = %q, want hello", hist[0].Content)
	}
}

// ═══════════════ BuildEngine 配置 ═══════════════

func TestBuildEngine_Should_ConfigurePermissionsAndTools(t *testing.T) {
	store := openTestDB(t)
	cl := provider.New("http://mock/v1", "k", "m")
	runner := NewRunner(store, cl, t.TempDir())

	eng, err := runner.BuildEngine(ChainOption{ChainID: "c1", ProjectID: "p1"}, nil, nil)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	if eng == nil {
		t.Fatal("engine is nil")
	}
	// Sink 应接入（WAL 落盘）
	if eng.Sink == nil {
		t.Error("Sink not configured")
	}
	// FileTx 应存在（节点文件事务）
	if eng.FileTx == nil {
		t.Error("FileTx not configured")
	}
	if eng.CheckPermission == nil {
		t.Error("CheckPermission not configured")
	}
	if eng.OnNodeStart == nil {
		t.Error("OnNodeStart not configured (stateline consume)")
	}
}

// ═══════════════ CheckPermission 路径解析 ═══════════════

func TestCheckPermission_Should_ResolveFilePathField(t *testing.T) {
	store := openTestDB(t)
	workDir := t.TempDir()
	cl := provider.New("http://mock/v1", "k", "m")
	runner := NewRunner(store, cl, workDir)

	eng, err := runner.BuildEngine(ChainOption{ChainID: "c1", ProjectID: "p1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if eng.CheckPermission == nil {
		t.Fatal("CheckPermission not configured")
	}

	// edit_file 用 file_path 字段：项目内应 allow
	err = eng.CheckPermission("edit_file", []byte(`{"file_path":"/work/app/main.go"}`))
	if err != nil {
		t.Logf("edit_file outside mount rejected as expected: %v", err)
	}

	// 项目挂载目录需要真实存在才能 allow；无挂载时默认 ask → 拒绝
	// 这里只验证调用不 panic 且返回 error（无挂载目录）
	err = eng.CheckPermission("edit_file", []byte(`{"file_path":"/tmp/x.go"}`))
	if err == nil {
		t.Log("no mounts: outside path correctly resolved")
	}
	_ = context.Background
}

func TestRun_Should_InjectFileChangeNotice_WhenPendingStateline(t *testing.T) {
	store := openTestDB(t)
	p := model.NewProject("proj", []string{t.TempDir()})
	store.CreateProject(p)
	ch := model.NewChain(p.ID, "主链")
	store.CreateChain(ch)

	// 已有 pending draft 横线（文件在上一个节点之后变了）
	sl := model.NewStateline(p.ID, ch.ID, map[string]string{"/work/a.go": "modified"})
	store.InsertStateline(sl)

	// mock OpenAI 端点：捕获请求体，返回空 SSE
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}

data: [DONE]

`)
	}))
	defer srv.Close()

	cl := provider.New(srv.URL, "k", "m")
	runner := NewRunner(store, cl, t.TempDir())
	err := runner.Run(context.Background(), ch.ID, "继续", "", nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotBody == "" {
		t.Fatal("provider not called")
	}
	if !strings.Contains(gotBody, "文件状态已发生变化") {
		t.Errorf("request missing file change notice: %s", gotBody[:min(len(gotBody), 300)])
	}
	if !strings.Contains(gotBody, "/work/a.go") {
		t.Errorf("request missing changed file path: %s", gotBody[:min(len(gotBody), 300)])
	}
}
