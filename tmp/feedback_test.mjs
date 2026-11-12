// 留言板端到端验证（自包含确定性版本）：
// 0. 重启 genshin-server（清内存限频表；会话从磁盘恢复）
// 1. 清理历史测试留言（只删【自动化测试】标记记录，真实留言保留）
// 2. 三次提交（第4次429）→ 落库断言
// 3. 管理页（按内容定位测试留言）标记已处理 → 筛选 → 清理
// 4. 真实留言保护断言：清理后勘误×2 与 薇斯纳详细留言 必须仍在
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const BASE = 'http://127.0.0.1:50055';
const ADMIN = 'http://127.0.0.1:50056';
const MARK = '【自动化测试】';

const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };

// 0. 重启 genshin-server：清内存限频表（会话从磁盘恢复），保证确定性
await pg.request.post('http://127.0.0.1:60068/api/jobs/genshin/restart');
await pg.waitForTimeout(4000);

// 1. 清理历史测试留言（只删标记记录）
const delResp = await pg.request.post(ADMIN + '/api/feedback/delete-marked', { data: { marker: MARK } });
await pg.request.post(ADMIN + '/api/feedback/delete-marked', { data: { marker: '测试留言' } }); // 兼容清理历史遗留的无标记测试数据
const delBody = await delResp.json().catch(() => ({}));
console.log('  前置清理:', delBody.deleted ?? 0, '条历史测试留言');

// 2. 三次提交：T1/T2/T3 成功落库，T4 被 429
async function submit(text) {
  await pg.goto(BASE + '/', { waitUntil: 'networkidle' });
  await pg.click('.fb-btn');
  await pg.fill('.fb-text', text);
  await pg.click('.fb-send');
  await pg.waitForSelector('.fb-panel .fb-msg:has-text("已收到"), .fb-panel .fb-msg:has-text("频繁")', { timeout: 3000 }).catch(() => {});
  return pg.textContent('.fb-msg');
}
let msg = await submit(MARK + '留言一：首页浮窗提交');
check('T1 提交成功提示', msg.includes('已收到'), msg);
msg = await submit(MARK + '留言二：米提亚页伤害计算');
check('T2 提交成功提示', msg.includes('已收到'), msg);
msg = await submit(MARK + '留言三：占满限频名额');
check('T3 提交成功提示', msg.includes('已收到'), msg);
await pg.waitForTimeout(1400); // 等成功后的自动收起定时器（1.2s）结束
await pg.evaluate(() => { document.querySelector('.fb-panel').hidden = false; }); // 确定性展开
msg = await submit(MARK + '留言四：应当被限频');
check('T4 限频 429 提示', msg.includes('频繁'), msg);

// 落库断言：3 条测试留言 + 字段正确
let list = await (await pg.request.get(ADMIN + '/api/feedback')).json();
check('落库：共 6 条（3 真实 + 3 新测试）', list.length === 6, String(list.length));
check('落库：T1 字段正确', list.some((r) => r.content === MARK + '留言一：首页浮窗提交' && r.page === '/' && !r.char), '');
check('落库：T2 附带米提亚', list.some((r) => r.char === '米提亚'), '');
check('落库：新标记 3 条都在', list.filter((r) => (r.content || '').includes(MARK)).length === 3, '');

// 3. 管理页：按内容定位测试留言标记已处理 → 筛选
await pg.goto(ADMIN + '/', { waitUntil: 'networkidle' });
await pg.waitForSelector('#rows tr .am-content', { timeout: 3000 }).catch(() => {});
// 找到 T3 测试留言所在行，点击其标记按钮
const t3Row = pg.locator('#rows tr', { hasText: '留言三' }).first();
await t3Row.locator('button').first().click();
await pg.waitForSelector('#rows tr.am-done:has-text("留言三")', { timeout: 3000 }).catch(() => {});
const doneRow = await pg.locator('#rows tr.am-done:has-text("留言三")').count();
check('按内容定位标记已处理生效', doneRow === 1, String(doneRow));
await pg.click('button[data-f="done"]');
const doneCount = await pg.locator('#rows tr').count();
check('筛选已处理只出 1 条', doneCount === 1, String(doneCount));
await pg.click('button[data-f="open"]');
const openCount = await pg.locator('#rows tr').count();
check('筛选未处理出 2 条', openCount === 2, String(openCount));

// 4. 真实留言保护断言：清理测试留言后，3 条真实留言必须保留
await pg.request.post(ADMIN + '/api/feedback/delete-marked', { data: { marker: MARK } });
list = await (await pg.request.get(ADMIN + '/api/feedback')).json();
const realKept = list.filter((r) => r.content.includes('勘误') || r.content.includes('打桩') || r.content.includes('86级'));
check('清理后真实留言保留 3 条', list.length === 3 && realKept.length === 3, JSON.stringify(list.map((r) => r.content.slice(0, 15))));

// 5. 公网不可达回归
await pg.goto(BASE + '/', { waitUntil: 'networkidle' });
const pubList = await pg.request.get(BASE + '/api/feedback');
check('公网 GET /api/feedback 404', pubList.status() === 404, String(pubList.status()));

await b.close();
console.log(`\n===== 留言板验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
