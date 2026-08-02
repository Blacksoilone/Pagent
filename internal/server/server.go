// Package server 提供本地 Web 界面的 HTTP/SSE 服务。
//
// 里程碑1（本地 Web 模式）：Go 后端 + 浏览器前端。
// 路由：
//
//	GET  /api/chains          链列表
//	GET  /api/chains/:id/nodes 链的节点（含 parts）
//	GET  /api/statelines      横线列表
//	POST /api/chat            SSE 流式对话
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"pagent/internal/agent"
	"pagent/internal/db"
	"pagent/internal/model"
	"pagent/internal/provider"
)

// Server 本地 Web 服务。
type Server struct {
	store    *db.DB
	provider *provider.Client
	workDir  string
	mux      *http.ServeMux
}

// New 创建服务。
func New(store *db.DB, cl *provider.Client, workDir string) *Server {
	s := &Server{store: store, provider: cl, workDir: workDir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/chains", s.handleChains)
	mux.HandleFunc("GET /api/chains/{id}/nodes", s.handleChainNodes)
	mux.HandleFunc("GET /api/statelines", s.handleStatelines)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux = mux
	return s
}

// Handler 返回 HTTP handler（供前端 dev server 代理或直接挂载）。
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Listen 启动 HTTP 服务（addr 如 ":8080"）。
func (s *Server) Listen(addr string) error {
	log.Printf("Pagent Web 界面: http://localhost%s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// ═══════════════ API ═══════════════

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ChainDTO 链的 API 表示。
type ChainDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	chains, err := s.store.ListChains()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]ChainDTO, 0, len(chains))
	for _, c := range chains {
		out = append(out, ChainDTO{ID: c.ID, Name: c.Name, Status: c.Status})
	}
	writeJSON(w, 200, out)
}

// NodeDTO 节点的 API 表示（含 parts）。
type NodeDTO struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent_id,omitempty"`
	Kind     string    `json:"kind"`
	Status   string    `json:"status"`
	Title    string    `json:"title,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Visible  bool      `json:"visible"`
	Parts    []PartDTO `json:"parts"`
}

// PartDTO part 的 API 表示。
type PartDTO struct {
	Seq     int    `json:"seq"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *Server) handleChainNodes(w http.ResponseWriter, r *http.Request) {
	chainID := r.PathValue("id")
	nodes, err := s.store.ListChainNodes(chainID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]NodeDTO, 0, len(nodes))
	for _, n := range nodes {
		parts := make([]PartDTO, 0, len(n.Parts))
		for _, p := range n.Parts {
			parts = append(parts, PartDTO{Seq: p.Seq, Role: string(p.Role), Content: p.Content})
		}
		out = append(out, NodeDTO{
			ID: n.ID, ParentID: n.ParentID, Kind: string(n.Kind),
			Status: string(n.Status), Title: n.Title, Summary: n.Summary, Visible: n.Visible,
			Parts: parts,
		})
	}
	writeJSON(w, 200, out)
}

// StatelineDTO 横线的 API 表示。
type StatelineDTO struct {
	ID         string            `json:"id"`
	State      string            `json:"state"`
	FileDiffs  map[string]string `json:"file_diffs"`
	ConsumedBy string            `json:"consumed_by,omitempty"`
}

func (s *Server) handleStatelines(w http.ResponseWriter, r *http.Request) {
	// 列出所有项目的横线（简单版：全部链的最新横线）
	chains, err := s.store.ListChains()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var sls []*model.Stateline
	for _, ch := range chains {
		if sl, err := s.store.GetLatestStateline(ch.ProjectID); err == nil {
			sls = append(sls, sl)
		}
	}
	out := make([]StatelineDTO, 0, len(sls))
	for _, sl := range sls {
		out = append(out, StatelineDTO{
			ID: sl.ID, State: string(sl.State), FileDiffs: sl.FileDiffs, ConsumedBy: sl.ConsumedBy,
		})
	}
	writeJSON(w, 200, out)
}

// ChatRequest 对话请求。
type ChatRequest struct {
	ChainID string `json:"chain_id"`
	Message string `json:"message"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求格式错误: "+err.Error())
		return
	}
	if req.Message == "" {
		writeErr(w, 400, "message 不能为空")
		return
	}
	if req.ChainID == "" {
		writeErr(w, 400, "chain_id 不能为空")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "SSE 不支持")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 用共享 Runner 执行会话（权限/工具/文件事务/横线回调全部内置）
	runner := agent.NewRunner(s.store, s.provider, s.workDir)
	err := runner.Run(r.Context(), req.ChainID, req.Message,
		func(p provider.StreamPart) {
			switch p.Type {
			case provider.PartText:
				fmt.Fprintf(w, "event: text\ndata: %s\n\n", jsonEscape(p.Text))
			case provider.PartToolCall:
				fmt.Fprintf(w, "event: tool\ndata: %s\n\n", jsonEscape(fmt.Sprintf("调用工具 %s", p.ToolCall.Name)))
			}
			flusher.Flush()
		},
		func(nodeID string, status model.NodeStatus) {
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEscape(fmt.Sprintf(`{"node_id":%q,"status":%q}`, nodeID, status)))
			flusher.Flush()
		})
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(err.Error()))
		flusher.Flush()
	}
}

// ═══════════════ helpers ═══════════════

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var _ = context.Background
