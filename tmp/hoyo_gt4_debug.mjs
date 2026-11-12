// 复现调试：真实 gt4.js + 米哈游真实极验参数（仅 mock send 响应，不发真短信）
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const logs = [];
pg.on('console', (m) => logs.push(`[${m.type()}] ${m.text()}`));
pg.on('pageerror', (e) => logs.push('[pageerror] ' + e.message));
pg.on('console', (m) => { if (m.type() === 'error' || m.text().includes('Mixed Content')) logs.push('[console.error] ' + m.text().slice(0, 220)); });
pg.on('requestfailed', (r) => { if (r.url().includes('geetest')) logs.push('[reqfail] ' + r.url() + ' :: ' + (r.failure()?.errorText || '')); });
const net = [];
pg.on('response', (r) => { if (r.url().includes('geetest')) net.push(`${r.status()} ${r.url().slice(0, 90)}`); });

// 只 mock send 响应为米哈游真实下发的极验会话（从日志复刻），其余真实执行
await pg.route('**/api/hoyo/sms/send', (r) => r.fulfill({
  status: 502, contentType: 'application/json',
  body: JSON.stringify({
    error: '触发米哈游人机验证，请在弹窗中完成滑动验证',
    aigis: { session_id: 'sid-debug', mmt_type: 1, data: { gt: 'fa08f72997a01b95bde5facadcf284c4', new_captcha: 1, risk_type: 'icon', success: 1, use_v4: true } },
  }),
}));

await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
await pg.click('details.fold:has(#hoyo_send) > summary');
await pg.fill('#hoyo_phone', '13800138000');
await pg.click('#hoyo_send');
await pg.waitForTimeout(9000);
console.log('=== 页面 URL ===', pg.url());

console.log('=== 状态行 ===');
console.log(await pg.textContent('#hoyo_status'));
console.log('=== typeof initGeetest4 ===', await pg.evaluate(() => typeof window.initGeetest4));
console.log('=== typeof initGeetest ===', await pg.evaluate(() => typeof window.initGeetest));
console.log('=== #hoyo_captcha DOM 长度 ===', await pg.$eval('#hoyo_captcha', (e) => e.innerHTML.length).catch((e) => 'ERR ' + e.message));
console.log('=== geetest 网络 ===');
net.forEach((n) => console.log('  ', n));
console.log('=== 控制台/页面错误 ===');
logs.slice(0, 25).forEach((l) => console.log('  ', l.slice(0, 200)));
await pg.screenshot({ path: 'F:/AskMetaFilterInCN/tmp/t_gt4_debug.png' });
await b.close();
