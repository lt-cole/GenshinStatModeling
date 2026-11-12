// 星系反应（星超导/星扩散）+ 月曜直伤公式验证：期望值从角色 JSON 实时计算，对照 gensri.wiki 口径与游戏内实测
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
import { readFileSync } from 'fs';
const ch = (n) => JSON.parse(readFileSync(`F:/GenshinStatModeling/genshin/site/data/characters/${n}.json`, 'utf8'));

const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage({ viewport: { width: 1400, height: 900 } });
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };

const set = async (id, v) => {
  const tag = await pg.$eval('#' + id, (e) => e.tagName);
  if (tag === 'SELECT') { await pg.selectOption('#' + id, v); return; }
  await pg.fill('#' + id, String(v));
  await pg.dispatchEvent('#' + id, 'input');
};
// 持久化上线后输入会跨导航保留，每个用例先重置回默认基准
const DEFAULTS = { c_scaling: 'atk', c_atk: '2000', c_hp: '30000', c_def: '900', c_cr: '50', c_cd: '100', c_bonus: '46.6', c_em: '100', c_amplify: '1', c_charlv: '90', c_enelev: '90', c_res: '10', c_records: '0', c_starbonus: '0', c_tlv: '10' };
const resetDefaults = async () => { for (const [id, v] of Object.entries(DEFAULTS)) await set(id, v); };
const row = (tIdx, kw) => pg.evaluate((args) => {
  const sec = document.querySelectorAll('#calcOut section')[args.tIdx];
  if (!sec) return null;
  for (const tr of sec.querySelectorAll('tbody tr')) {
    if (tr.cells[0].textContent.includes(args.kw)) {
      const n = (s) => parseFloat(s.replace(/,/g, ''));
      return { name: tr.cells[0].textContent, white: n(tr.cells[1].textContent), crit: n(tr.cells[2].textContent), expect: n(tr.cells[3].textContent) };
    }
  }
  return null;
}, { tIdx, kw });

const metaText = () => pg.evaluate(() => document.getElementById('calcMeta').textContent);
const resMult = (resPct) => resPct >= 0 ? 1 - resPct / 100 : 1 - resPct / 200;
const emZone = (em) => 1 + (6 * em) / (em + 2000);

// ===== 1. 米提亚：递变聚爆星超导（EM=1000、天赋Lv10、敌90抗10%、暴击50/100 —— 用户反馈场景）=====
// 2026-11-12 口径修订：星系行不含基础提升（星耀祝礼 14%），桑多涅/薇斯纳两次游戏内实测均在不乘 1.14 时闭合
const mitya = ch('Mitya');
await pg.goto('http://127.0.0.1:50055/detail.html?name=Mitya', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_em', 1000); await set('c_tlv', 10); await set('c_res', 10);
await set('c_cr', 50); await set('c_cd', 100); await set('c_records', 0);
await pg.waitForTimeout(400);
const mMult = mitya.talents.combat1.params.param10[9]; // 天赋Lv10
const mBase = 1000 * mMult;                            // EM×倍率（不含基础提升）
const mZone = emZone(1000);                            // 月曜精通乘区 = 3.0
let r = await row(0, '星超导');
const w0 = mBase * 1 * mZone * resMult(10);
check(`米提亚 记录0 白字 ≈ ${w0.toFixed(1)}（旧值 4591.9）`, r && Math.abs(r.white - w0) < 3, r ? JSON.stringify(r) : '无行');
check('米提亚 暴击=白字×2', r && Math.abs(r.crit - w0 * 2) < 4, r ? String(r.crit) : '');
check('米提亚 期望=白字×1.5', r && Math.abs(r.expect - w0 * 1.5) < 4, r ? String(r.expect) : '');
// 记录 1 次 → 1.45；12 次 → 封顶 2.00
await set('c_records', 1); await pg.waitForTimeout(300);
const w1 = mBase * 1.45 * mZone * resMult(10);
r = await row(0, '星超导');
check(`米提亚 记录1 白字 ≈ ${w1.toFixed(1)}`, r && Math.abs(r.white - w1) < 3, r ? String(r.white) : '');
await set('c_records', 12); await pg.waitForTimeout(300);
const w12 = mBase * 2.0 * mZone * resMult(10);
r = await row(0, '星超导');
check(`米提亚 记录12 白字 ≈ ${w12.toFixed(1)}（封顶2.0）`, r && Math.abs(r.white - w12) < 3, r ? String(r.white) : '');
// 公式说明跟随角色类型：米提亚只显示星系公式
let meta = await metaText();
check('米提亚 公式说明为星系口径', meta.includes('星超导/星扩散直伤') && !meta.includes('增幅区 × 防御区'), meta.slice(0, 60));

// ===== 2. 桑多涅：星超导吃系数、星扩散不吃；固有天赋「淑女的行事准则」精通加成（ATK 2000 拿满 160）自动计入 =====
const sand = ch('Sandrone');
await pg.goto('http://127.0.0.1:50055/detail.html?name=Sandrone', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_records', 0); await set('c_tlv', 10); await set('c_res', 10);
await pg.waitForTimeout(400);
const sZone = emZone(100 + 160); // 实际精通 = 面板 100 + 天赋 160（每 100 攻击力 +8，2000 拿满）
const sMultSC = sand.talents.combat3.params.param3[9];
const sMultSD = sand.talents.combat3.params.param6[9];
const sBase = 2000; // ATK 2000（不含基础提升，实测口径）
r = await row(2, '聚能光束星超导');
const s0 = sBase * sMultSC * sZone * resMult(10);
check(`桑多涅 星超导(记录0) ≈ ${s0.toFixed(1)}`, r && Math.abs(r.white - s0) < 3, r ? JSON.stringify(r) : '无行');
r = await row(2, '聚能光束星扩散');
const sx = sBase * sMultSD * sZone * resMult(10);
check(`桑多涅 星扩散(记录0) ≈ ${sx.toFixed(1)}（独立倍率 param6）`, r && Math.abs(r.white - sx) < 3, r ? JSON.stringify(r) : '无行');
await set('c_records', 12); await pg.waitForTimeout(300);
r = await row(2, '聚能光束星超导');
check(`桑多涅 星超导(记录12) ≈ ${(s0 * 2).toFixed(1)}`, r && Math.abs(r.white - s0 * 2) < 6, r ? String(r.white) : '');
r = await row(2, '聚能光束星扩散');
check(`桑多涅 星扩散(记录12)不受系数影响 ≈ ${sx.toFixed(1)}`, r && Math.abs(r.white - sx) < 3, r ? String(r.white) : '');

// ===== 2.1 实测复现：桑多涅留言板 id=28（ATK 3121.4 / 面板EM 18.6 / 爆伤186.8 / 记录1 / 天赋Lv10）
// 游戏实测无暴击 9785 —— 含天赋精通 160（面板18.6+160=178.6）闭合，残差 0.3%（面板舍入级）
await pg.goto('http://127.0.0.1:50055/detail.html?name=Sandrone', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_atk', 3121.4); await set('c_em', 18.6); await set('c_cd', 186.8);
await set('c_records', 1); await set('c_cr', 100); await set('c_tlv', 10);
await pg.waitForTimeout(400);
const sandEm = 18.6 + Math.min((3121.4 / 100) * 8, 160); // 178.6
const sandRepro = 3121.4 * 1.615 * 1.45 * emZone(sandEm) * resMult(10);
r = await row(0, '冷凝射线星超导');
check(`桑多涅 实测复现(留言#28) 白字 ≈ ${sandRepro.toFixed(0)}（游戏实测 9785，容差 0.4%）`,
  r && Math.abs(r.white - 9785) < 40, r ? JSON.stringify(r) : '无行');

// ===== 2.2 实测复现：兹白留言板 id=29（DEF 3320.47 / EM 119.6 / 爆伤212 / 敌110级 / 天赋Lv10 / C2）
// 月曜直伤：防御×倍率段(基础+太阴降0.6+二命5.5)×基础提升1.14×月乘区1.62×月曜精通乘区×抗性，一命×3.2；不吃防御乘区
await pg.goto('http://127.0.0.1:50055/detail.html?name=Zibai', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_atk', 1021.8); await set('c_def', 3320.4719); await set('c_em', 119.6);
await set('c_cr', 100); await set('c_cd', 212); await set('c_charlv', 90); await set('c_enelev', 110);
await set('c_res', 10); await set('c_tlv', 10);
await pg.evaluate(() => { window.hoyoPanelForPage = { constellation: 2 }; }); // 注入 C2（模拟米游社面板缓存）
await set('c_def', '3320.4719'); // 触发重算读取命座
await pg.waitForTimeout(400);
const zb = ch('Zibai');
const zMult = zb.talents.combat2.params.param2[9] + 0.6 + 5.5; // 8.637424
const zWhite = 3320.4719 * zMult * 1.14 * 1.62 * emZone(119.6) * resMult(10);
r = await row(1, '灵驹飞踏第二段');
check(`兹白 实测复现(留言#29, C2) 白字 ≈ ${zWhite.toFixed(0)}（游戏实测 63811）`,
  r && Math.abs(r.white - 63811) < 30, r ? JSON.stringify(r) : '无行');
check(`兹白 实测复现 暴击 ≈ ${(zWhite * 3.12).toFixed(0)}（游戏实测 199091）`,
  r && Math.abs(r.crit - 199091) < 90, r ? String(r.crit) : '');

// ===== 3. 薇斯纳：非伤害行（持续时间/冷却时间/元素能量）不再进计算表 =====
await pg.goto('http://127.0.0.1:50055/detail.html?name=Vesna', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await pg.evaluate(() => {
  const inp = document.getElementById('c_tlv');
  inp.value = '10';
  inp.dispatchEvent(new Event('input'));
});
await pg.waitForTimeout(400);
const vesnaRows = await pg.evaluate(() =>
  [...document.querySelectorAll('#calcOut tbody tr')].map((tr) => tr.cells[0].textContent));
const badRow = vesnaRows.filter((n) => /持续时间|冷却时间|元素能量|体力消耗|治疗量/.test(n));
check('薇斯纳 计算表无非伤害行', badRow.length === 0, '仍存在: ' + JSON.stringify(badRow));
check('薇斯纳 伤害行仍在（星扩散）', vesnaRows.some((n) => n.includes('翔风剑二阶灵剑星扩散伤害')), JSON.stringify(vesnaRows).slice(0, 200));
check('薇斯纳 伤害行仍在（风翎）', vesnaRows.some((n) => n.includes('风翎伤害')));

// ===== 3.4 实测复现：薇斯纳 2679攻/145精通/256.58%爆伤/36%星反应增伤，游戏实测暴击 81428 =====
// 复现条件：灵剑星扩散(爆发)Lv9、暴击率100%、敌人风抗被减到-20%（抗性乘区1.10）
await pg.goto('http://127.0.0.1:50055/detail.html?name=Vesna', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_atk', 2679); await set('c_em', 145); await set('c_cd', 256.58);
await set('c_starbonus', 36); await set('c_res', -20); await set('c_cr', 100); await set('c_tlv', 9);
await pg.waitForTimeout(400);
const vZone = 1 + (6 * 145) / (145 + 2000) + 0.36; // 1 + 精通项0.3706 + 36%星反应增伤
const vWhite = 2679 * 4.4744 * vZone * 1.1;        // ×抗性乘区(-20%→1.10)
r = await row(2, '灵剑星扩散');
check(`薇斯纳 实测复现 暴击 ≈ ${Math.round(vWhite * 3.5658).toLocaleString()}（游戏实测 81428，输入四舍五入差≈0.1%）`,
  r && Math.abs(r.crit - vWhite * 3.5658) < 3, r ? JSON.stringify(r) : '无行');

// ===== 3.5 过滤回归：甘雨瞄准射击行恢复；奈芙尔幻戏虚影按精通面板 =====
await pg.goto('http://127.0.0.1:50055/detail.html?name=Ganyu', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
const ganyuRows = await pg.evaluate(() =>
  [...document.querySelectorAll('#calcOut tbody tr')].map((tr) => tr.cells[0].textContent));
check('甘雨 瞄准射击行仍在', ganyuRows.some((n) => n.includes('瞄准射击')));

const nefer = ch('Nefer');
await pg.goto('http://127.0.0.1:50055/detail.html?name=Nefer', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await resetDefaults();
await set('c_bonus', 0); await set('c_cr', 0); await set('c_cd', 0); await set('c_res', 0);
await set('c_em', 500); await set('c_tlv', 10); await pg.waitForTimeout(400);
const nMult = nefer.talents.combat2.params.param9[9];
const nExp = 500 * nMult * 0.5; // 精通缩放直伤 = EM×倍率×防御区(90级打90级=0.5)，其余乘区全 1
r = await row(1, '幻戏虚影一段');
check(`奈芙尔 幻戏虚影(EM=500) ≈ ${nExp.toFixed(1)}`, r && Math.abs(r.white - nExp) < 1, r ? JSON.stringify(r) : '无行');
await set('c_atk', 5000); await pg.waitForTimeout(300);
r = await row(1, '幻戏虚影一段');
check('奈芙尔 幻戏虚影不随攻击力变化', r && Math.abs(r.white - nExp) < 1, r ? String(r.white) : '');
await set('c_em', 1000); await pg.waitForTimeout(300);
r = await row(1, '幻戏虚影一段');
check('奈芙尔 幻戏虚影(EM=1000)翻倍 ≈ ' + (nExp * 2).toFixed(1), r && Math.abs(r.white - nExp * 2) < 1, r ? String(r.white) : '');

// ===== 4. 回归：胡桃普通直伤（含 10% 抗性修复验证）=====
await pg.goto('http://127.0.0.1:50055/detail.html?name=HuTao', { waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
await set('c_atk', 2000); await set('c_cr', 0); await set('c_cd', 0);
await set('c_bonus', 0); await set('c_em', 0); await set('c_res', 10); await set('c_tlv', 10);
await pg.waitForTimeout(400);
const hutaoWhite = 2000 * 0.8365 * (190 / 380) * resMult(10); // defMult: (90+100)/(190+190)
r = await row(0, '一段');
check(`胡桃 白字(抗10%) ≈ ${hutaoWhite.toFixed(1)}（修复前为 ×0.999 的错误值）`, r && Math.abs(r.white - hutaoWhite) < 1, r ? JSON.stringify(r) : '无行');
meta = await metaText();
check('胡桃 公式说明为标准口径', meta.includes('公式：伤害 = 属性×倍率') && !meta.includes('星超导'), meta.slice(0, 60));

// ===== 5. 持久化：改动后刷新页面，输入与首算应自动恢复 =====
await set('c_atk', 3000); await pg.waitForTimeout(200);
await pg.reload({ waitUntil: 'networkidle' });
await pg.waitForTimeout(800);
const pv = await pg.evaluate(() => ({
  atk: document.getElementById('c_atk').value,
  res: document.getElementById('c_res').value,
  em: document.getElementById('c_em').value,
}));
check('刷新后输入自动恢复(atk=3000/res=10/em=0)', pv.atk === '3000' && pv.res === '10' && pv.em === '0', JSON.stringify(pv));
r = await row(0, '一段');
const pvWhite = 3000 * 0.8365 * (190 / 380) * resMult(10);
check(`刷新后首算用恢复值 ≈ ${pvWhite.toFixed(1)}`, r && Math.abs(r.white - pvWhite) < 1, r ? JSON.stringify(r) : '无行');

await pg.screenshot({ path: 'F:/GenshinStatModeling/tmp/t_star_calc.png' });
await b.close();
console.log(`\n===== 星系反应公式验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
if (fail) process.exit(1);
