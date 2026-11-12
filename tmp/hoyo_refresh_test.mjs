// 重新拉取面板按钮实测：服务器会话存活时无需短信直接拉取
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage({ viewport: { width: 1400, height: 800 } });
await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
await pg.click('details.fold:has(#hoyo_send) > summary');
await pg.click('#hoyo_refresh');
await pg.waitForSelector('#hoyo_status:has-text("面板已缓存")', { timeout: 8000 }).catch(() => {});
const status = await pg.textContent('#hoyo_status');
console.log('状态行:', status);
const chars = await pg.evaluate(() => {
  try {
    return Object.keys(JSON.parse(localStorage.getItem('genshin_hoyo_panels') || '{}').chars || {});
  } catch { return []; }
});
console.log('缓存角色:', chars.join('、'));
await pg.screenshot({ path: 'F:/AskMetaFilterInCN/tmp/t_refresh_btn.png', clip: { x: 0, y: 60, width: 1400, height: 260 } });
await b.close();
