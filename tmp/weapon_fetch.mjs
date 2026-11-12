// 武器库数据管线：抓 yatta.moe 全武器（rank>=3）→ 曲线校准（锚点反推乘数+互检断言）→
// 被动文本清洗与结构化 fx 解析（overrides 人工表为权威）→ 输出 genshin/site/data/weapons.json
// 锚点出处与口径见 tmp/weapon_curve_anchors.json。用法：node tmp/weapon_fetch.mjs
import { readFileSync, writeFileSync, statSync } from 'fs';

const YATTA = 'https://gi.yatta.moe/api/v2/chs/weapon';
const fetchJson = async (url, tries = 4) => {
  for (let i = 0; i < tries; i++) {
    try {
      const r = await fetch(url, { headers: { 'User-Agent': 'genshin-site-pipeline' } });
      if (!r.ok) throw new Error('HTTP ' + r.status);
      const j = await r.json();
      // yatta 业务码非 200（限流等）时响应不完整，按失败重试
      if (j.response !== undefined && j.response !== 200) throw new Error('yatta 业务码 ' + j.response);
      return j;
    } catch (e) { if (i === tries - 1) throw e; await new Promise(ok => setTimeout(ok, 1200 * (i + 1))); }
  }
};

// ===== 副词条/类型枚举映射（未知值 throw，防静默错值）=====
const SUBKEY = {
  NONE: null,
  FIGHT_PROP_ATTACK_PERCENT: 'atk%',
  FIGHT_PROP_CRITICAL: 'cr',
  FIGHT_PROP_CRITICAL_HURT: 'cd',
  FIGHT_PROP_ELEMENT_MASTERY: 'em',
  FIGHT_PROP_CHARGE_EFFICIENCY: 'er',
  FIGHT_PROP_PHYSICAL_ADD_HURT: 'phys',
  FIGHT_PROP_HP_PERCENT: 'hp%',
  FIGHT_PROP_DEFENSE_PERCENT: 'def%',
};
const TYPE_ZH = {
  WEAPON_SWORD_ONE_HAND: '单手剑', WEAPON_CLAYMORE: '双手剑', WEAPON_POLE: '长柄武器',
  WEAPON_CATALYST: '法器', WEAPON_BOW: '弓',
};

// ===== 1. 列表 + 详情全量抓取 =====
const list = await fetchJson(YATTA);
const ids = Object.entries(list.data.items).filter(([, w]) => w.rank >= 3).map(([id]) => id);
console.log('rank>=3 武器:', ids.length);
const details = {};
let done = 0, failed = 0, skipped = 0;
const queue = [...ids.filter((id) => list.data.items[id].rank >= 4)]; // 3★ 在 yatta 无 upgrade 数据，走 wiki 分支
const worker = async () => {
  while (queue.length) {
    const id = queue.shift();
    try {
      const j = await fetchJson(`${YATTA}/${id}?include=upgrade`);
      if (!j.data || !j.data.upgrade) { // 月之版本真化形态/剧情赠送武器等不可升级类，跳过
        skipped++;
        console.log('跳过特殊武器', id, list.data.items[id].name);
        continue;
      }
      details[id] = j.data;
      done++;
      if (done % 60 === 0) console.log('详情进度', done);
    } catch (e) { failed++; console.error('详情失败', id, e.message); }
  }
};
await Promise.all(Array.from({ length: 6 }, worker));
if (failed) { console.error('共', failed, '把详情抓取失败，退出'); process.exit(1); }

// ===== 2. 曲线乘数校准（锚点反推 + 互检断言）=====
const anchors = JSON.parse(readFileSync(new URL('./weapon_curve_anchors.json', import.meta.url), 'utf8'));
const calib = { base: {}, sub: {} };
const WIKI_ONLY_CURVES = new Set(['GROW_CURVE_ATTACK_102', 'GROW_CURVE_ATTACK_104']); // 纯 3★ 曲线：yatta 无 initValue，90 级值由 wiki 分支直取（ATTACK_101/CRITICAL_101 被 4★ 降临之剑共用，走乘数校准）
const calibCheck = (table, curKey, anchorKey) => {
  for (const [curve, a] of Object.entries(anchors[anchorKey])) {
    if (WIKI_ONLY_CURVES.has(curve)) continue;
    const w = details[Object.keys(details).find((id) => details[id].name === a.w)];
    if (!w) throw new Error('锚点武器不在 rank>=3 集合: ' + a.w);
    const prop = (w.upgrade.prop || []).find((p) =>
      table === 'base' ? p.propType === 'FIGHT_PROP_BASE_ATTACK' : p.propType === a.propType);
    if (!prop) throw new Error(`锚点武器 ${a.w} 无 ${a.propType} 词条`);
    const m = a.v / prop.initValue;
    const slot = calib[table][curve];
    if (slot !== undefined && Math.abs(slot.m - m) / m > 0.002) {
      throw new Error(`曲线 ${curve} 双锚乘数不一致: ${slot.m} vs ${m}`);
    }
    calib[table][curve] = { m, from: a.w };
  }
};
calibCheck('base', 'base', 'base');
calibCheck('sub', 'sub', 'sub');
// extra 锚点（狼末）作交叉验证：反推乘数与已校准乘数一致
for (const [curve, a] of Object.entries(anchors.extra || {})) {
  const w = details[Object.keys(details).find((id) => details[id].name === a.w)];
  const table = a.propType ? 'sub' : 'base';
  const prop = (w.upgrade.prop || []).find((p) => a.propType ? p.propType === a.propType : p.propType === 'FIGHT_PROP_BASE_ATTACK');
  const m = a.v / prop.initValue;
  const want = table === 'sub' ? calib.sub[curve] : calib.base[curve];
  if (want && Math.abs(want.m - m) / m > 0.002) throw new Error(`extra 锚点 ${a.w} 曲线 ${curve} 乘数不符: ${want.m} vs ${m}`);
  console.log('交叉验证通过:', a.w, curve, '乘数', m.toFixed(4));
}
for (const [t, tbl] of Object.entries(calib)) {
  for (const [curve, { m, from }] of Object.entries(tbl)) console.log(`乘数 ${t} ${curve} = ${m.toFixed(5)}（锚点 ${from}）`);
}

// ===== 3. 全量断言 + 组装 =====
const cleanText = (s) => (s || '').replace(/<color=[^>]+>/g, '').replace(/<\/color>/g, '').replace(/\\n/g, '\n').trim();
// 被动数值句式解析（初稿；人工核对覆盖表 tmp/weapon_fx_overrides.json 为权威）
const FX_PATTERNS = [
  [/攻击力(?:和防御力)?(?:提升|提高)\s*([\d.]+)%/g, 'atk%'],
  [/暴击伤害(?:提升|提高)\s*([\d.]+)%/g, 'cd'],
  [/暴击率(?:提升|提高)\s*([\d.]+)%/g, 'cr'],
  [/全伤害(?:提升|提高)\s*([\d.]+)%/g, 'bonus.all'],
  [/星扩散反应伤害(?:提升|提高)\s*([\d.]+)%/g, 'star'],
  [/星超导反应伤害(?:提升|提高)\s*([\d.]+)%/g, 'star'],
  [/普通攻击(?:和重击)?(?:造成的)?伤害(?:提升|提高)\s*([\d.]+)%/g, 'bonus.normal'],
  [/重击(?:造成的)?伤害(?:提升|提高)\s*([\d.]+)%/g, 'bonus.charge'],
  [/元素战技(?:造成的)?伤害(?:提升|提高)\s*([\d.]+)%/g, 'bonus.skill'],
  [/元素爆发(?:造成的)?伤害(?:提升|提高)\s*([\d.]+)%/g, 'bonus.burst'],
  [/元素充能效率(?:提升|提高)\s*([\d.]+)%/g, 'er'],
  [/生命值(?:提升|提高)\s*([\d.]+)%/g, 'hp%'],
  [/防御力(?:提升|提高)\s*([\d.]+)%/g, 'def%'],
  [/元素精通(?:提升|提高)\s*([\d.]+)点/g, 'em'],
];
// 轮转类（蝶变式"依次获得以下三种效果"）：按"之风/之X："分段，每段独立解析
function parseFx(text) {
  if (!text) return null;
  const clean = cleanText(text);
  let segments = [[null, clean]];
  if (/依次获得/.test(clean)) {
    // 取轮转句主体（"按照以下顺序……重置其顺序"之间的分号段）
    const body = clean.slice(clean.indexOf('依次获得'));
    const segs = body.split(/[；;]/).map((s) => s.trim()).filter(Boolean);
    segments = segs.map((s, i) => ['lun' + (i + 1), s]).filter(([, s]) => /之风：|之息：|之声：|之约：|之誓：/.test(s));
    if (!segments.length) segments = [[null, clean]];
  }
  const fx = [];
  for (const [w, seg] of segments) {
    for (const [re, k] of FX_PATTERNS) {
      re.lastIndex = 0;
      const m = re.exec(seg);
      if (m) fx.push({ k, v: +m[1], w: w || (seg.includes('时') && !/装备者的?攻击力(?:提升|提高)\s*\d/.test(seg) ? 'cond' : 'none') });
    }
  }
  return fx.length ? fx : null;
}
// 人工核对覆盖表（权威），形如 { "11522": { fx: [...] } }
let overrides = {};
try { overrides = JSON.parse(readFileSync(new URL('./weapon_fx_overrides.json', import.meta.url), 'utf8')); } catch {}

const items = {};
const fxStats = { auto: 0, override: 0, none: 0 };
const pending3star = [];
// 3★ 走 B 站 wiki MediaWiki API（结构化 wikitext：|90突破前攻击力=401 |副属性=攻击力 |90级副属性=35.2%）
// wiki 对高频程序请求限流：增量缓存 + 失败待补（重跑管线只补缺失，几轮补齐）
const WIKI_CACHE = new URL('./_wiki_3star_cache.json', import.meta.url);
let wikiCache = {};
try { wikiCache = JSON.parse(readFileSync(WIKI_CACHE, 'utf8')); } catch {}
const SUB_WIKI_KEY = { '攻击力': 'atk%', '暴击率': 'cr', '暴击伤害': 'cd', '元素充能效率': 'er', '物理伤害加成': 'phys', '生命值': 'hp%', '防御力': 'def%', '元素精通': 'em' };
async function wikiWeapon(name) {
  if (wikiCache[name]) return wikiCache[name];
  let wt = '';
  for (let i = 0; i < 4; i++) {
    try {
      await new Promise(ok => setTimeout(ok, i ? 8000 : 0)); // B站wiki限流，串行+递增间隔
      const r = await fetch('https://wiki.biligame.com/ys/api.php?action=parse&page=' + encodeURIComponent(name) + '&prop=wikitext&format=json', { headers: { 'User-Agent': 'curl/8.0.1' } });
      const j = await r.json();
      if (j.parse && j.parse.wikitext) { wt = j.parse.wikitext['*']; break; }
    } catch {}
  }
  const kv = (k) => { const m = wt.match(new RegExp('\\|' + k + '=([^\\n|]*)')); return m ? m[1].trim() : ''; };
  const base90 = parseFloat(kv('90突破前攻击力')) || null;
  const subName = kv('副属性');
  const subVal = parseFloat(kv('90级副属性')) || 0;
  const s = subName && SUB_WIKI_KEY[subName] ? [SUB_WIKI_KEY[subName], subVal] : (subName ? null : undefined);
  const fxText = (wt.match(/\|技能描述=([^|\n]*)/) || [])[1] || '';
  const result = { base90, s, fxText: fxText.trim() };
  if (result.base90) { wikiCache[name] = result; writeFileSync(WIKI_CACHE, JSON.stringify(wikiCache, null, 1)); }
  return result;
}
for (const id of ids) {
  const listItem = list.data.items[id];
  let name, rank, type, base90, s, fxText;
  if (listItem.rank >= 4) {
    const w = details[id];
    if (!w) { skipped++; continue; } // 特殊武器（真化形态/剧情赠送等，worker 已跳过并计数）
    const baseProp = (w.upgrade.prop || []).find((p) => p.propType === 'FIGHT_PROP_BASE_ATTACK');
    const subProp = (w.upgrade.prop || []).find((p) => p.propType !== 'FIGHT_PROP_BASE_ATTACK');
    if (!baseProp || baseProp.type === undefined) throw new Error(id + ' 无白值曲线');
    const mBase = calib.base[baseProp.type];
    if (!mBase) throw new Error('未知白值曲线 ' + baseProp.type + '（武器 ' + w.name + '）——需补锚点');
    base90 = Math.round(baseProp.initValue * mBase.m);
    if (subProp) {
      const mSub = calib.sub[subProp.type];
      if (!mSub) throw new Error('未知副词条曲线 ' + subProp.type + '（武器 ' + w.name + '）——需补锚点');
      const key = SUBKEY[subProp.propType];
      if (key === undefined) throw new Error('未知副词条枚举 ' + subProp.propType);
      if (key) {
        // 精通 initValue 是点数（36），百分比类是小数（0.096）——精通乘数 = 百分比乘数 / 100
        const isEm = subProp.propType === 'FIGHT_PROP_ELEMENT_MASTERY';
        const v = subProp.initValue * (isEm ? mSub.m / 100 : mSub.m);
        s = [key, key === 'em' ? Math.round(v) : Math.round(v * 10) / 10];
      }
    }
    type = w.type; // 详情接口 type 直接是中文（列表接口才是英文枚举）
    if (!Object.values(TYPE_ZH).includes(type)) throw new Error('未知武器类型 ' + type);
    const affix = w.affix && Object.values(w.affix)[0];
    fxText = cleanText(affix && affix.upgrade ? affix.upgrade['0'] : '');
    name = w.name; rank = w.rank;
  } else {
    const wk = await wikiWeapon(listItem.name); // 3★：yatta 无 upgrade 数据
    if (!wk.base90) { pending3star.push(listItem.name); continue; } // 限流未抓到：待补，重跑管线继续
    base90 = wk.base90; s = wk.s; fxText = wk.fxText;
    name = listItem.name; rank = listItem.rank;
    type = TYPE_ZH[listItem.type];
    if (!type) throw new Error('未知武器类型 ' + listItem.type);
  }
  const nameKey = name;
  let fx;
  if (overrides[nameKey]) { fx = overrides[nameKey].fx; fxStats.override++; }
  else { fx = parseFx(fxText); if (fx) fxStats.auto++; else fxStats.none++; }
  items[id] = { n: name, r: rank, t: type, a: base90, s, ...(fx ? { fx } : {}), ...(fxText ? { fxText } : {}) };
}
writeFileSync('genshin/site/data/weapons.json', JSON.stringify({
  _gen: new Date().toISOString().slice(0, 10) + ' · gi.yatta.moe/api/v2/chs/weapon · 90级数值 · fx 为结构化被动（overrides 人工核对表为权威，fxText 为清洗后原文）',
  items,
}, null, 0));
console.log('输出 weapons.json：', Object.keys(items).length, '把 |', (statSync('genshin/site/data/weapons.json').size / 1024).toFixed(1) + 'KB');
console.log('被动解析统计：自动', fxStats.auto, '/ 人工覆盖', fxStats.override, '/ 未计入', fxStats.none);
if (pending3star.length) console.log('3★ 待补抓（重跑本管线自动续补）：', pending3star.join('、'));
