// genshin-server 原神数值站独立服务：静态站点（genshin/site/）+ 留言板接口（数据落 genshin/data/feedback.json），
// 与镜像站 server.exe 零包共享、零进程关联——独立故障域、独立启停。
// 使用站 :50055 对外（Cloudflare 子域名回源），运维站 127.0.0.1:50056 仅本机状态查询。
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const version = "genshin-server/1.0"

// 留言管理页：编译进二进制（源文件在 server/ 目录，不在公网静态根 site/ 内）
//
//go:embed admin.html
var adminPageHTML []byte

var startTime = time.Now()

func main() {
	self, err := os.Executable()
	if err != nil {
		slog.Error("获取自身路径失败", "err", err)
		self = "."
	}
	selfDir := filepath.Dir(self)

	// 默认值贴合部署路径：genshin-server.exe 位于 backend/bin/，静态目录为仓库根 genshin/
	addr := flag.String("addr", ":50055", "使用站监听地址（对外经 Cloudflare 回源）")
	adminAddr := flag.String("admin", "127.0.0.1:50056", "运维状态监听地址（仅本机）")
	dirF := flag.String("dir", "", "静态站点目录（默认 仓库根/genshin/）")
	dbServer := flag.String("db-server", "localhost", "SQL Server 地址")
	dbPort := flag.String("db-port", "1433", "SQL Server 端口")
	dbUser := flag.String("db-user", "sa", "SQL Server 用户")
	dbPass := flag.String("db-pass", "123456", "SQL Server 密码")
	dbEncrypt := flag.String("db-encrypt", "disable", "SQL Server 加密模式")
	dbName := flag.String("db-name", "genshin", "留言与角色数据库名")
	flag.Parse()

	dir := *dirF
	if dir == "" {
		// genshin/bin/genshin-server.exe → genshin/site/
		dir = filepath.Join(filepath.Dir(selfDir), "site")
	}
	if fi, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || fi.IsDir() {
		slog.Error("静态站点目录无效", "dir", dir, "err", err)
		os.Exit(1)
	}

	// SQL Server 独立 genshin 库：建库建表 + legacy JSON 迁移（留言）+ 角色文档导入
	db, err := openGenshinDB(*dbServer, *dbPort, *dbUser, *dbPass, *dbEncrypt, *dbName)
	if err != nil {
		slog.Error("数据库初始化失败", "err", err)
		os.Exit(1)
	}
	if err := migrateLegacyFeedback(db, filepath.Join(filepath.Dir(selfDir), "data", "feedback.json")); err != nil {
		slog.Error("legacy 留言迁移失败", "err", err)
		os.Exit(1)
	}
	imported, err := ImportCharactersFromDir(db, filepath.Join(dir, "data", "characters"))
	if err != nil {
		slog.Error("角色数据导入失败", "err", err)
		os.Exit(1)
	}
	slog.Info("角色数据导入完成", "count", imported)

	fbStore := newFeedbackStore(db)
	hoyo := newHoyoSession(filepath.Join(filepath.Dir(selfDir), "data", "hoyo_session.json"))
	hoyo.charIndex = func() (map[int64][2]string, error) { return CharIndexFromDB(db) } // Enka 展柜映射数据源
	charAPI := &charactersAPI{db: db}
	if err := charAPI.reload(); err != nil {
		slog.Error("角色数据缓存加载失败", "err", err)
		os.Exit(1)
	}

	slog.Info("genshin-server 启动", "version", version,
		"addr", *addr, "admin", *adminAddr, "dir", dir, "db", *dbName)

	// ===== 使用站：纯静态（index.html / detail.html / data/ / assets/）=====
	// 缓存策略：CSS/HTML/JSON no-cache（内容更新即时生效，配合 ETag 304 零开销）；
	// 头像图内容不变，长缓存。
	cachePolicy := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/assets/avatars/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			next.ServeHTTP(w, r)
		})
	}
	siteMux := http.NewServeMux()
	siteMux.Handle("/", cachePolicy(http.FileServer(http.Dir(dir))))
	registerHoyo(siteMux, hoyo) // 米游社短信登录与面板拉取（具体路径模式比 "/" 更精确，优先匹配）
	siteMux.HandleFunc("GET /api/characters", charAPI.handleList)
	siteMux.HandleFunc("GET /api/characters/{name}", charAPI.handleDetail)
	siteSrv := &http.Server{Addr: *addr, Handler: siteMux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("使用站开始监听", "addr", *addr)
		if e := siteSrv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			slog.Error("使用站监听异常退出", "err", e)
			os.Exit(1)
		}
	}()

	// ===== 运维站：留言管理页 + 状态 JSON + 留言管理接口（仅本机，与主站后台同惯例）=====
	adminMux := http.NewServeMux()
	registerFeedback(siteMux, adminMux, fbStore)
	adminPage := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(adminPageHTML)
	}
	adminMux.HandleFunc("GET /{$}", adminPage)
	adminMux.HandleFunc("GET /admin", adminPage)
	// 管理页引用的样式等静态资产（同 site/ 目录，含缓存策略）
	adminMux.Handle("GET /assets/", cachePolicy(http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(dir, "assets"))))))
	adminMux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":      "genshin-server",
			"version":      version,
			"pid":          os.Getpid(),
			"uptime_s":     int(time.Since(startTime).Seconds()),
			"site_dir":     dir,
			"memory_alloc": mem.Alloc,
			"goroutines":   runtime.NumGoroutine(),
		})
	})
	adminSrv := &http.Server{Addr: *adminAddr, Handler: adminMux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("运维站开始监听", "addr", *adminAddr)
		if e := adminSrv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			slog.Error("运维站监听异常退出", "err", e)
			os.Exit(1)
		}
	}()

	// 无优雅关闭需求（纯静态服务，无在途状态）：常驻直到进程被杀
	select {}
}
