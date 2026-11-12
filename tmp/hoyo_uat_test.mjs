// 用户视角完整走查：主页登录区各元素 → 留言板 → 角色详情自动填充，逐项实测
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage({ viewport: { width: 1400, height: 900 } });
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };

const PANEL = {
  time: '2026-11-11 17:16',
  chars: {
    桑多涅: { level: 90, atk: 3121.4, hp: 18005.6, def: 1043.8, cr: 70.9, cd: 186.8, em: 18.6, bonus: 0 },
    哥伦比娅: { level: 90, atk: 986.9, hp: 35483.9, def: 586.7, cr: 59.6, cd: 200.8, em: 65.3, bonus: 0 },
  },
};
await pg.addInitScript((data) => localStorage.setItem('genshin_hoyo_panels', JSON.stringify(data)), PANEL);

// 1. 主页：米游社面板折叠块的每个元素
await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
await pg.click('details.fold:has(#hoyo_send) > summary');
check('主页 手机号输入框', await pg.$('#hoyo_phone') !== null);
check('主页 发送验证码按钮', await pg.$('#hoyo_send') !== null);
check('主页 验证码输入框', await pg.$('#hoyo_code') !== null);
check('主页 登录并拉取面板按钮', await pg.$('#hoyo_login') !== null);
check('主页 重新拉取面板按钮', await pg.$('#hoyo_refresh') !== null);
const idxStatus = await pg.textContent('#hoyo_status');
check('主页 缓存列表显示角色名', idxStatus.includes('已缓存') && idxStatus.includes('桑多涅'), idxStatus.slice(0, 60));

// 2. 角色详情页（桑多涅在展柜缓存中）：自动填充 + 状态行
await pg.goto('https://genshin.1998991.xyz/detail.html?name=Sandrone', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
const g = (id) => pg.$eval('#' + id, (e) => e.value);
check('详情页 面板攻击自动填充 3121.4', (await g('c_atk')) === '3121.4', await g('c_atk'));
check('详情页 暴击率自动填充 70.9', (await g('c_cr')) === '70.9', await g('c_cr'));
check('详情页 状态行提示', (await pg.textContent('#hoyo_status')).includes('已自动填入米游社面板'), (await pg.textContent('#hoyo_status')).slice(0, 40));

// 3. 详情页 米游社面板折叠块已移除（登录在主页），不应有残留死表单
check('详情页 无残留登录表单', await pg.$('#hoyo_phone') === null);

// 4. 留言板浮窗存在且可用
await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
check('主页 留言浮窗按钮存在', await pg.$('.fb-btn') !== null);
await pg.click('.fb-btn');
check('留言面板可展开', await pg.$eval('.fb-panel', (e) => !e.hidden));

// 5. 详情页伤害计算核心回归（桑多涅面板填充后计算联动：聚能光束星超导）
await pg.goto('https://genshin.1998991.xyz/detail.html?name=Sandrone', { waitUntil: 'networkidle' });
await pg.waitForTimeout(600);
const r = await pg.evaluate(() => {
  const secs = document.querySelectorAll('#calcOut section');
  const sec = secs[2];
  if (!sec) return null;
  for (const tr of sec.querySelectorAll('tbody tr')) {
    if (tr.cells[0].textContent.includes('聚能光束星超导')) {
      const n = (s) => parseFloat(s.replace(/,/g, ''));
      return { white: n(tr.cells[1].textContent) };
    }
  }
  return null;
});
check('伤害计算随填充联动（星超导行有数值）', r && !isNaN(r.white) && r.white > 0, r ? JSON.stringify(r) : '无行');

await pg.screenshot({ path: 'F:/AskMetaFilterInCN/tmp/t_uat_final.png' });
await b.close();
console.log(`\n===== 用户视角走查：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
