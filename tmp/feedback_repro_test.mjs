// 留言复现：按两条勘误留言的 ctx 输入在站点上复现计算，量化与游戏实测的偏差
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const inputs = ['c_scaling','c_atk','c_hp','c_def','c_cr','c_cd','c_bonus','c_em','c_amplify','c_charlv','c_enelev','c_res','c_records','c_starbonus','c_tlv'];
async function setInputs(ctx) {
  for (const [k, v] of Object.entries(ctx)) {
    const id = 'c_' + k;
    const el = await pg.$('#' + id);
    if (!el) continue;
    await pg.$eval('#' + id, (e, val) => { e.value = val; e.dispatchEvent(new Event('input')); }, String(v));
  }
  await pg.waitForTimeout(400);
}
async function row(tIdx, kw) {
  return pg.evaluate((args) => {
    const sec = document.querySelectorAll('#calcOut section')[args.tIdx];
    if (!sec) return null;
    for (const tr of sec.querySelectorAll('tbody tr')) {
      if (tr.cells[0].textContent.includes(args.kw)) {
        const n = (s) => parseFloat(s.replace(/,/g, ''));
        return { white: n(tr.cells[1].textContent), crit: n(tr.cells[2].textContent), label: tr.cells[0].textContent };
      }
    }
    return null;
  }, { tIdx, kw });
}

// ===== 留言1：桑多涅 重击冷凝射线星超导（游戏：没暴击 9785 / 暴击 28062）=====
await pg.goto('https://genshin.1998991.xyz/detail.html?name=Sandrone', { waitUntil: 'networkidle' });
await setInputs({ scaling: 'atk', atk: '3121.4', hp: '18005.583984375', def: '1043.7716064453125', cr: '70.9', cd: '186.8', bonus: '0', em: '18.6', amplify: '1', charlv: '90', enelev: '110', res: '10', records: '1', starbonus: '0', tlv: '10' });
let r = await row(0, '重击冷凝射线星超导');
console.log('[桑多涅] 站点白字:', r && r.white, '| 暴击:', r && r.crit);
console.log('[桑多涅] 游戏 白字: 9785 | 暴击: 28062');
console.log('[桑多涅] 白字偏差:', r ? ((r.white - 9785) / 9785 * 100).toFixed(2) + '%' : '?');
console.log('[桑多涅] 暴伤比值检验: 游戏暴/白 =', (28062 / 9785).toFixed(4), '(面板暴伤186.8% → 理论比值 2.868)');

// ===== 留言2：兹白 灵驹飞踏第二段（游戏：暴击 199091 / 没暴击 63811，二命）=====
await pg.goto('https://genshin.1998991.xyz/detail.html?name=Zibai', { waitUntil: 'networkidle' });
await setInputs({ scaling: 'atk', atk: '1021.8', hp: '18237.1875', def: '3320.471923828125', cr: '59.4', cd: '212.0', bonus: '0', em: '119.6', amplify: '1', charlv: '90', enelev: '110', res: '10', records: '1', starbonus: '0', tlv: '10' });
r = await row(1, '灵驹飞踏第二段');
console.log('[兹白] 站点白字(防御力倍率):', r && r.white, '| 暴击:', r && r.crit);
console.log('[兹白] 游戏 白字: 63811 | 暴击: 199091 | 暴/白比:', (199091 / 63811).toFixed(4), '(面板暴伤212% → 理论比值 3.1206)');

// C2 规则验证：注入兹白2命缓存 → 灵驹飞踏第二段应含 +550%防御 加成段
await pg.evaluate(() => {
  const cache = JSON.parse(localStorage.getItem('genshin_hoyo_panels') || '{"chars":{}}');
  cache.chars = cache.chars || {};
  cache.chars['兹白'] = { level: 90, constellation: 2 };
  localStorage.setItem('genshin_hoyo_panels', JSON.stringify(cache));
});
await pg.reload({ waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await setInputs({ scaling: 'atk', atk: '1021.8', hp: '18237.1875', def: '3320.471923828125', cr: '59.4', cd: '212.0', bonus: '0', em: '119.6', amplify: '1', charlv: '90', enelev: '110', res: '10', records: '1', starbonus: '0', tlv: '10' });
await pg.fill('#c_fulight', '332');
r = await row(1, '灵驹飞踏第二段');
const fullTheory = 3320.47 * 2.5374 * 0.6 * 0 + 0;
const m = 2.5374 + 0.6 + 5.5;
const fullWhite = 3320.47 * m * (1 + 0.016 * (332 - 70)) * (190 / 400) * 0.9;
console.log('[兹白] 完整模型(F=332浮光) 白字(理论 ≈' + fullWhite.toFixed(1) + '):', r && r.white);
const critTheory = fullWhite * 3.1206;
console.log('[兹白] 暴击理论 ≈', critTheory.toFixed(0), '| 游戏暴击 199091');

await b.close();
