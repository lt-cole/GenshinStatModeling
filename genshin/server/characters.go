// 角色数据 API：角色完整 JSON 文档存储于 SQL Server genshin 库 characters 表，
// 启动时从抓取管线输出目录（site/data/characters/*.json）全量导入，此后 DB 为服务真源。
// 列表与详情均为公开数据（角色数值站本体内容）。
package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
)

type charactersAPI struct {
	mu     sync.RWMutex
	db     *sql.DB
	docs   []json.RawMessage   // 全角色文档数组（列表接口响应体）
	byName map[string]string   // 英文名 → 文档原文（详情接口）
	index  map[int64][2]string // id → [中文名, 元素]（Enka 展柜映射）
}

// reload 从库中刷新内存缓存（启动导入后调用；重启或重新导入即刷新）
func (c *charactersAPI) reload() error {
	docs, err := ListCharacterDocs(c.db)
	if err != nil {
		return err
	}
	byName := map[string]string{}
	index := map[int64][2]string{}
	for _, d := range docs {
		var probe struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			NameZh  string `json:"nameZh"`
			Element string `json:"element"`
		}
		if json.Unmarshal(d, &probe) != nil {
			continue
		}
		byName[probe.Name] = string(d)
		index[probe.ID] = [2]string{probe.NameZh, probe.Element}
	}
	c.mu.Lock()
	c.docs = docs
	c.byName = byName
	c.index = index
	c.mu.Unlock()
	return nil
}

// handleList GET /api/characters：全角色文档数组
func (c *charactersAPI) handleList(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	docs := c.docs
	c.mu.RUnlock()
	if docs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "角色数据尚未加载"})
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// handleDetail GET /api/characters/{name}：单角色完整文档
func (c *charactersAPI) handleDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c.mu.RLock()
	doc, ok := c.byName[name]
	c.mu.RUnlock()
	if !ok || doc == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "角色不存在"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(doc))
}
