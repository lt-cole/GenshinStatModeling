// 武器收益对比图验收：守卫链引导 / 出图与排序 / round-trip（当前武器 bar == 右列代表行暴击）/
// 副词条重组（攻击%↔暴击%互换）/ 米游社形态武器名解析 / 降级三态（level≠90 / weaponId 不存在 / json 404）/
// 被动注入（狼末常驻 atk%）/ 回归
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
import { readFileSync } from 'fs';

const BASE = 'http://127.0.0.1:50055';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage({ viewport: { width: 1400, height: 900 } });
const errors = [];
pg.on('pageerror', (e) => errors.push(e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };
const weapons = JSON.parse(readFileSync('F:/GenshinStatModeling/genshin/site/data/weapons.json', 'utf8'));
const vis = (id) => pg.$eval('#' + id, (e) => !e.hidden);
const hint = () => pg.evaluate(() => ({ h: !document.getElementById('weaponHint').hidden, t: document.getElementById('weaponHint').textContent }));
const bars = () => pg.evaluate(() => window.GS_WEAPON_BARS || []);

// ===== 1. 无缓存：引导提示、画布隐藏 =====
await pg.goto(BASE + '/detail.html?name=HuTao', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
let h = await hint();
check('无缓存显示引导提示', h.h && h.t.includes('登录'), h.t);

// ===== 2. 胡桃 + 长柄 5★（Enka 形态 weaponId）：出图、排序、round-trip =====
const pole5 = Object.entries(weapons.items).find(([id, x]) => x.t === '长柄武器' && x.r === 5);
await pg.evaluate(([wid]) => {
  localStorage.setItem('genshin_hoyo_panels', JSON.stringify({
    time: '2026-11-12 16:00', uid: '100000001', chars: {
      胡桃: { level: 90, constellation: 0, atk: '2901.7', hp: '33000.0', def: '876.0', cr: '63.9', cd: '200.0', em: '100.0', bonus: '46.6', weaponId: wid, affix: 1 },
    },
  }));
}, [pole5[0]]);
await pg.reload({ waitUntil: 'networkidle' }); await pg.waitForTimeout(900);
check('有缓存出图', await pg.evaluate(() => (window.GS_WEAPON_BARS || []).length) > 10);
let st = await pg.evaluate(() => ({ bars: window.GS_WEAPON_BARS, note: document.getElementById('weaponNote').textContent }));
check('bars 按暴击降序', st.bars.every((b, i) => i === 0 || st.bars[i - 1].crit >= b.crit));
check('bars 数量 = 同类型 3-5★ 总数', st.bars.length === Object.values(weapons.items).filter((x) => x.t === '长柄武器' && x.r >= 3).length, String(st.bars.length));
const curBar = st.bars.find((b) => b.isCur);
check('当前武器标记', curBar && curBar.isCur, JSON.stringify(curBar));
// 把右列输入填成缓存面板 → 同面板下 round-trip：当前武器 bar == 右列代表行暴击
const set = async (id, v) => { await pg.$eval('#' + id, (e, val) => { e.value = val; e.dispatchEvent(new Event('input', { bubbles: true })); }, String(v)); };
for (const [id, v] of [['c_atk', 2901.7], ['c_hp', 33000], ['c_def', 876], ['c_cr', 63.9], ['c_cd', 200], ['c_em', 100]]) await set(id, v);
await pg.waitForTimeout(300);
const repName = (st.note.match(/代表行：([^（]+)/) || [])[1];
const domRow = await pg.evaluate((rn) => {
  for (const tr of document.querySelectorAll('#calcOut tbody tr'))
    if (tr.cells[0].textContent.includes(rn)) return parseFloat(tr.cells[2].textContent.replace(/,/g, ''));
  return null;
}, repName);
check(`round-trip：同面板下当前武器 bar == 右列代表行暴击（${repName.slice(0, 12)}）`, domRow && Math.abs(domRow - curBar.crit) <= 1, `dom=${domRow} bar=${curBar.crit}`);

// ===== 3. 副词条重组专项：换攻击%武器时攻击力按新白值与新攻击%重算（手工复算对比）=====
// 挑一把同类型攻击%副词条武器，手工复算其面板与代表行暴击，与 bar 差 <1
const atkSub = st.bars.length && Object.entries(weapons.items).find(([id, x]) => x.t === '长柄武器' && x.r >= 3 && x.s && x.s[0] === 'atk%');
if (atkSub) {
  const [wid2, x2] = atkSub;
  const hu = JSON.parse(readFileSync('F:/GenshinStatModeling/genshin/site/data/characters/HuTao.json', 'utf8'));
  const stat = hu.stats.at(-1); // 90 级白字：atk 106.4；突破=暴击伤害 88.4%（ascCd）
  const curW = weapons.items[pole5[0]];
  const fxSum = (x, k) => (x.fx || []).reduce((a, f) => a + (f.k === k ? f.v : 0), 0);
  // 手工复算（公式口径与 detail.html evalRow 一致）：
  // 反推 C：C.x = 面板.x − (基础 × (1+突破%+武器%+被动fx)) 或暴击/爆伤/精通的线性合成
  const C_atk = 2901.7 - (stat.atk + curW.a) * (1 + fxSum(curW, 'atk%') / 100); // 胡桃突破非攻击%，护摩无 atk% 副词条与 fx
  const curCdSub = curW.s && curW.s[0] === 'cd' ? curW.s[1] : 0;
  const C_cd = 200 - 50 - 88.4 - curCdSub - fxSum(curW, 'cd');
  const newAtkPct = (x2.s && x2.s[0] === 'atk%' ? x2.s[1] / 100 : 0) + fxSum(x2, 'atk%') / 100;
  const panelAtk = (stat.atk + x2.a) * (1 + newAtkPct) + C_atk;
  const cd2 = Math.max(0, 50 + 88.4 + (x2.s && x2.s[0] === 'cd' ? x2.s[1] : 0) + fxSum(x2, 'cd') + C_cd);
  const mult = hu.talents.combat3.params.param2[9]; // 低血量时技能伤害 tlv10
  const defMult = (90 + 100) / ((90 + 100) + (90 + 100)); // 敌 90 级（页面默认输入）
  const manual = panelAtk * mult * (1 + 46.6 / 100) * defMult * 0.9 * (1 + cd2 / 100);
  const bar2 = await pg.evaluate(() => window.GS_WEAPON_BARS);
  const b2 = bar2.find((b) => b.id === wid2);
  check(`换装复算：${x2.n} bar ≈ 手工复算 ${Math.round(manual)}`, b2 && Math.abs(b2.crit - manual) < Math.max(1, manual * 0.002), `bar=${b2 && b2.crit} manual=${Math.round(manual)}`);
} else console.log('  -（无攻击%副词条长柄武器，跳过换装复算）');

// ===== 4. 米游社形态（weapon 中文名解析）：米提亚+法器 =====
const cat5 = Object.entries(weapons.items).find(([id, x]) => x.t === '法器' && x.r === 5);
await pg.goto(BASE + '/detail.html?name=Mitya', { waitUntil: 'networkidle' }); await pg.waitForTimeout(600);
await pg.evaluate(([wname]) => {
  localStorage.setItem('genshin_hoyo_panels', JSON.stringify({
    time: '2026-11-12 16:00', uid: '100000001', chars: {
      米提亚: { level: 90, constellation: 0, atk: '2631.0', hp: '22000.0', def: '800.0', cr: '55.0', cd: '180.0', em: '500.0', bonus: '0.0', weapon: wname, affix: 1 },
    },
  }));
}, [cat5[1].n]);
await pg.reload({ waitUntil: 'networkidle' }); await pg.waitForTimeout(900);
check('米游社形态（武器名）出图', await pg.evaluate(() => (window.GS_WEAPON_BARS || []).length) > 5);

// ===== 5. 降级三态 =====
// 5a. level≠90
await pg.evaluate(() => {
  const c = JSON.parse(localStorage.getItem('genshin_hoyo_panels'));
  Object.values(c.chars).forEach((x) => { x.level = 85; });
  localStorage.setItem('genshin_hoyo_panels', JSON.stringify(c));
});
await pg.reload({ waitUntil: 'networkidle' }); await pg.waitForTimeout(700);
h = await hint();
check('level≠90 提示', h.h && h.t.includes('等级非 90'), h.t);
// 5b. weaponId 不存在
await pg.evaluate(() => {
  const c = JSON.parse(localStorage.getItem('genshin_hoyo_panels'));
  Object.values(c.chars).forEach((x) => { x.level = 90; x.weaponId = 999999999; x.weapon = undefined; });
  localStorage.setItem('genshin_hoyo_panels', JSON.stringify(c));
});
await pg.reload({ waitUntil: 'networkidle' }); await pg.waitForTimeout(700);
h = await hint();
check('weaponId 不存在提示', h.h && h.t.includes('无法解析'), h.t);
// 5c. weapons.json 404
await pg.route('**/data/weapons.json', (route) => route.abort());
await pg.reload({ waitUntil: 'networkidle' }); await pg.waitForTimeout(700);
h = await hint();
check('weapons.json 加载失败提示', h.h && h.t.includes('加载失败'), h.t);
await pg.unroute('**/data/weapons.json');

await pg.screenshot({ path: 'F:/GenshinStatModeling/tmp/t_weapon_compare.png' });
await b.close();
console.log(`\n===== 武器收益图验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
