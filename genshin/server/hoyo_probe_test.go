package main

// 诊断探针：用已登录会话对 game_record 各请求变体逐个探测（仅临时诊断用，验证完成后删除）
import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestProbeGameRecordVariants(t *testing.T) {
	if os.Getenv("HOYO_PROBE") == "" {
		t.Skip("设置 HOYO_PROBE=1 才执行（需已登录会话）")
	}
	raw, err := os.ReadFile("../data/hoyo_session.json")
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	var s struct {
		Aid       string `json:"aid"`
		CookieTok string `json:"cookie_token"`
		GameUID   string `json:"game_uid"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("解析会话失败: %v", err)
	}
	f := newHoyoSession(t.TempDir() + "/probe.json")
	f.aid, f.cookieTok, f.gameUID = s.Aid, s.CookieTok, s.GameUID
	cookie := "account_id=" + s.Aid + "; cookie_token=" + s.CookieTok
	body := `{"sort_type":1,"role_id":"` + s.GameUID + `","server":"cn_gi01"}`

	variants := []struct {
		name   string
		method string
		url    string
		query  string
		body   string
		noBQ   bool
		ctype  string
		cookie string
	}{
		{"基线 cookie_token", http.MethodPost, hoyoCharListURL, "", body, false, "5", cookie},
		{"cookie 加 mid", http.MethodPost, hoyoCharListURL, "", body, false, "5", cookie + "; mid=" + s.Aid},
		{"client_type 2 移动端", http.MethodPost, hoyoCharListURL, "", body, false, "2", cookie},
		{"DS 无 b/q", http.MethodPost, hoyoCharListURL, "", body, true, "5", cookie},
		{"GET index 带查询", http.MethodGet, "https://api-takumi-record.mihoyo.com/game_record/app/genshin/api/index", "role_id=" + s.GameUID + "&server=cn_gi01", "", false, "5", cookie},
	}
	for _, v := range variants {
		_, err := f.call(v.method, v.url, v.query, v.body, v.cookie, hoyoX4Salt, v.ctype, "", v.noBQ)
		if err == nil {
			t.Logf("%-40s 成功", v.name)
			continue
		}
		var ae *hoyoAPIError
		if errors.As(err, &ae) {
			t.Logf("%-40s retcode=%d msg=%s", v.name, ae.Retcode, ae.Message)
		} else {
			t.Logf("%-40s 其他错误: %v", v.name, err)
		}
	}

	// 探测 device_fp：获取后加 x-rpc-device_fp 头重试基线
	f.fetchDeviceFP()
	fp := f.deviceFP
	t.Logf("device_fp=%q", fp)
	if fp != "" {
		f.deviceFP = fp
		_, err := f.call(http.MethodPost, hoyoCharListURL, "", body, cookie, hoyoX4Salt, "5", "", false)
		if err == nil {
			t.Logf("%-40s 成功", "基线 + device_fp")
		} else {
			var ae *hoyoAPIError
			if errors.As(err, &ae) {
				t.Logf("%-40s retcode=%d msg=%s", "基线 + device_fp", ae.Retcode, ae.Message)
			} else {
				t.Logf("%-40s 其他错误: %v", "基线 + device_fp", err)
			}
		}
	}
}
