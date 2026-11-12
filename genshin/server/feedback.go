// 留言板：留言持久化于 SQL Server 独立 genshin 库的 feedback 表（建库建表与迁移见 sqlstore.go）。
// 提交接口挂使用站（公开）；列表/标记已处理/删除挂 127.0.0.1:50056 运维站（仅本机，与主站后台同惯例）。
// 请求校验、限频、错误语义沿用既有实现；并发由连接池 + 内存限频控制。
package main

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// feedbackRecord 单条留言（不收 IP/昵称/联系方式：仅内容 + 来源上下文）
type feedbackRecord struct {
	ID      int64             `json:"id"`
	Time    string            `json:"time"` // RFC3339
	Page    string            `json:"page"` // 来源页路径，如 /detail.html?name=米提亚
	Char    string            `json:"char"` // 角色中文名（详情页自动附带，可为空）
	Content string            `json:"content"`
	Ctx     map[string]string `json:"ctx,omitempty"` // 自动附带的页面上下文（伤害计算输入快照/米游社缓存/UA）
	Done    bool              `json:"done"`          // 站长标记已处理
}

type feedbackStore struct {
	mu   sync.Mutex
	db   *sql.DB
	rate map[string][]time.Time // 限频表：IP → 近期提交时刻（仅内存，不入库）
}

func newFeedbackStore(db *sql.DB) *feedbackStore {
	return &feedbackStore{db: db, rate: map[string][]time.Time{}}
}

// allow 滑窗限频：同 IP 10 分钟内至多 3 条
func (f *feedbackStore) allow(ip string) bool {
	now := time.Now()
	recent := f.rate[ip][:0]
	for _, t := range f.rate[ip] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 3 {
		f.rate[ip] = recent
		return false
	}
	f.rate[ip] = append(recent, now)
	return true
}

// clientIP 站点经 Cloudflare 回源，真实客户端 IP 优先取 CF 头；
// 回退 RemoteAddr 时剥掉端口（同一客户端每次连接源端口不同，带端口会导致限频键失效）
func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeJSON 统一 JSON 响应（错误格式沿用主站惯例 {"error": "中文消息"}）
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// truncateRunes 按 rune 截断，防超长上下文字段
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// handleCreate 公开提交留言：POST /api/feedback {page, char, content, ctx}
func (f *feedbackStore) handleCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in struct {
		Page    string            `json:"page"`
		Char    string            `json:"char"`
		Content string            `json:"content"`
		Ctx     map[string]string `json:"ctx"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		slog.Error("留言请求体解析失败", "err", err, "ip", clientIP(r))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误，请刷新后重试"})
		return
	}
	content := strings.TrimSpace(in.Content)
	n := utf8.RuneCountInString(content)
	if n < 1 || n > 500 {
		slog.Info("留言内容字数不合法", "len", n, "ip", clientIP(r))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "留言内容需在 1~500 字之间"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ip := clientIP(r)
	if !f.allow(ip) {
		slog.Warn("留言触发限频", "ip", ip)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "留言太频繁，请稍后再试"})
		return
	}
	ctxJSON := "{}"
	if len(in.Ctx) > 0 {
		trimmed := map[string]string{}
		for k, v := range in.Ctx { // 上下文逐项截断，最多 20 项
			if len(trimmed) >= 20 {
				break
			}
			trimmed[truncateRunes(strings.TrimSpace(k), 40)] = truncateRunes(strings.TrimSpace(v), 300)
		}
		if b, err := json.Marshal(trimmed); err == nil {
			ctxJSON = string(b)
		}
	}
	var newID int64
	err := f.db.QueryRow(
		`INSERT INTO feedback ([time], [page], [char_name], content, ctx, done)
		 OUTPUT INSERTED.id VALUES (@p1, @p2, @p3, @p4, @p5, 0)`,
		time.Now().Format(time.RFC3339), truncateRunes(strings.TrimSpace(in.Page), 200),
		truncateRunes(strings.TrimSpace(in.Char), 50), content, ctxJSON,
	).Scan(&newID)
	if err != nil {
		slog.Error("留言入库失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "服务器保存留言失败，请稍后再试"})
		return
	}
	slog.Info("收到留言", "id", newID, "page", truncateRunes(strings.TrimSpace(in.Page), 200), "char", truncateRunes(strings.TrimSpace(in.Char), 50), "len", n, "ip", ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": newID})
}

// feedbackRecord 的 SQL 行扫描结构
type feedbackRow struct {
	ID      int64
	Time    string
	Page    string
	Char    string
	Content string
	Ctx     sql.NullString
	Done    bool
}

const feedbackSelectCols = `id, [time], [page], [char_name], content, ctx, done`

// scanFeedbackRow 从当前行读取一条留言（ctx 空串解析为空 map）
func scanFeedbackRow(row *sql.Row) (feedbackRecord, error) {
	var fr feedbackRow
	if err := row.Scan(&fr.ID, &fr.Time, &fr.Page, &fr.Char, &fr.Content, &fr.Ctx, &fr.Done); err != nil {
		return feedbackRecord{}, err
	}
	return fr.toRecord(), nil
}

func (fr feedbackRow) toRecord() feedbackRecord {
	ctx := map[string]string{}
	if fr.Ctx.Valid && strings.TrimSpace(fr.Ctx.String) != "" {
		_ = json.Unmarshal([]byte(fr.Ctx.String), &ctx)
	}
	return feedbackRecord{
		ID: fr.ID, Time: fr.Time, Page: fr.Page, Char: fr.Char,
		Content: fr.Content, Ctx: ctx, Done: fr.Done,
	}
}

// handleList 管理接口：GET /api/feedback 返回全量留言（仅本机运维站）
func (f *feedbackStore) handleList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	rows, err := f.db.Query(`SELECT ` + feedbackSelectCols + ` FROM feedback ORDER BY id`)
	f.mu.Unlock()
	if err != nil {
		slog.Error("读取留言失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取留言失败，请稍后再试"})
		return
	}
	defer rows.Close()
	list := []feedbackRecord{}
	for rows.Next() {
		var fr feedbackRow
		var ctxStr sql.NullString
		if err := rows.Scan(&fr.ID, &fr.Time, &fr.Page, &fr.Char, &fr.Content, &ctxStr, &fr.Done); err != nil {
			slog.Error("留言行扫描失败", "err", err)
			continue
		}
		fr.Ctx = ctxStr
		list = append(list, fr.toRecord())
	}
	slog.Info("管理端拉取留言", "count", len(list), "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, list)
}

// handleResolve 管理接口：POST /api/feedback/resolve {id, done}（仅本机运维站）
func (f *feedbackStore) handleResolve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   int64 `json:"id"`
		Done bool  `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误"})
		return
	}
	res, err := f.db.Exec(`UPDATE feedback SET done = @p1 WHERE id = @p2`, in.Done, in.ID)
	if err != nil {
		slog.Error("更新留言状态失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存失败，请稍后再试"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "留言不存在"})
		return
	}
	slog.Info("留言处理状态更新", "id", in.ID, "done", in.Done, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteMarked 运维站清理接口：POST /api/feedback/delete-marked {marker}
// 删除 content 含 marker 的记录（供自动化测试清理，仅本机运维站可达）
func (f *feedbackStore) handleDeleteMarked(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Marker string `json:"marker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Marker) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误"})
		return
	}
	res, err := f.db.Exec(`DELETE FROM feedback WHERE content LIKE @p1`, "%"+in.Marker+"%")
	if err != nil {
		slog.Error("删除测试留言失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "删除失败，请稍后再试"})
		return
	}
	affected, _ := res.RowsAffected()
	slog.Info("清理测试留言", "deleted", affected, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": affected})
}

// registerFeedback 注册留言接口：提交挂使用站（公开），管理/清理挂运维站（仅本机）
func registerFeedback(siteMux, adminMux *http.ServeMux, store *feedbackStore) {
	siteMux.HandleFunc("POST /api/feedback", store.handleCreate)
	adminMux.HandleFunc("GET /api/feedback", store.handleList)
	adminMux.HandleFunc("POST /api/feedback/resolve", store.handleResolve)
	adminMux.HandleFunc("POST /api/feedback/delete-marked", store.handleDeleteMarked)
}
