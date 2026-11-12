// 米游社登录按钮状态机验证（路由拦截，不发真短信）：灰显/倒计时/反馈/防连点/恢复
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };

// 拦截米哈游链路接口，返回模拟响应（verify 加 800ms 延迟以可靠观察"登录中…"中间态）
await pg.route('**/api/hoyo/sms/send', (r) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }));
await pg.route('**/api/hoyo/sms/verify', async (r) => {
  await new Promise((ok) => setTimeout(ok, 800));
  await r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
});
await pg.route('**/api/hoyo/panels', (r) => r.fulfill({
  status: 200, contentType: 'application/json',
  body: JSON.stringify({ ok: true, time: '2026-11-11 14:00', chars: { 米提亚: { level: 90, atk: 2631, hp: 11634, def: 792, cr: 55, cd: 150, em: 120, bonus: 61.6 } } }),
}));

await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
await pg.click('details.fold:has(#hoyo_send) > summary'); // 展开米游社面板折叠块
const btn = (id) => pg.$eval('#' + id, (e) => ({ text: e.textContent, disabled: e.disabled }));

// 1. 空手机号点发送：不灰显，提示格式错
await pg.click('#hoyo_send');
let s = await btn('hoyo_send');
check('空手机号：按钮不灰显', !s.disabled, JSON.stringify(s));
check('空手机号：提示格式错', (await pg.textContent('#hoyo_status')).includes('格式'), await pg.textContent('#hoyo_status'));

// 2. 合法号点发送：灰显 → 倒计时灰显"已发送(n s)"
await pg.fill('#hoyo_phone', '13800138000');
await pg.click('#hoyo_send');
await pg.waitForTimeout(200);
s = await btn('hoyo_send');
check('发送后：按钮灰显（发送中或已进入倒计时）', s.disabled && (s.text === '发送中…' || /已发送（\d+s）/.test(s.text)), JSON.stringify(s));
await pg.waitForSelector('#hoyo_status:has-text("验证码已发送")', { timeout: 3000 }).catch(() => {});
check('发送成功提示', (await pg.textContent('#hoyo_status')).includes('已发送'), await pg.textContent('#hoyo_status'));
await pg.waitForTimeout(1600);
s = await btn('hoyo_send');
check('倒计时进行中：灰显 + 已发送(n s)', s.disabled && /已发送（\d+s）/.test(s.text), JSON.stringify(s));

// 3. 填验证码点登录（用 Enter 键触发）：登录中灰显（发送按钮冻结）→ 拉取面板 → 成功后双按钮恢复 + 缓存写入
await pg.fill('#hoyo_code', '123456');
await pg.press('#hoyo_code', 'Enter');
await pg.waitForTimeout(150);
s = await btn('hoyo_login');
check('登录中：登录按钮灰显 + 登录中…', s.disabled && s.text === '登录中…', JSON.stringify(s));
await pg.waitForSelector('#hoyo_status:has-text("面板已缓存")', { timeout: 4000 }).catch(() => {});
s = await btn('hoyo_login');
check('完成后登录按钮恢复', !s.disabled && s.text.includes('登录并拉取面板'), JSON.stringify(s));
s = await btn('hoyo_send');
check('完成后发送按钮恢复（倒计时继续）', s.disabled || s.text.includes('重新发送'), JSON.stringify(s));
const cache = await pg.evaluate(() => JSON.parse(localStorage.getItem('genshin_hoyo_panels') || 'null'));
check('面板缓存已写入 localStorage', cache && cache.chars && cache.chars.米提亚 && cache.chars.米提亚.atk === 2631, JSON.stringify(cache).slice(0, 120));

// 4. 详情页联动（同一缓存）
await pg.goto('https://genshin.1998991.xyz/detail.html?name=Mitya', { waitUntil: 'networkidle' });
await pg.waitForTimeout(600);
check('详情页自动填充面板攻击 2631', (await pg.$eval('#c_atk', (e) => e.value)) === '2631');

await b.close();
console.log(`\n===== 登录按钮状态机验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
