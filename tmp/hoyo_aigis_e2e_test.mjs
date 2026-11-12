// 极验透传端到端验证：send 被风控要求 GT4 → stub 极验弹窗 → 模拟滑动成功 → 带 aigis 自动重试 → 短信发出
import { chromium } from 'file:///C:/Users/Administrator/AppData/Local/npm-cache/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';
const b = await chromium.launch({ executablePath: 'C:/Users/Administrator/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe' });
const pg = await b.newPage();
const errors = [];
pg.on('pageerror', (e) => errors.push('[pageerror] ' + e.message));
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { if (ok) { pass++; console.log('  ✓', name); } else { fail++; console.log('  ✗', name, detail); } };
const reqLog = [];
pg.on('request', (r) => { if (r.url().includes('/api/hoyo') || r.url().includes('geetest')) reqLog.push(r.method() + ' ' + r.url().slice(0, 90)); });
pg.on('response', (r) => { if (r.url().includes('/api/hoyo')) reqLog.push('   -> ' + r.status() + ' ' + r.url().slice(0, 60)); });

let sendCalls = 0;
let retryBody = null;
const AIGIS = {
  session_id: 'sid-e2e', mmt_type: 1,
  data: { gt: 'gt-fake-id', new_captcha: 1, risk_type: 'icon', success: 1, use_v4: true },
};

// stub 极验 GT4 脚本：记录 captchaId，暴露 onReady/onSuccess 供测试驱动
await pg.route('**/static.geetest.com/v4/gt4.js', (r) => r.fulfill({
  status: 200, contentType: 'application/javascript',
  body: `window.initGeetest4 = (cfg, cb) => {
    window.__captchaCfg = cfg;
    const obj = {
      onSuccess: (f) => { window.__fireSuccess = f; },
      onReady: (f) => { window.__fireReady = f; },
      onError: () => {},
      appendTo: (el) => { document.querySelector(el).appendChild(document.createElement('div')); },
      showCaptcha: () => { window.__showCaptchaCalled = true; },
      getValidate: () => ({ lot_number: 'ln1', captcha_output: 'co1', pass_token: 'pt1', gen_time: '1700000000' }),
    };
    cb(obj);
  };`,
}));

// 第一次 send：返回风控极验会话；带 aigis 的重试：成功
let mockCalls = 0;
await pg.route('**/api/hoyo/sms/send', async (r) => {
  mockCalls++;
  const req = r.request();
  const body = req.postData() ? JSON.parse(req.postData()) : {};
  console.log('  [mock#' + mockCalls + '] aigis=' + JSON.stringify(body.aigis || null));
  if (body.aigis) {
    retryBody = body;
    await r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
  } else {
    sendCalls++;
    await r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: false, error: '触发米哈游人机验证，请在弹窗中完成滑动验证', aigis: AIGIS }) });
  }
});
pg.on('response', async (r) => {
  if (r.url().includes('/api/hoyo/sms/send')) {
    try { console.log('  [resp-body]', JSON.stringify(await r.json()).slice(0, 150)); } catch {}
  }
});

await pg.goto('https://genshin.1998991.xyz/', { waitUntil: 'networkidle' });
await pg.click('details.fold:has(#hoyo_send) > summary'); // 展开折叠块
await pg.fill('#hoyo_phone', '13800138000');
await pg.click('#hoyo_send');

// 等待极验 stub 生效并断言初始化参数（captchaId/riskType/userInfo）→ 触发 onReady（组件就绪）
await pg.waitForFunction('window.__captchaCfg && window.__captchaCfg.captchaId && window.__fireReady', { timeout: 4000 }).catch(() => {});
const cfg = await pg.evaluate('window.__captchaCfg');
check('GT4 弹窗 captchaId 取自 aigis.data.gt', cfg && cfg.captchaId === 'gt-fake-id', JSON.stringify(cfg));
check('GT4 传入 riskType=icon', cfg && cfg.riskType === 'icon', JSON.stringify(cfg));
check('GT4 绑定 userInfo 会话', cfg && (cfg.userInfo || '').includes('sid-e2e'), JSON.stringify(cfg));
await pg.evaluate('window.__fireReady && window.__fireReady()'); // 组件 ready → 前端调 showCaptcha
await pg.waitForFunction('window.__showCaptchaCalled === true', { timeout: 2000 }).catch(() => {});
const captchaShown = await pg.evaluate('window.__showCaptchaCalled === true');
check('onReady 后 showCaptcha 已调用（验证组件展开）', captchaShown, String(captchaShown));
const status1 = await pg.textContent('#hoyo_status');
check('状态行提示完成人机验证', status1.includes('人机验证'), status1);

// 模拟用户滑动成功
await pg.evaluate('window.__fireSuccess && window.__fireSuccess()');
await pg.waitForSelector('#hoyo_status:has-text("验证码已发送")', { timeout: 4000 }).catch(() => {});
const status2 = await pg.textContent('#hoyo_status');
check('极验通过后自动重试成功', status2.includes('验证码已发送'), status2);
check('重试请求携带 aigis（session_id + 滑块结果，不含注入的 captcha_id）', retryBody && retryBody.aigis && retryBody.aigis.session_id === 'sid-e2e'
  && retryBody.aigis.data.lot_number === 'ln1' && retryBody.aigis.data.captcha_output === 'co1'
  && retryBody.aigis.data.captcha_id === undefined, JSON.stringify(retryBody));
check('首次被风控拦截仅 1 次（重试带 aigis 未再计拦截）', sendCalls === 1, String(sendCalls));

await b.close();
console.log('=== 请求日志 ===');
reqLog.forEach((l) => console.log('  ', l));
console.log(`\n===== 极验透传验收：${pass} 通过 / ${fail} 失败 =====`);
if (errors.length) { console.log('页面JS错误：', errors.join(' | ')); process.exit(1); }
process.exit(fail ? 1 : 0);
