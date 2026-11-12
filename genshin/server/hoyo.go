// 米游社短信登录与全角色面板拉取（仅站长自用实验）：
// 链路 = passport 短信登录（RSA 加密手机号 + DS Gen2/X4 盐 + 移动端设备头；被风控要求极验时
//       经 X-Rpc-Aigis 透传前端弹窗，验证结果回填 x-rpc-aigis 自动重试）→ stoken → cookie_token → 游戏角色绑定
//       → game_record character/list + character/detail（DS Gen2/X4 盐）拉全角色面板。
// stoken 用后即弃；低权限 cookie_token 落盘（重启免重登）；短信/校验接口限频防滥用。
// 米哈游接口与盐值随时可能变更，常量失效时更新 https://github.com/UIGF-org/mihoyo-api-collect（issue #1 盐值总表）。
// 请求构造参照 MiyoQian（github.com/Marchen-orz/MiyoQian）生产可用实现。
package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hoyoProdSalt = "JwYDpKvLj6MrMqqYU6jTKF17KNO2PXoS" // 备用：账号系盐（DS Gen2）
	hoyoX4Salt   = "xV8v4Qu54lUKrEYFZkJhB8cuOh9Asafs" // 短信验证码链路与 game_record 的盐（DS Gen2，长期稳定）
	hoyoAppVer   = "2.106.2"                          // 米游社 App 版本（对齐 MiyoQian 生产可用值）
	hoyoAppID    = "bll8iq97cem8"                     // 米游社 App x-rpc-app_id
	hoyoMobileUA = "Mozilla/5.0 (Linux; Android 12; Unspecified Device) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/103.0.5060.129 Mobile Safari/537.36 miHoYoBBS/" + hoyoAppVer
	hoyoWebUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) miHoYoBBS/" + hoyoAppVer
	hoyoReferer  = "https://webstatic.mihoyo.com/"

	hoyoCreateCaptchaURL  = "https://passport-api.mihoyo.com/account/ma-cn-verifier/verifier/createLoginCaptcha"
	hoyoLoginByCaptchaURL = "https://passport-api.mihoyo.com/account/ma-cn-passport/app/loginByMobileCaptcha"
	hoyoCookieBySTokenURL = "https://passport-api.mihoyo.com/account/auth/api/getCookieAccountInfoBySToken"
	hoyoGameRolesURL      = "https://api-takumi.mihoyo.com/binding/api/getUserGameRolesByCookie?game_biz=hk4e_cn"
	hoyoCharListURL       = "https://api-takumi-record.mihoyo.com/game_record/app/genshin/api/character/list"
	hoyoCharDetailURL     = "https://api-takumi-record.mihoyo.com/game_record/app/genshin/api/character/detail"
)

// 米游社账号体系 RSA 公钥（PKCS1v15 加密手机号/区号），来源 UIGF-org/mihoyo-api-collect issue #1
const hoyoRSAPubKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDDvekdPMHN3AYhm/vktJT+YJr7
cI5DcsNKqdsx5DZX0gDuWFuIjzdwButrIYPNmRJ1G8ybDIF7oDW2eEpm5sMbL9zs
9ExXCdvqrn51qELbqj0XxtMTIpaCHFSI50PfPpTFV9Xt/hmyVwokoOXFlAEgCn+Q
CgGs52bFoYMtyi+xEQIDAQAB
-----END PUBLIC KEY-----`

// 元素 → 元素伤害加成 property_type（40~46=火/雷/水/草/风/岩/冰），用于挑选 bonus
var hoyoElemProp = map[string]float64{
	"Pyro": 40, "Electro": 41, "Hydro": 42, "Dendro": 43, "Anemo": 44, "Geo": 45, "Cryo": 46,
	"火": 40, "雷": 41, "水": 42, "草": 43, "风": 44, "岩": 45, "冰": 46,
}

var hoyoPhoneRe = regexp.MustCompile(`^1\d{10}$`)

// aigisSession 极验挑战会话：X-Rpc-Aigis 响应头（base64 JSON）解码后。
// data 为嵌套 JSON：GT3 形态 {gt, challenge}、GT4 形态 {captcha_id, lot_number}
type aigisSession struct {
	SessionID string         `json:"session_id"`
	MmtType   int            `json:"mmt_type"`
	Data      map[string]any `json:"data"`
}

// hoyoAigisError 米哈游要求人机验证，携带可透传给前端的极验会话
type hoyoAigisError struct{ Session *aigisSession }

func (e *hoyoAigisError) Error() string { return "触发米哈游人机验证，请在弹窗中完成滑动验证" }

// parseAigisHeader 解析 X-Rpc-Aigis 响应头。实测头值形态不唯一（裸 JSON / URL 编码 / 引号包裹 /
// 四种 base64 变体都可能出现），故做兼容链；解析失败时错误信息携带原始头值便于定位
func parseAigisHeader(v string) (*aigisSession, error) {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"'`)
	if v == "" {
		return nil, errors.New("aigis 头为空")
	}
	if strings.HasPrefix(v, "%") { // URL 编码形态
		if u, uerr := url.QueryUnescape(v); uerr == nil {
			v = strings.TrimSpace(strings.Trim(u, `"'`))
		}
	}
	var (
		s   *aigisSession
		err error
	)
	if strings.HasPrefix(v, "{") { // 裸 JSON 形态
		s, err = decodeAigisJSON([]byte(v))
	} else { // base64 形态（std/url × 带填充/无填充 依次尝试）
		var raw []byte
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			var d []byte
			d, err = enc.DecodeString(v)
			if err == nil {
				raw = d
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("aigis base64 解码失败: %w（原始头值 %.120s）", err, v)
		}
		s, err = decodeAigisJSON(raw)
	}
	if err != nil {
		return nil, fmt.Errorf("%w（原始头值 %.120s）", err, v)
	}
	if s.SessionID == "" { // 个别形态不下发会话 ID，补随机值供重试头配对
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		s.SessionID = fmt.Sprintf("%x", b)
	}
	return s, nil
}

// decodeAigisJSON 兼容两种 JSON 形态：完整 aigis JSON（session_id/data 嵌套字符串）
// 或仅极验参数 JSON（顶层 gt/challenge 或 captcha_id/lot_number）
func decodeAigisJSON(raw []byte) (*aigisSession, error) {
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("aigis JSON 解析失败: %w", err)
	}
	s := &aigisSession{Data: map[string]any{}}
	if v, ok := probe["session_id"].(string); ok {
		s.SessionID = v
	}
	if v, ok := probe["mmt_type"].(float64); ok {
		s.MmtType = int(v)
	}
	if dv, ok := probe["data"]; ok { // 完整形态：data 为嵌套 JSON 字符串或对象
		switch t := dv.(type) {
		case string:
			if strings.HasPrefix(strings.TrimSpace(t), "{") {
				if err := json.Unmarshal([]byte(t), &s.Data); err != nil {
					return nil, fmt.Errorf("aigis data 解析失败: %w", err)
				}
			}
		case map[string]any:
			s.Data = t
		}
	}
	if len(s.Data) == 0 { // 仅极验参数形态：顶层取参
		for _, k := range []string{"gt", "challenge", "captcha_id", "lot_number", "captcha_output", "pass_token", "gen_time"} {
			if v, ok := probe[k]; ok {
				s.Data[k] = v
			}
		}
	}
	return s, nil
}

// buildAigisHeader 组装重试请求头 x-rpc-aigis："{session_id};base64(用户验证结果 JSON)"
func buildAigisHeader(sessionID string, solution map[string]any) (string, error) {
	raw, err := json.Marshal(solution)
	if err != nil {
		return "", err
	}
	return sessionID + ";" + base64.StdEncoding.EncodeToString(raw), nil
}

type hoyoSession struct {
	mu          sync.Mutex
	client      *http.Client
	deviceID    string
	deviceName  string // 移动端设备名（米游社 App 设备头）
	deviceModel string // 移动端设备型号
	deviceFP    string // x-rpc-device_fp（game_record 等接口可能强制要求）
	path        string // 会话持久化文件（genshin/data/hoyo_session.json，静态根之外；仅存低权限 cookie_token）
	charIndex   func() (map[int64][2]string, error) // avatarId → [中文名, 元素]（Enka 展柜映射，源自数据库）
	actionType  string                              // createLoginCaptcha 返回的 action_type，verify 时原样回传
	aid         string
	cookieTok   string // game_record 用的低权限凭据（约一天失效），stoken 换到即弃不保存
	gameUID     string
	lastPanel   time.Time
	verifyFails int                    // 同一验证码会话连续校验失败次数（达 5 次作废，须重发短信）
	verifyIP    map[string][]time.Time // 校验接口 IP 限频（10 分钟 5 次，防验证码暴力枚举）
	smsIP       map[string][]time.Time // 同 IP 短信发送时刻（60 秒 1 条）
	smsGlobal   []time.Time            // 全局短信发送时刻（1 小时 10 条）
}

func newHoyoSession(path string) *hoyoSession {
	// 随机设备 ID 进程内保持一致（一致性越好风控越少）
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	dev := fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	f := &hoyoSession{
		client: &http.Client{
			Timeout: 20 * time.Second,
			// 直连（不走 watchd 环境继承的 HTTPS_PROXY）：代理出口多为境外 IP，
			// 境外 IP 调国服账号接口会显著提高米哈游风控等级
			Transport: &http.Transport{ TLSHandshakeTimeout: 10 * time.Second },
		},
		deviceID:    dev,
		deviceName:  "Xiaomi 12",
		deviceModel: "2201123G",
		path:        path,
		verifyIP:    map[string][]time.Time{},
		smsIP:       map[string][]time.Time{},
	}
	// 恢复持久化会话（仅低权限 cookie_token），避免服务器重启后重新发短信触发风控
	if raw, err := os.ReadFile(path); err == nil {
		var s struct {
			Aid        string `json:"aid"`
			CookieTok  string `json:"cookie_token"`
			GameUID    string `json:"game_uid"`
		}
		if json.Unmarshal(raw, &s) == nil && s.CookieTok != "" && s.GameUID != "" {
			f.aid, f.cookieTok, f.gameUID = s.Aid, s.CookieTok, s.GameUID
			slog.Info("米游社会话已从磁盘恢复", "gameUID", f.gameUID)
		}
	} else if !os.IsNotExist(err) {
		slog.Error("读取米游社会话文件失败", "path", path, "err", err)
	}
	return f
}

// fetchDeviceFP 向公共设备指纹服务申请 device_fp（无鉴权；失败返回空串不阻塞）
func (f *hoyoSession) fetchDeviceFP() string {
	if f.deviceFP != "" {
		return f.deviceFP
	}
	body := `{"device_id":"` + f.deviceID + `","device_name":"` + hoyoMobileUA + `","device_type":"2","seed_id":"` + f.deviceID + `","seed_time":"` + strconv.FormatInt(time.Now().Unix(), 10) + `","platform":"2","ext_fields":"{}","app_sn":"ys","device_board":"","cpu_type":"","oaid":"","openudid":""}`
	req, err := http.NewRequest(http.MethodPost, "https://public-data-api.mihoyo.com/device-fp/api/getFp", strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", hoyoMobileUA)
	resp, err := f.client.Do(req)
	if err != nil {
		slog.Error("获取 device_fp 失败", "err", err)
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var d struct {
		Data struct {
			DeviceFP string `json:"device_fp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.Data.DeviceFP == "" {
		slog.Error("device_fp 响应解析失败", "body", truncateRunes(string(raw), 200))
		return ""
	}
	f.deviceFP = d.Data.DeviceFP
	slog.Info("device_fp 获取成功", "fp", truncateRunes(f.deviceFP, 20))
	return f.deviceFP
}

// saveSession 会话落盘（temp+rename；仅低权限 cookie_token，文件在静态根之外）
func (f *hoyoSession) saveSession() error {
	raw, _ := json.MarshalIndent(map[string]string{
		"aid": f.aid, "cookie_token": f.cookieTok, "game_uid": f.gameUID,
	}, "", "  ")
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// hoyoRSAEnc PKCS1v15 加密 + base64（米游社 passport 接口要求手机号/区号密文传输）
func hoyoRSAEnc(plain string) (string, error) {
	block, _ := pem.Decode([]byte(hoyoRSAPubKey))
	if block == nil {
		return "", errors.New("RSA 公钥解析失败")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("非 RSA 公钥")
	}
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// dsGen2 DS 签名："{t},{r},{md5hex(签名串)}"。
// withBQ=true（passport 链路）：签名串含 &b={body}&q={query}；false（game_record 链路）：不含
func dsGen2(salt, body, query string, withBQ bool) string {
	t := time.Now().Unix()
	r := mrand.Intn(100000) + 100000
	if r == 100000 {
		r = 642367
	}
	raw := fmt.Sprintf("salt=%s&t=%d&r=%d", salt, t, r)
	if withBQ {
		raw += fmt.Sprintf("&b=%s&q=%s", body, query)
	}
	return fmt.Sprintf("%d,%d,%x", t, r, md5.Sum([]byte(raw)))
}

type hoyoResp struct {
	Retcode int             `json:"retcode"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// hoyoAPIError 米哈游业务失败（retcode != 0），Message 可直接透传给前端
type hoyoAPIError struct {
	Retcode int
	Message string
}

func (e *hoyoAPIError) Error() string {
	return fmt.Sprintf("retcode=%d %s", e.Retcode, e.Message)
}

// call 米哈游接口统一请求：JSON POST/GET + DS2 签名 + 风控头检测（返回 hoyoAigisError 时前端需完成极验后带 aigis 重试）；retcode!=0 视为业务失败
func (f *hoyoSession) call(method, url, query, body, cookie, salt, clientType, aigisHeader string, noBQ bool) (json.RawMessage, error) {
	var rd io.Reader
	if method == http.MethodPost {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return nil, err
	}
	if method == http.MethodGet && query != "" {
		req.URL.RawQuery = query
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DS", dsGen2(salt, body, query, !noBQ))
	req.Header.Set("x-rpc-app_version", hoyoAppVer)
	req.Header.Set("x-rpc-client_type", clientType)
	req.Header.Set("x-rpc-device_id", f.deviceID)
	req.Header.Set("x-rpc-app_id", hoyoAppID)
	req.Header.Set("x-rpc-device_name", f.deviceName)
	req.Header.Set("x-rpc-device_model", f.deviceModel)
	req.Header.Set("X-Requested-With", "com.mihoyo.hyperion")
	if clientType == "2" {
		// 米游社 App 移动端形态（短信验证码链路，对齐 MiyoQian 生产可用实现）
		req.Header.Set("User-Agent", hoyoMobileUA)
		req.Header.Set("Referer", "https://user.miyoushe.com/")
		if strings.Contains(url, "createLoginCaptcha") {
			req.Header.Set("x-rpc-game_biz", "hk4e_cn")
		}
	} else { // 网页形态（game_record / Cookie 兑换）
		req.Header.Set("User-Agent", hoyoWebUA)
		req.Header.Set("Referer", hoyoReferer)
		req.Header.Set("Origin", "https://webstatic.mihoyo.com")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if f.deviceFP != "" {
		req.Header.Set("x-rpc-device_fp", f.deviceFP)
	}
	if aigisHeader != "" {
		req.Header.Set("x-rpc-aigis", aigisHeader) // 人机验证结果回填重试
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if v := resp.Header.Get("X-Rpc-Aigis"); v != "" {
		slog.Warn("米哈游接口触发人机验证", "url", url, "aigisRaw", strings.TrimSpace(v[:min(len(v), 160)]))
		s, perr := parseAigisHeader(v)
		if perr != nil {
			return nil, fmt.Errorf("米哈游要求人机验证且会话解析失败: %w", perr)
		}
		return nil, &hoyoAigisError{Session: s}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<19))
	if err != nil {
		return nil, err
	}
	var hr hoyoResp
	if err := json.Unmarshal(raw, &hr); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	if hr.Retcode != 0 {
		return nil, &hoyoAPIError{Retcode: hr.Retcode, Message: hr.Message}
	}
	return hr.Data, nil
}

// allowSMS 短信发送限频：同 IP 60 秒 1 条、全局 1 小时 10 条（调用方需持锁）
func (f *hoyoSession) allowSMS(ip string) bool {
	now := time.Now()
	recent := f.smsIP[ip][:0]
	for _, t := range f.smsIP[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 1 {
		f.smsIP[ip] = recent
		return false
	}
	f.smsIP[ip] = append(recent, now)
	kept := f.smsGlobal[:0]
	for _, t := range f.smsGlobal {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 10 {
		f.smsGlobal = kept
		return false
	}
	f.smsGlobal = append(kept, now)
	return true
}

// allowVerify 校验接口限频：同 IP 10 分钟 5 次（6 位验证码防暴力枚举；调用方需持锁）
func (f *hoyoSession) allowVerify(ip string) bool {
	now := time.Now()
	recent := f.verifyIP[ip][:0]
	for _, t := range f.verifyIP[ip] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		f.verifyIP[ip] = recent
		return false
	}
	f.verifyIP[ip] = append(recent, now)
	return true
}

// hoyoUserMsg 业务失败时把米哈游原始消息透传给前端，便于定位（其余走兜底文案）
func hoyoUserMsg(err error, fallback string) string {
	var apiErr *hoyoAPIError
	if errors.As(err, &apiErr) {
		return "米哈游：" + apiErr.Message
	}
	return fallback
}

func hoyoWriteErr(w http.ResponseWriter, code int, err error, userMsg string) {
	slog.Error("米游社接口失败", "err", err, "userMsg", userMsg)
	writeJSON(w, code, map[string]string{"error": userMsg})
}

// handleSMSSend POST /api/hoyo/sms/send {phone, aigis?}：RSA 加密后请求短信验证码；
// 被风控要求极验时返回 {error, aigis}，前端完成滑动验证后带 aigis 重试（重试跳过限频）
func (f *hoyoSession) handleSMSSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone string `json:"phone"`
		Aigis *struct {
			SessionID string         `json:"session_id"`
			Data      map[string]any `json:"data"`
		} `json:"aigis"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误"})
		return
	}
	phone := strings.TrimSpace(in.Phone)
	if !hoyoPhoneRe.MatchString(phone) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "手机号格式有误"})
		return
	}
	aigisHeader := ""
	if in.Aigis != nil && in.Aigis.SessionID != "" {
		h, err := buildAigisHeader(in.Aigis.SessionID, in.Aigis.Data)
		if err != nil {
			hoyoWriteErr(w, http.StatusBadRequest, err, "人机验证结果格式有误")
			return
		}
		aigisHeader = h
	}
	ip := clientIP(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	// 首次发送限频防轰炸；带极验结果的重试跳过（极验已证明是人）
	if aigisHeader == "" && !f.allowSMS(ip) {
		slog.Warn("米游社短信发送触发限频", "ip", ip)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "发送太频繁，请稍后再试"})
		return
	}
	encArea, err := hoyoRSAEnc("+86")
	if err != nil {
		hoyoWriteErr(w, http.StatusInternalServerError, err, "服务端加密失败")
		return
	}
	encMobile, err := hoyoRSAEnc(phone)
	if err != nil {
		hoyoWriteErr(w, http.StatusInternalServerError, err, "服务端加密失败")
		return
	}
	body := `{"area_code":"` + encArea + `","mobile":"` + encMobile + `"}`
	data, err := f.call(http.MethodPost, hoyoCreateCaptchaURL, "", body, "", hoyoX4Salt, "2", aigisHeader, false)
	if err != nil {
		var aigisErr *hoyoAigisError
		if errors.As(err, &aigisErr) {
			// 经 CF 回源时 5xx 响应体会被错误页替换，业务语义一律 200 + ok:false 携带 error/aigis
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "aigis": aigisErr.Session})
			return
		}
		hoyoWriteErr(w, http.StatusOK, err, hoyoUserMsg(err, "验证码发送失败（米哈游接口）"))
		return
	}
	var d struct {
		ActionType string `json:"action_type"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.ActionType == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "验证码发送失败（米哈游接口）"})
		return
	}
	f.actionType = d.ActionType
	slog.Info("米游社短信验证码已发送", "phoneTail", phone[len(phone)-4:], "ip", ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSMSVerify POST /api/hoyo/sms/verify {phone, code, aigis?}：短信码登录 → stoken → cookie_token → 游戏角色
func (f *hoyoSession) handleSMSVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
		Aigis *struct {
			SessionID string         `json:"session_id"`
			Data      map[string]any `json:"data"`
		} `json:"aigis"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误"})
		return
	}
	phone, code := strings.TrimSpace(in.Phone), strings.TrimSpace(in.Code)
	if !hoyoPhoneRe.MatchString(phone) || code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "手机号或验证码格式有误"})
		return
	}
	aigisHeader := ""
	if in.Aigis != nil && in.Aigis.SessionID != "" {
		h, err := buildAigisHeader(in.Aigis.SessionID, in.Aigis.Data)
		if err != nil {
			hoyoWriteErr(w, http.StatusBadRequest, err, "人机验证结果格式有误")
			return
		}
		aigisHeader = h
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// 防暴力枚举：校验接口同 IP 10 分钟 5 次；连续错 5 次作废当前验证码会话
	ip := clientIP(r)
	if !f.allowVerify(ip) {
		slog.Warn("米游社验证码校验触发限频", "ip", ip)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "尝试过于频繁，请 10 分钟后再试"})
		return
	}
	actionType := f.actionType
	if actionType == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "请先发送验证码"})
		return
	}
	encArea, err := hoyoRSAEnc("+86")
	if err != nil {
		hoyoWriteErr(w, http.StatusInternalServerError, err, "服务端加密失败")
		return
	}
	encMobile, err := hoyoRSAEnc(phone)
	if err != nil {
		hoyoWriteErr(w, http.StatusInternalServerError, err, "服务端加密失败")
		return
	}
	body := `{"area_code":"` + encArea + `","action_type":"` + actionType + `","captcha":"` + code + `","mobile":"` + encMobile + `"}`
	data, err := f.call(http.MethodPost, hoyoLoginByCaptchaURL, "", body, "", hoyoX4Salt, "2", aigisHeader, false)
	if err != nil {
		var aigisErr *hoyoAigisError
		if errors.As(err, &aigisErr) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "aigis": aigisErr.Session})
			return
		}
		var apiErr *hoyoAPIError
		if errors.As(err, &apiErr) { // 米哈游业务失败：按验证码错误计次，连续 5 次作废会话
			f.verifyFails++
			slog.Warn("米游社验证码校验失败", "fails", f.verifyFails, "retcode", apiErr.Retcode, "ip", ip)
			if f.verifyFails >= 5 {
				f.actionType, f.verifyFails = "", 0
				slog.Warn("验证码连续错误达上限，作废当前登录会话", "ip", ip)
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "验证码错误次数过多，请重新发送验证码"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": hoyoUserMsg(err, "验证码校验失败（检查短信码或稍后再试）")})
			return
		}
		hoyoWriteErr(w, http.StatusOK, err, "验证码校验失败（检查短信码或稍后再试）")
		return
	}
	f.verifyFails = 0
	var d struct {
		Token struct {
			Token string `json:"token"`
		} `json:"token"`
		UserInfo struct {
			Aid string `json:"aid"`
			Mid string `json:"mid"`
		} `json:"user_info"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Token.Token == "" || d.UserInfo.Aid == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "登录响应解析失败"})
		return
	}
	// stoken 为高权限长期凭据：仅用于换 cookie_token，换取成功后即丢弃不保存（凭据最小化）
	stoken, mid := d.Token.Token, d.UserInfo.Mid
	f.aid = d.UserInfo.Aid

	// stoken → cookie_token
	// MiyoQian 生产口径：stoken 走 query 参与 DS 签名，Cookie 带 mid+stoken
	data, err = f.call(http.MethodGet, hoyoCookieBySTokenURL, "stoken="+stoken, "", "mid="+mid+"; stoken="+stoken, hoyoX4Salt, "5", "", false)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "登录成功但换取 Cookie 失败"})
		return
	}
	var ck struct {
		CookieToken string `json:"cookie_token"`
		UID         string `json:"uid"`
	}
	if err := json.Unmarshal(data, &ck); err != nil || ck.CookieToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "登录成功但换取 Cookie 失败"})
		return
	}
	f.cookieTok = ck.CookieToken

	// 绑定的游戏角色（国服优先）
	data, err = f.call(http.MethodGet, hoyoGameRolesURL, "game_biz=hk4e_cn", "", "account_id="+f.aid+"; cookie_token="+f.cookieTok, hoyoX4Salt, "2", "", false)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "登录成功但获取游戏角色失败"})
		return
	}
	var roles struct {
		List []struct {
			GameUID string `json:"game_uid"`
			Region  string `json:"region"`
		} `json:"list"`
	}
	if err := json.Unmarshal(data, &roles); err != nil || len(roles.List) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "该账号未绑定原神角色"})
		return
	}
	f.gameUID = roles.List[0].GameUID
	for _, ro := range roles.List {
		if ro.Region == "cn_gi01" {
			f.gameUID = ro.GameUID
			break
		}
	}
	slog.Info("米游社短信登录成功", "aidTail", f.aid[len(f.aid)-4:], "gameUID", f.gameUID)
	if err := f.saveSession(); err != nil {
		slog.Error("米游社会话落盘失败", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// enkaPanels Enka.Network 展柜降级路径：game_record 被风控拒时改用公开展柜数据（仅展柜角色，无圣遗物）
func (f *hoyoSession) enkaPanels() (map[string]map[string]any, error) {
	resp, err := f.client.Get("https://enka.network/api/uid/" + f.gameUID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var d struct {
		PlayerInfo     *struct{} `json:"playerInfo"`
		AvatarInfoList []struct {
			AvatarID     int64              `json:"avatarId"`
			TalentIdList []any              `json:"talentIdList"`
			FightPropMap map[string]float64 `json:"fightPropMap"`
		} `json:"avatarInfoList"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("Enka 响应解析失败: %w", err)
	}
	if len(d.AvatarInfoList) == 0 {
		return nil, errors.New("该账号未开启角色展柜详情（游戏内需开启并展示角色）")
	}
	// avatarId → 角色中文名/元素（源自数据库 characters 表）
	idx, err := f.charIndex()
	if err != nil {
		slog.Error("角色索引读取失败", "err", err)
	}
	chars := map[string]map[string]any{}
	for _, a := range d.AvatarInfoList {
		info, ok := idx[a.AvatarID]
		if !ok {
			continue
		}
		fp := a.FightPropMap
		get := func(k string) float64 { return fp[k] }
		panel := map[string]any{
			"level":         90,
			"constellation": len(a.TalentIdList),
			"atk":   strconv.FormatFloat(get("2001"), 'f', 1, 64),
			"hp":    strconv.FormatFloat(get("2000"), 'f', -1, 64),
			"def":   strconv.FormatFloat(get("2002"), 'f', -1, 64),
			"cr":    strconv.FormatFloat(get("20")*100, 'f', 1, 64),
			"cd":    strconv.FormatFloat(get("22")*100, 'f', 1, 64),
			"em":    strconv.FormatFloat(get("28"), 'f', 1, 64),
		}
		if pt, ok := hoyoElemProp[info[1]]; ok {
			panel["bonus"] = strconv.FormatFloat(get(strconv.Itoa(int(pt)))*100, 'f', 1, 64)
		}
		chars[info[0]] = panel
	}
	if len(chars) == 0 {
		return nil, errors.New("展柜角色均无法匹配本地角色库")
	}
	return chars, nil
}

// handlePanels POST /api/hoyo/panels：拉取全角色面板并解析为伤害计算所需字段（20 秒节流）
func (f *hoyoSession) handlePanels(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.cookieTok == "" || f.gameUID == "" {
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "请先短信登录米游社账号"})
		return
	}
	if time.Since(f.lastPanel) < 20*time.Second {
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "拉取太频繁，请 20 秒后再试"})
		return
	}
	cookie := "account_id=" + f.aid + "; cookie_token=" + f.cookieTok
	uid := f.gameUID

	listBody := `{"sort_type":1,"role_id":"` + uid + `","server":"cn_gi01"}`
	data, err := f.call(http.MethodPost, hoyoCharListURL, "", listBody, cookie, hoyoX4Salt, "5", "", true)
	if err != nil {
		f.mu.Unlock()
		msg := hoyoUserMsg(err, "拉取角色列表失败")
		var aigisErr *hoyoAigisError
		if errors.As(err, &aigisErr) {
			msg = err.Error()
		} else if strings.Contains(err.Error(), "10101") {
			msg = "登录已失效，请重新短信登录"
		}
		slog.Error("拉取角色列表失败", "err", err, "retcode", func() any { var ae *hoyoAPIError; if errors.As(err, &ae) { return ae.Retcode }; return nil }())
		// game_record 被风控/参数拒时降级：Enka.Network 公开展柜数据（仅展柜角色）
		if chars, en := f.enkaPanels(); en == nil {
			slog.Info("Enka 展柜面板拉取成功", "chars", len(chars))
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().Format("2006-01-02 15:04"), "chars": chars, "source": "enka"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg})
		return
	}
	var cl struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(data, &cl); err != nil || len(cl.List) == 0 {
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "角色列表解析失败"})
		return
	}
	ids := make([]int64, 0, len(cl.List))
	for _, c := range cl.List {
		ids = append(ids, c.ID)
	}
	idJSON, _ := json.Marshal(ids)
	detailBody := `{"sort_type":1,"character_ids":` + string(idJSON) + `,"role_id":"` + uid + `","server":"cn_gi01"}`
	data, err = f.call(http.MethodPost, hoyoCharDetailURL, "", detailBody, cookie, hoyoX4Salt, "5", "", true)
	if err != nil {
		f.mu.Unlock()
		msg := hoyoUserMsg(err, "拉取角色详情失败")
		var aigisErr *hoyoAigisError
		if errors.As(err, &aigisErr) {
			msg = err.Error()
		} else if strings.Contains(err.Error(), "10101") {
			msg = "登录已失效，请重新短信登录"
		}
		slog.Error("拉取角色详情失败", "err", err, "retcode", func() any { var ae *hoyoAPIError; if errors.As(err, &ae) { return ae.Retcode }; return nil }())
		if chars, en := f.enkaPanels(); en == nil {
			slog.Info("Enka 展柜面板拉取成功", "chars", len(chars))
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().Format("2006-01-02 15:04"), "chars": chars, "source": "enka"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg})
		return
	}
	f.lastPanel = time.Now()
	f.mu.Unlock()

	// 解析 detail：selected_properties[].final 即最终面板；百分比字符串按类型换算
	var det struct {
		List []struct {
			Base struct {
				Name                    string `json:"name"`
				Level                   int    `json:"level"`
				Element                 string `json:"element"`
				ActivedConstellationNum int    `json:"actived_constellation_num"`
			} `json:"base"`
			SelectedProperties []struct {
				PropertyType int    `json:"property_type"`
				Final        string `json:"final"`
			} `json:"selected_properties"`
		} `json:"list"`
	}
	if err := json.Unmarshal(data, &det); err != nil || len(det.List) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "角色详情解析失败"})
		return
	}
	chars := map[string]map[string]any{}
	for _, c := range det.List {
		panel := map[string]any{"level": c.Base.Level, "constellation": c.Base.ActivedConstellationNum}
		bonusBest := -1.0
		for _, p := range c.SelectedProperties {
			v := hoyoParseFinal(p.Final, p.PropertyType)
			if v == nil {
				continue
			}
			switch p.PropertyType {
			case 2000:
				panel["hp"] = strconv.FormatFloat(*v, 'f', -1, 64)
			case 2001:
				panel["atk"] = strconv.FormatFloat(*v, 'f', -1, 64)
			case 2002:
				panel["def"] = strconv.FormatFloat(*v, 'f', -1, 64)
			case 20:
				panel["cr"] = strconv.FormatFloat(*v, 'f', 1, 64)
			case 22:
				panel["cd"] = strconv.FormatFloat(*v, 'f', 1, 64)
			case 28:
				panel["em"] = strconv.FormatFloat(*v, 'f', 1, 64)
			default:
				if p.PropertyType >= 40 && p.PropertyType <= 46 {
					if want, ok := hoyoElemProp[c.Base.Element]; ok && float64(p.PropertyType) == want {
						panel["bonus"] = strconv.FormatFloat(*v, 'f', 1, 64)
					} else if *v > bonusBest { // 元素名不匹配时退回最大项
						bonusBest = *v
						if _, has := panel["bonus"]; !has {
							panel["bonus"] = strconv.FormatFloat(*v, 'f', 1, 64)
						}
					}
				}
			}
		}
		if c.Base.Name != "" {
			chars[c.Base.Name] = panel
		}
	}
	slog.Info("米游社面板拉取成功", "chars", len(chars), "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"time":  time.Now().Format("2006-01-02 15:04"),
		"chars": chars,
	})
}

// hoyoParseFinal 解析米游社格式化面板值："50.0%" → 50.0（百分比）；"13508" → 13508；
// 暴击率/暴伤等分数型（无 % 且数值小）按小数 ×100
func hoyoParseFinal(s string, propType int) *float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, "+", ""))
	isPercent := strings.HasSuffix(s, "%")
	s = strings.TrimSuffix(s, "%")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	switch {
	case isPercent:
		// 已是百分数值
	case propType == 20 || propType == 22 || propType == 23:
		if v <= 5 { // 分数形式（0.5 = 50%）
			v *= 100
		}
	}
	return &v
}

// handleClientLog POST /api/hoyo/client-log {message, stack}：前端人机验证链路异常上报（仅写日志，截断防滥用）
func (f *hoyoSession) handleClientLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式有误"})
		return
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 message"})
		return
	}
	slog.Error("前端米游社链路异常上报",
		"message", truncateRunes(msg, 300),
		"stack", truncateRunes(strings.TrimSpace(in.Stack), 600),
		"ip", clientIP(r),
		"ua", truncateRunes(r.Header.Get("User-Agent"), 140))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// registerHoyo 注册米游社接口（公开站内，站长自用；凭据在服务端内存）
func registerHoyo(siteMux *http.ServeMux, f *hoyoSession) {
	siteMux.HandleFunc("POST /api/hoyo/sms/send", f.handleSMSSend)
	siteMux.HandleFunc("POST /api/hoyo/sms/verify", f.handleSMSVerify)
	siteMux.HandleFunc("POST /api/hoyo/panels", f.handlePanels)
	siteMux.HandleFunc("POST /api/hoyo/client-log", f.handleClientLog)
}
