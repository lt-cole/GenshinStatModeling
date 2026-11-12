// 全站留言浮窗：右下角固定「留言」按钮 + 展开面板，POST /api/feedback 提交。
// 详情页留言自动附带角色中文名（detail.html 的 boot() 里设置 window.GS_PAGE_NAME），
// 其余页面回退取 URL name 参数。样式在 genshin.css 的 .fb-* 段。
(function () {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'fb-btn';
  btn.textContent = '留言';

  const panel = document.createElement('div');
  panel.className = 'fb-panel';
  panel.hidden = true;
  // 静态模板（不含任何用户数据，可安全 innerHTML）；用户内容一律 textContent 写入
  panel.innerHTML =
    '<div class="fb-head"><span>留言反馈</span>' +
    '<button type="button" class="fb-close" aria-label="关闭">×</button></div>' +
    '<textarea class="fb-text" maxlength="500" placeholder="数值有误？缺功能？欢迎留言（500 字内）"></textarea>' +
    '<div class="fb-foot"><span class="fb-count">0 / 500</span><span class="fb-msg"></span>' +
    '<button type="button" class="fb-send">提交</button></div>';

  document.body.append(btn, panel);

  const ta = panel.querySelector('.fb-text');
  const count = panel.querySelector('.fb-count');
  const msg = panel.querySelector('.fb-msg');
  const send = panel.querySelector('.fb-send');

  ta.addEventListener('input', () => {
    count.textContent = ta.value.length + ' / 500';
    msg.textContent = '';
  });

  const close = () => { panel.hidden = true; msg.textContent = ''; };
  btn.addEventListener('click', () => {
    panel.hidden = !panel.hidden;
    if (!panel.hidden) ta.focus();
  });
  panel.querySelector('.fb-close').addEventListener('click', close);

  // 自动附带当前页面上下文：伤害计算输入快照、米游社面板缓存状态（便于站长复现分析）
  function collectContext() {
    const ctx = { ua: navigator.userAgent };
    document.querySelectorAll('.calc-grid input, .calc-grid select').forEach((el) => {
      if (el.id) ctx['面板.' + el.id.replace(/^c_/, '')] = el.value;
    });
    try {
      const cache = JSON.parse(localStorage.getItem('genshin_hoyo_panels') || 'null');
      if (cache && cache.time) {
        let s = cache.time + '（' + Object.keys(cache.chars || {}).length + ' 角色）';
        const p = cache.chars && (window.GS_PAGE_NAME ? cache.chars[window.GS_PAGE_NAME] : null);
        if (p && p.weapon) s += ' 当前武器：' + p.weapon + (p.affix ? ' 精炼' + p.affix : ''); // 便于站长按武器被动复现实测
        else if (p && p.weaponId) s += ' 当前武器Id：' + p.weaponId + (p.affix ? ' 精炼' + p.affix : ''); // Enka 路径无名称，仅 id
        ctx['米游社面板缓存'] = s;
        if (cache.uid) ctx['面板账号UID'] = String(cache.uid); // 留言归属留言人的游戏账号（最后一次登录拉面板的会话）
      }
    } catch {}
    return ctx;
  }

  send.addEventListener('click', async () => {
    const content = ta.value.trim();
    if (!content) {
      msg.textContent = '请输入留言内容';
      return;
    }
    send.disabled = true;
    msg.textContent = '';
    try {
      const resp = await fetch('/api/feedback', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          page: location.pathname + location.search,
          char: window.GS_PAGE_NAME || new URLSearchParams(location.search).get('name') || '',
          content,
          ctx: collectContext(),
        }),
      });
      const body = await resp.json().catch(() => ({}));
      if (resp.ok) {
        ta.value = '';
        count.textContent = '0 / 500';
        msg.textContent = '已收到，感谢反馈！';
        setTimeout(close, 1200);
      } else {
        msg.textContent = body.error || '提交失败，请稍后再试';
      }
    } catch {
      msg.textContent = '网络异常，请稍后再试';
    } finally {
      send.disabled = false;
    }
  });
})();
