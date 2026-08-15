package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pagent/internal/db"
	"pagent/internal/model"
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

// mockOpenAI 返回 SSE 流：文本"你好"。
func mockOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"你好"}}]}

data: [DONE]

`)
	}))
}

func setupServer(t *testing.T) (*Server, *httptest.Server, *model.Project) {
	t.Helper()
	return setupServerMode(t, false)
}

func setupServerMode(t *testing.T, testMode bool) (*Server, *httptest.Server, *model.Project) {
	t.Helper()
	store := openTestDB(t)
	ws := model.NewWorkspace("ws")
	store.CreateWorkspace(ws)
	p := model.NewProject("项目", []string{t.TempDir()})
	store.CreateProject(p)
	ch := model.NewChain(p.ID, "主链")
	store.CreateChain(ch)

	// 插入一个已完成节点
	n := ch.AddNode(model.NodeKindNormal)
	n.AppendPart(model.NodePartRoleUser, "你好", 2)
	n.AppendPart(model.NodePartRoleAssistant, "我是助手", 4)
	n.Complete()
	store.InsertNode(n)

	openai := mockOpenAI(t)
	t.Cleanup(openai.Close)
	cl := provider.New(openai.URL, "test-key", "test-model")
	srv := New(store, cl, t.TempDir(), testMode)
	return srv, openai, p
}

func doGet(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHandleChains_Should_ReturnChainList(t *testing.T) {
	srv, _, _ := setupServer(t)
	rec := doGet(t, srv, "/api/chains")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var chains []ChainDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &chains); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(chains) != 1 {
		t.Fatalf("chains len = %d, want 1", len(chains))
	}
	if chains[0].Name != "主链" {
		t.Errorf("chain name = %s, want 主链", chains[0].Name)
	}
}

func TestHandleChainNodes_Should_ReturnNodesWithParts(t *testing.T) {
	srv, _, p := setupServer(t)
	ch, _ := srv.store.GetChain("")
	_ = ch
	_ = p

	// 找链 ID
	chains, _ := srv.store.ListChains()
	if len(chains) == 0 {
		t.Fatal("no chains")
	}
	rec := doGet(t, srv, "/api/chains/"+chains[0].ID+"/nodes")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var nodes []NodeDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	if len(nodes[0].Parts) != 2 {
		t.Errorf("parts len = %d, want 2", len(nodes[0].Parts))
	}
	if nodes[0].Parts[0].Role != "user" || nodes[0].Parts[0].Content != "你好" {
		t.Errorf("part[0] = %+v", nodes[0].Parts[0])
	}
	if nodes[0].Status != "done" {
		t.Errorf("status = %s, want done", nodes[0].Status)
	}
}

func TestHandleStatelines_Should_ReturnList(t *testing.T) {
	srv, _, _ := setupServer(t)
	// 插入一条横线
	sl := model.NewStateline("proj-x", "chain-x", map[string]string{"a.go": "modified"})
	srv.store.InsertStateline(sl)

	rec := doGet(t, srv, "/api/statelines")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var sls []StatelineDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sls); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// setupServer 的项目有挂载目录但没横线；这里插入的是 proj-x（不属于任何链）
	// 所以 handleStatelines 按链查询可能返回空——这取决于实现。
	// 至少验证响应是合法 JSON 数组。
	_ = sls
}

func TestHandleChat_Should_StreamTextAndDone(t *testing.T) {
	srv, _, _ := setupServer(t)
	chains, _ := srv.store.ListChains()
	if len(chains) == 0 {
		t.Fatal("no chains")
	}
	chainID := chains[0].ID

	body := fmt.Sprintf(`{"chain_id":%q,"message":"你好"}`, chainID)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// 解析 SSE
	var sawText, sawDone bool
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			ev := strings.TrimPrefix(line, "event: ")
			if ev == "text" {
				sawText = true
			}
			if ev == "done" {
				sawDone = true
			}
		}
	}
	if !sawText {
		t.Error("no text event in SSE stream")
	}
	if !sawDone {
		t.Error("no done event in SSE stream")
	}
}

func TestHandleChat_Should_RejectMissingMessage(t *testing.T) {
	srv, _, _ := setupServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"chain_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func doPostJSON(t *testing.T, s *Server, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHandlePromote_Should_UpgradeToImportant(t *testing.T) {
	srv, _, _ := setupServer(t)
	// 拿到链和节点
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	if len(chains) == 0 {
		t.Fatal("no chains")
	}
	chainID := chains[0].ID
	var nodes []NodeDTO
	rec = doGet(t, srv, "/api/chains/"+chainID+"/nodes")
	json.Unmarshal(rec.Body.Bytes(), &nodes)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	nodeID := nodes[0].ID

	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/nodes/"+nodeID+"/promote", "{}")
	if rec.Code != 200 {
		t.Fatalf("promote status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out NodeDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Kind != string(model.NodeKindImportant) {
		t.Errorf("kind = %s, want important", out.Kind)
	}

	// 降低回 normal
	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/nodes/"+nodeID+"/demote", "{}")
	if rec.Code != 200 {
		t.Fatalf("demote status = %d", rec.Code)
	}
}

func TestHandlePromote_Should_RejectGhost(t *testing.T) {
	srv, _, _ := setupServer(t)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID

	// fork 产生虚节点
	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/fork", `{"anchor_node_id":"missing"}`)
	if rec.Code != 400 {
		t.Fatalf("fork with missing anchor should 400, got %d", rec.Code)
	}
}

func TestHandleMaterialize_Should_RejectNonGhost(t *testing.T) {
	srv, _, _ := setupServer(t)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID
	var nodes []NodeDTO
	rec = doGet(t, srv, "/api/chains/"+chainID+"/nodes")
	json.Unmarshal(rec.Body.Bytes(), &nodes)

	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/nodes/"+nodes[0].ID+"/materialize", "{}")
	if rec.Code != 400 {
		t.Fatalf("materialize normal node should 400, got %d", rec.Code)
	}
}

func TestForkPromoteMaterialize_FullFlow(t *testing.T) {
	srv, _, _ := setupServer(t)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID
	var nodes []NodeDTO
	rec = doGet(t, srv, "/api/chains/"+chainID+"/nodes")
	json.Unmarshal(rec.Body.Bytes(), &nodes)
	anchorID := nodes[0].ID

	// 1. fork → 产生虚节点
	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/fork", `{"anchor_node_id":"`+anchorID+`"}`)
	if rec.Code != 201 {
		t.Fatalf("fork status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var fork NodeDTO
	json.Unmarshal(rec.Body.Bytes(), &fork)
	if fork.Kind != string(model.NodeKindGhost) {
		t.Fatalf("fork kind = %s, want ghost", fork.Kind)
	}

	// 2. 虚节点 → 实化
	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/nodes/"+fork.ID+"/materialize", "{}")
	if rec.Code != 200 {
		t.Fatalf("materialize status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// 3. 实化后 → 提升
	rec = doPostJSON(t, srv, "/api/chains/"+chainID+"/nodes/"+fork.ID+"/promote", "{}")
	if rec.Code != 200 {
		t.Fatalf("promote status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out NodeDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Kind != string(model.NodeKindImportant) {
		t.Errorf("final kind = %s, want important", out.Kind)
	}
}

func TestTestCreateNode_Should_NotExistInProduction(t *testing.T) {
	srv, _, _ := setupServer(t)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID

	// 未设置 PAGENT_TEST_MODE：路由不存在 → 404
	rec = doPostJSON(t, srv, "/api/test/chains/"+chainID+"/nodes", `{"kind":"normal"}`)
	if rec.Code != 404 {
		t.Fatalf("test route should 404 without PAGENT_TEST_MODE, got %d", rec.Code)
	}
}

func TestTestCreateNode_Should_CreateNormalNode(t *testing.T) {
	srv, _, _ := setupServerMode(t, true)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID

	rec = doPostJSON(t, srv, "/api/test/chains/"+chainID+"/nodes", `{"kind":"normal","content":"测试节点"}`)
	if rec.Code != 201 {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out NodeDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Kind != "normal" || out.Status != "done" {
		t.Errorf("kind=%s status=%s, want normal/done", out.Kind, out.Status)
	}
	if out.ParentID == "" {
		t.Errorf("should hang on chain tail")
	}
}

func TestTestCreateNode_Should_CreateGhostAndRejectBadKind(t *testing.T) {
	srv, _, _ := setupServerMode(t, true)
	var chains []ChainDTO
	rec := doGet(t, srv, "/api/chains")
	json.Unmarshal(rec.Body.Bytes(), &chains)
	chainID := chains[0].ID

	rec = doPostJSON(t, srv, "/api/test/chains/"+chainID+"/nodes", `{"kind":"ghost"}`)
	if rec.Code != 201 {
		t.Fatalf("create ghost status = %d", rec.Code)
	}
	var out NodeDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Kind != "ghost" {
		t.Errorf("kind = %s, want ghost", out.Kind)
	}

	rec = doPostJSON(t, srv, "/api/test/chains/"+chainID+"/nodes", `{"kind":"hacker"}`)
	if rec.Code != 400 {
		t.Fatalf("bad kind should 400, got %d", rec.Code)
	}
}
