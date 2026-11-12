// 米游社面板缓存自动填充验证：注入构造缓存 → 打开角色页 → 断言面板输入与计算联动
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };

const PANEL = {
  time: '2026-11-11 13:00',
  chars: {
    米提亚: { level: 90, atk: 2631, hp: 11634, def: 792, cr: 55, cd: 150, em: 120, bonus: 61.6 },
    胡桃: { level: 90, atk: 3200, hp: 32000, def: 900, cr: 60, cd: 200, em: 100, bonus: 46.6 },
  },
};

// 在任何页面脚本执行前注入缓存（模拟登录拉取后写好的 localStorage）
await pg.addInitScript((data) => localStorage.setItem('genshin_hoyo_panels', JSON.stringify(data)), PANEL);

await pg.goto('https://genshin.1998991.xyz/detail.html?name=Mitya', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
const g = (id) => pg.$eval('#' + id, (e) => e.value);
check('面板攻击力自动填充 2631', (await g('c_atk')) === '2631', await g('c_atk'));
check('面板生命自动填充 11634', (await g('c_hp')) === '11634', await g('c_hp'));
check('暴击率自动填充 55', (await g('c_cr')) === '55', await g('c_cr'));
check('暴伤自动填充 150', (await g('c_cd')) === '150', await g('c_cd'));
check('精通自动填充 120', (await g('c_em')) === '120', await g('c_em'));
check('角色等级自动填充 90', (await g('c_charlv')) === '90', await g('c_charlv'));
const status = await pg.textContent('#hoyo_status');
check('状态行显示缓存来源', status.includes('已自动填入米游社面板') && status.includes('2026-11-11 13:00'), status);

// 计算联动：递变聚爆星超导（EM=120，天赋Lv10=4.032，记录0，抗10%）
await pg.$eval('#c_tlv', (e) => { e.value = '10'; e.dispatchEvent(new Event('input')); });
await pg.waitForTimeout(300);
const mZone = 1 + (6 * 120) / (120 + 2000);
const mBase = 120 * 4.032 * (1 + 0.00028 * 120); // 基础提升未封顶（120 < 500）
const expWhite = mBase * mZone * 0.9;
const r = await pg.evaluate(() => {
  const sec = document.querySelectorAll('#calcOut section')[0];
  for (const tr of sec.querySelectorAll('tbody tr')) {
    if (tr.cells[0].textContent.includes('星超导')) {
      const n = (s) => parseFloat(s.replace(/,/g, ''));
      return { white: n(tr.cells[1].textContent), cr: n(tr.cells[2].textContent) };
    }
  }
  return null;
});
check(`星超导白字随填充联动 ≈ ${expWhite.toFixed(1)}`, r && Math.abs(r.white - expWhite) < 3, r ? JSON.stringify(r) : '无行');
check('暴击列 = 白字×2.5（暴伤150%）', r && Math.abs(r.cr - expWhite * 2.5) < 4, r ? String(r.cr) : '');

// 切到另一角色：胡桃缓存自动调用
await pg.goto('https://genshin.1998991.xyz/detail.html?name=HuTao', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
check('胡桃页自动调用缓存 面板攻击=3200', (await g('c_atk')) === '3200', await g('c_atk'));

// 主页：登录入口存在，缓存状态可见
await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
check('主页登录表单存在', await pg.$('#hoyo_phone') !== null && await pg.$('#hoyo_login') !== null);
const idxStatus = await pg.textContent('#hoyo_status');
check('主页显示已有缓存提示', idxStatus.includes('已有面板缓存') && idxStatus.includes('2026-11-11 13:00'), idxStatus);

// 清理测试缓存
await pg.evaluate(() => localStorage.removeItem('genshin_hoyo_panels'));
await b.close();
console.log(`\n===== 米游社面板自动填充验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
