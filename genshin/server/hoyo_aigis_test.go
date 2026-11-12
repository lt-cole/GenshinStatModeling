package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// parseAigisHeader / buildAigisHeader 应正确往返（GT3 形态）
func TestAigisParseAndBuild(t *testing.T) {
	wire, _ := json.Marshal(map[string]any{
		"session_id": "abc123", "mmt_type": 1,
		"data": `{"gt":"gt-config","challenge":"chg-456"}`,
	})
	s, err := parseAigisHeader(base64.StdEncoding.EncodeToString(wire))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s.SessionID != "abc123" || s.MmtType != 1 {
		t.Fatalf("会话字段不符: %+v", s)
	}
	if s.Data["gt"] != "gt-config" || s.Data["challenge"] != "chg-456" {
		t.Fatalf("data 字段不符: %+v", s.Data)
	}
	h, err := buildAigisHeader(s.SessionID, map[string]any{
		"challenge": "chg-456", "validate": "val", "seccode": "sec",
	})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if len(h) < 4 || h[:4] != "abc1" && h[:6] != "abc123" {
		t.Fatalf("header 应以 session_id 开头: %q", h[:10])
	}
	// 重建结果应可再解码回验证结果 JSON
	raw, derr := base64.StdEncoding.DecodeString(h[len("abc123")+1:])
	if derr != nil {
		t.Fatalf("重试头 base64 段解码失败: %v", derr)
	}
	var sol map[string]any
	if err := json.Unmarshal(raw, &sol); err != nil {
		t.Fatalf("重试头 JSON 解析失败: %v", err)
	}
	if sol["validate"] != "val" {
		t.Fatalf("validate 丢失: %+v", sol)
	}
}

// call() 收到 X-Rpc-Aigis 响应头时必须返回 hoyoAigisError 且携带解析后的极验会话
func TestCallDetectsAigis(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Rpc-Aigis", base64.StdEncoding.EncodeToString([]byte(
			`{"session_id":"sid1","mmt_type":1,"data":"{\"gt\":\"g\",\"challenge\":\"c\"}"}`)))
		_, _ = w.Write([]byte(`{"retcode":1034,"message":"请完成验证"}`))
	}))
	defer srv.Close()

	f := newHoyoSession(t.TempDir() + "/hoyo_session.json")
	_, err := f.call(http.MethodPost, srv.URL, "", "{}", "", hoyoProdSalt, "5", "", false)
	var aigisErr *hoyoAigisError
	if !errors.As(err, &aigisErr) {
		t.Fatalf("期望 hoyoAigisError，得到 %v", err)
	}
	if aigisErr.Session.SessionID != "sid1" || aigisErr.Session.Data["gt"] != "g" {
		t.Fatalf("极验会话解析不符: %+v", aigisErr.Session)
	}

	// 正常响应（无 aigis 头、retcode=0）不应误报
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retcode":0,"message":"ok","data":{"sent_new":true}}`))
	}))
	defer srv2.Close()
	f2 := newHoyoSession(t.TempDir() + "/hoyo_session2.json")
	data, err := f2.call(http.MethodPost, srv2.URL, "", "{}", "", hoyoProdSalt, "5", "", false)
	if err != nil {
		t.Fatalf("正常响应不应报错: %v", err)
	}
	if string(data) == "" {
		t.Fatal("data 不应为空")
	}
}

// 头值形态兼容：裸 JSON / 引号包裹 / URL 编码 / 仅极验参数缺 session_id
func TestParseAigisHeaderShapes(t *testing.T) {
	cases := []struct {
		name   string
		header string
		wantGt string
	}{
		{"base64完整形态", base64.StdEncoding.EncodeToString([]byte(`{"session_id":"s1","mmt_type":1,"data":"{\"gt\":\"g1\",\"challenge\":\"c1\"}"}`)), "g1"},
		{"裸JSON", `{"session_id":"s2","mmt_type":1,"data":"{\"gt\":\"g2\",\"challenge\":\"c2\"}"}`, "g2"},
		{"引号包裹裸JSON", "`" + `"` + `{"session_id":"s3","data":"{\"gt\":\"g3\",\"challenge\":\"c3\"}"}` + `"` + "`", "g3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.header
			h = strings.ReplaceAll(h, "`", "")
			s, err := parseAigisHeader(h)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if s.Data["gt"] != tc.wantGt {
				t.Fatalf("gt 不符: %+v", s.Data)
			}
			if s.SessionID == "" {
				t.Fatal("session_id 不应为空")
			}
		})
	}
	// URL 编码形态
	u := "%7B%22session_id%22%3A%22s4%22%2C%22data%22%3A%22%7B%5C%22gt%5C%22%3A%5C%22g4%5C%22%7D%22%7D"
	s, err := parseAigisHeader(u)
	if err != nil {
		t.Fatalf("URL 编码解析失败: %v", err)
	}
	if s.Data["gt"] != "g4" {
		t.Fatalf("gt 不符: %+v", s.Data)
	}
	// 仅极验参数（缺 session_id → 自动补随机值）
	s2, err := parseAigisHeader(`{"gt":"g9","challenge":"c9"}`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s2.Data["gt"] != "g9" || s2.SessionID == "" {
		t.Fatalf("解析不符: %+v", s2)
	}
}
