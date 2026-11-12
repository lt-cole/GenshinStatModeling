# GenshinStatModeling

试图给原神的角色强度打分——原神角色数值建模站：122 个角色的属性成长 / 技能倍率 / 伤害计算 / 米游社账号面板拉取。

公网入口 `genshin.1998991.xyz`（Cloudflare 回源本机 50055，运维后台仅本机 50056）。

## 结构

- `genshin/server`：Go 服务（标准库 + go-mssqldb），双监听——使用站 `:50055`、运维站 `127.0.0.1:50056`
  - `main.go` 入口与静态资源缓存策略 / `sqlstore.go` SQL Server 存储层 / `characters.go` 角色数据 API / `feedback.go` 留言板 / `hoyo.go` 米游社短信登录 + 面板拉取（Enka 降级）
- `genshin/site`：纯静态前端（`index.html` 角色列表、`detail.html` 角色数值与伤害计算、管理后台由运维站托管）
- 数据流：抓取管线输出 JSON → 启动时导入 SQL Server `genshin` 库（独立库，与镜像站零混用）→ API 提供服务
- 进程守护：watchd（`F:\AskMetaFilterInCN\watchd.json` 的 genshin 任务指向本目录）

## 本地构建与验收

```bash
cd genshin/server && go vet ./... && go test ./... && go build -o ../bin/genshin-server.exe .
```

验收套件（`tmp/` 下，打公网域名真实拓扑）：`feedback_test.mjs` 留言板 / `hoyo_btn_test.mjs` 登录按钮状态机 / `hoyo_uat_test.mjs` 用户视角走查 / `calc_star_verify.mjs` 伤害公式回归 / `tip_lock_test.mjs` 浮窗锁定 / `weapon_compare_test.mjs` 武器收益图。武器库更新：`node tmp/weapon_fetch.mjs`（yatta.moe 抓取 → 曲线锚点校准 → `site/data/weapons.json`，被动 fx 见 `weapon_fx_overrides.json` 人工核对表）。

## 公式口径

权威来源：强度研究院 gensri.wiki（乘区体系）+ UIGF-org/mihoyo-api-collect（登录接口）。机制不确定时以游戏内实测反推为准，实测数据经留言板回传（自动附带面板与武器上下文）。兹白月结晶、星系反应等已实测闭合的规则分支见 `detail.html` 内注释。
