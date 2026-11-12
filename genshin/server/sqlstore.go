// SQL Server 存储层：留言板数据持久化（genshin 独立库，与主站 askmetafilter 库隔离）。
// 启动时确保库与表存在，并将 legacy feedback.json 一次性迁移入库（迁移后改名留存）。
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb" // sqlserver 驱动
)

// openGenshinDB 连接 SQL Server：确保 genshin 库存在（不存在则创建）、确保 feedback 表存在，
// 然后返回连接池。库名/表名均为独立命名，不与主站 askmetafilter 库混用。
func openGenshinDB(server, port, user, pass, encrypt, dbName string) (*sql.DB, error) {
	dsnFor := func(db string) string {
		q := url.Values{}
		q.Set("database", db)
		q.Set("encrypt", encrypt)
		q.Set("app name", "genshin-server")
		return "sqlserver://" + url.UserPassword(user, pass).String() + "@" + server + ":" + port + "?" + q.Encode()
	}

	master, err := sql.Open("sqlserver", dsnFor("master"))
	if err != nil {
		return nil, fmt.Errorf("连接 master 失败: %w", err)
	}
	defer master.Close()
	if err := master.Ping(); err != nil {
		return nil, fmt.Errorf("SQL Server 连通失败: %w", err)
	}
	var n int
	if err := master.QueryRow("SELECT COUNT(*) FROM sys.databases WHERE name = @p1", dbName).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		if _, err := master.Exec("CREATE DATABASE " + dbName); err != nil {
			return nil, fmt.Errorf("创建数据库 %s 失败: %w", dbName, err)
		}
		slog.Info("已创建数据库", "database", dbName)
	}
	master.Close()

	db, err := sql.Open("sqlserver", dsnFor(dbName))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库 %s 失败: %w", dbName, err)
	}
	if err := ensureFeedbackTable(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureCharactersTable(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ensureFeedbackTable 确保 feedback 表存在（SQL Server 无 IF NOT EXISTS 建表，按存在性判断）
func ensureFeedbackTable(db *sql.DB) error {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sys.tables WHERE name = 'feedback'").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`CREATE TABLE feedback (
		id INT IDENTITY(1,1) PRIMARY KEY,
		[time] NVARCHAR(40) NOT NULL,
		[page] NVARCHAR(200) NOT NULL DEFAULT '',
		[char_name] NVARCHAR(50) NOT NULL DEFAULT '',
		content NVARCHAR(1000) NOT NULL,
		ctx NVARCHAR(MAX) NOT NULL DEFAULT '',
		done BIT NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return err
	}
	slog.Info("已创建 feedback 表")
	return nil
}

// migrateLegacyFeedback 将 legacy feedback.json 的记录一次性迁移入库（保留原 id 与全部字段），
// 迁移完成后把 JSON 文件改名为 feedback.json.imported 留存。
func migrateLegacyFeedback(db *sql.DB, legacyPath string) error {
	raw, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var recs []feedbackRecord
	if err := json.Unmarshal(raw, &recs); err != nil {
		return fmt.Errorf("解析 legacy %s: %w", legacyPath, err)
	}
	if len(recs) == 0 {
		_ = os.Rename(legacyPath, legacyPath+".imported")
		return nil
	}
	// IDENTITY_INSERT 为会话级设置：SET/INSERT/OFF 必须同事务（同连接）执行
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SET IDENTITY_INSERT feedback ON"); err != nil {
		return err
	}
	inserted := 0
	for _, r := range recs {
		ctxJSON := "{}"
		if len(r.Ctx) > 0 {
			b, _ := json.Marshal(r.Ctx)
			ctxJSON = string(b)
		}
		done := 0
		if r.Done {
			done = 1
		}
		if _, err := tx.Exec(`INSERT INTO feedback (id, [time], [page], [char_name], content, ctx, done) VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7)`,
			r.ID, r.Time, r.Page, r.Char, r.Content, ctxJSON, done); err != nil {
			return err
		}
		inserted++
	}
	if _, err := tx.Exec("SET IDENTITY_INSERT feedback OFF"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := os.Rename(legacyPath, legacyPath+".imported"); err != nil {
		return err
	}
	slog.Info("legacy 留言迁移完成", "count", inserted)
	return nil
}

// ensureCharactersTable 确保 characters 表存在（角色完整 JSON 文档存储）
func ensureCharactersTable(db *sql.DB) error {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sys.tables WHERE name = 'characters'").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`CREATE TABLE characters (
		id INT PRIMARY KEY,
		[name] NVARCHAR(50) NOT NULL UNIQUE,
		name_zh NVARCHAR(50) NOT NULL,
		version NVARCHAR(20) NOT NULL,
		data NVARCHAR(MAX) NOT NULL
	)`)
	if err != nil {
		return err
	}
	slog.Info("已创建 characters 表")
	return nil
}

// UpsertCharacter 插入或更新角色文档（先 UPDATE，未命中再 INSERT，避免 MERGE 的参数重复引用问题）
func UpsertCharacter(db *sql.DB, id int64, name, nameZh, version, data string) error {
	res, err := db.Exec(`UPDATE characters SET [name] = @p1, name_zh = @p2, version = @p3, data = @p4 WHERE id = @p5`,
		name, nameZh, version, data, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected > 0 {
		return nil
	}
	_, err = db.Exec(`INSERT INTO characters (id, [name], name_zh, version, data) VALUES (@p1, @p2, @p3, @p4, @p5)`,
		id, name, nameZh, version, data)
	return err
}

// ListCharacterDocs 返回全部角色文档原文（按 id 排序）
func ListCharacterDocs(db *sql.DB) ([]json.RawMessage, error) {
	rows, err := db.Query(`SELECT data FROM characters ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []json.RawMessage
	for rows.Next() {
		var d string // NVARCHAR(MAX) 驱动返回 string，不能直接 Scan 进 json.RawMessage
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		list = append(list, json.RawMessage(d))
	}
	return list, rows.Err()
}

// GetCharacterDoc 按英文名取角色文档原文（不存在返回空）
func GetCharacterDoc(db *sql.DB, name string) (string, error) {
	var d string
	err := db.QueryRow(`SELECT data FROM characters WHERE [name] = @p1`, name).Scan(&d)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return d, nil
}

// CharIndexFromDB 从库中读 avatarId → [中文名, 元素] 映射（Enka 展柜映射用）
func CharIndexFromDB(db *sql.DB) (map[int64][2]string, error) {
	rows, err := db.Query(`SELECT id, name_zh, JSON_VALUE(data, '$.element') FROM characters`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[int64][2]string{}
	for rows.Next() {
		var id int64
		var nameZh, elem string
		if err := rows.Scan(&id, &nameZh, &elem); err != nil {
			return nil, err
		}
		idx[id] = [2]string{nameZh, elem}
	}
	return idx, rows.Err()
}

// ImportCharactersFromDir 从抓取管线输出的目录（site/data/characters/*.json）全量导入角色文档。
// 键统一口径：文档 name 字段与文件名可能不一致（"Hu Tao" vs "HuTao"），统一规范化为去空白形式，
// 同时重写文档内 name 字段，保证列表 href → 详情 API 全链路键一致。
func ImportCharactersFromDir(db *sql.DB, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return imported, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return imported, fmt.Errorf("解析 %s: %w", e.Name(), err)
		}
		idv, _ := doc["id"].(float64)
		nameRaw, _ := doc["name"].(string)
		nameZh, _ := doc["nameZh"].(string)
		version, _ := doc["version"].(string)
		id := int64(idv)
		if id == 0 || nameRaw == "" {
			continue
		}
		urlName := strings.ReplaceAll(nameRaw, " ", "")
		doc["name"] = urlName // 统一 URL 键口径（显示名用 nameZh，不受影响）
		out, err := json.Marshal(doc)
		if err != nil {
			return imported, err
		}
		if err := UpsertCharacter(db, id, urlName, nameZh, version, string(out)); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}
