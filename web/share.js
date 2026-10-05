/* ============================================================
   NetDisk 分享查看页
   通过 /s/:token JSON 接口渲染文件 / 文件夹
   ============================================================ */
'use strict';

const $ = (sel, root = document) => root.querySelector(sel);

const esc = (s) =>
  String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

const icon = (name, cls = '') => `<svg class="icon ${cls}"><use href="#i-${name}"></use></svg>`;

function fmtSize(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n, i = -1;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  if (v >= 100) {
    const r = Math.round(v);
    if (r >= 1024 && i < units.length - 1) {
      return ((v / 1024).toFixed(1)) + ' ' + units[i + 1];
    }
    return r + ' ' + units[i];
  }
  return v.toFixed(1) + ' ' + units[i];
}

function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const p = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (_) {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      return ok;
    } catch (_) {
      return false;
    }
  }
}

const EXT_GROUPS = [
  ['image', ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif']],
  ['video', ['mp4', 'mkv', 'avi', 'mov', 'webm', 'flv', 'wmv', 'm4v']],
  ['audio', ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma']],
  ['archive', ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz']],
  ['code', ['js', 'ts', 'jsx', 'tsx', 'py', 'go', 'java', 'c', 'cpp', 'h', 'hpp', 'cs', 'rs', 'rb', 'php', 'html', 'css', 'scss', 'json', 'xml', 'sh', 'yaml', 'yml', 'sql']],
  ['sheet', ['xls', 'xlsx', 'csv']],
  ['ppt', ['ppt', 'pptx']],
];

function extOf(name) {
  const dot = (name || '').lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

function kindOf(name, isDir) {
  if (isDir) return { icon: 'folder', cls: 'ico-folder' };
  const ext = extOf(name);
  for (const [group, list] of EXT_GROUPS) {
    if (list.includes(ext)) return { icon: group, cls: 'ico-' + group };
  }
  return { icon: 'file', cls: 'ico-file' };
}

function previewKind(name) {
  const ext = extOf(name);
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico'].includes(ext)) return 'image';
  if (['mp4', 'webm', 'mov', 'm4v'].includes(ext)) return 'video';
  if (['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a'].includes(ext)) return 'audio';
  return null;
}

/* ---------- 页面状态 ---------- */
const params = new URLSearchParams(location.search);
const token = params.get('token') || '';
let pathSegs = (params.get('path') || '').split('/').filter(Boolean);
let rootName = '';
let seq = 0;

const main = $('#share-main');
const setHTML = (html) => { main.innerHTML = html; };

function expiryText(iso) {
  return iso ? fmtTime(iso) + ' 到期' : '永久有效';
}

function apiURL() {
  const suffix = pathSegs.length ? '/' + pathSegs.map(encodeURIComponent).join('/') : '';
  return '/s/' + encodeURIComponent(token) + suffix;
}

function downloadURL() {
  const suffix = pathSegs.length ? '/' + pathSegs.map(encodeURIComponent).join('/') : '';
  return '/s/' + encodeURIComponent(token) + suffix + '?download=1';
}

function updateURL(push) {
  const q = new URLSearchParams();
  q.set('token', token);
  if (pathSegs.length) q.set('path', pathSegs.join('/'));
  const url = location.pathname + '?' + q.toString();
  if (push) history.pushState(null, '', url);
  else history.replaceState(null, '', url);
}

function renderError(title, message) {
  setHTML(`
    <div class="share-card">
      <div class="empty">
        <span class="empty-ico" style="background:var(--danger-weak);color:var(--danger)">${icon('alert', 'xl')}</span>
        <h3>${esc(title)}</h3>
        <p>${esc(message)}</p>
        <a class="btn" style="margin-top:14px" href="/">${icon('cloud')}回到首页</a>
      </div>
    </div>`);
}

async function load() {
  if (!token) {
    renderError('链接不完整', '缺少分享标识，请使用完整的分享链接打开。');
    return;
  }
  const mySeq = ++seq;
  setHTML(`<div class="share-card"><div class="loading-row"><span class="spinner"></span>加载中…</div></div>`);

  let res;
  try {
    res = await fetch(apiURL(), {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
    });
  } catch (_) {
    if (mySeq === seq) renderError('网络错误', '无法连接到服务器，请稍后再试。');
    return;
  }
  if (mySeq !== seq) return;

  if (res.status === 404) {
    let msg = '分享不存在或已失效。';
    try {
      const d = await res.json();
      if (d && d.error) msg = d.error;
    } catch (_) {}
    renderError('无法访问', msg);
    return;
  }
  if (!res.ok) {
    renderError('加载失败', '服务返回了 HTTP ' + res.status + '，请稍后再试。');
    return;
  }

  const data = await res.json();
  if (mySeq !== seq) return;
  if (!pathSegs.length) rootName = data.name || '';
  document.title = (data.name || '文件分享') + ' · 云盘 NetDisk';
  if (data.type === 'dir') renderDir(data);
  else renderFile(data);
}

/* ---------- 渲染：单个文件 ---------- */
function renderFile(data) {
  const kind = kindOf(data.name, false);
  const pk = previewKind(data.name);
  const dl = data.download_url;
  const canPreview = pk && Number(data.size) <= 80 * 1024 * 1024;

  let preview = '';
  if (canPreview) {
    if (pk === 'image') preview = `<img src="${esc(dl)}" alt="${esc(data.name)}" loading="lazy">`;
    else if (pk === 'video') preview = `<video controls preload="metadata" src="${esc(dl)}"></video>`;
    else preview = `<audio controls preload="metadata" src="${esc(dl)}"></audio>`;
  }

  setHTML(`
    <div class="share-card">
      <div class="share-file-head">
        <span class="file-ico ${kind.cls}">${icon(kind.icon)}</span>
        <h1>${esc(data.name)}</h1>
        <div class="share-sub">${esc(fmtSize(data.size))} · ${esc(expiryText(data.expires_at))}</div>
      </div>
      ${preview ? `<div class="share-preview">${preview}</div>` : ''}
      <div class="share-actions">
        <button class="btn btn-primary btn-lg" id="dl-btn">${icon('download')}下载文件</button>
        <button class="btn btn-lg" id="copy-btn">${icon('copy')}复制分享链接</button>
      </div>
    </div>`);

  $('#dl-btn').addEventListener('click', () => { location.href = dl; });
  $('#copy-btn').addEventListener('click', async () => {
    if (await copyText(location.href)) toastMini('链接已复制');
    else toastMini('复制失败，请手动复制');
  });
}

/* ---------- 渲染：文件夹 ---------- */
function crumbsHTML() {
  const items = [{ name: rootName || '分享内容', depth: 0 }];
  pathSegs.forEach((s, i) => items.push({ name: s, depth: i + 1 }));
  return items.map((c, i) => {
    const current = i === items.length - 1;
    return current
      ? `<span class="crumb current">${esc(c.name)}</span>`
      : `<a class="crumb" href="#" data-depth="${c.depth}">${esc(c.name)}</a>`;
  }).join(icon('chevron-right', 'crumb-sep'));
}

function renderDir(data) {
  const items = data.items || [];
  const rows = items.map((it) => {
    const isDir = it.type === 'dir';
    const kind = kindOf(it.name, isDir);
    const action = isDir
      ? ''
      : `<a class="icon-btn" href="${esc(it.download_url)}" title="下载" aria-label="下载">${icon('download')}</a>`;
    return `
      <div class="file-row" data-name="${esc(it.name)}" data-type="${it.type}" data-url="${esc(it.download_url)}">
        <div class="file-main">
          <span class="file-ico ${kind.cls}">${icon(kind.icon)}</span>
          <div class="file-text">
            <span class="file-name">${esc(it.name)}</span>
            ${isDir ? '<span class="file-sub">文件夹</span>' : ''}
          </div>
        </div>
        <div class="file-size">${isDir ? '—' : esc(fmtSize(it.size))}</div>
        <div class="row-actions">${action}</div>
      </div>`;
  }).join('');

  setHTML(`
    <div class="share-card">
      <div class="share-dir-head">
        <span class="file-ico ico-folder">${icon('folder')}</span>
        <div class="share-dir-info">
          <h1>${esc(data.name)}</h1>
          <div class="share-sub">${items.length} 项 · ${esc(expiryText(data.expires_at))}</div>
        </div>
        <button class="btn btn-primary" id="dl-all">${icon('download')}打包下载</button>
      </div>
      <div class="share-crumbs">${crumbsHTML()}</div>
      ${items.length
        ? rows + `<div class="panel-foot">共 ${items.length} 项</div>`
        : `<div class="empty"><span class="empty-ico">${icon('box', 'xl')}</span><h3>空文件夹</h3><p>这个文件夹里还没有内容</p></div>`}
    </div>`);

  $('#dl-all').addEventListener('click', () => { location.href = downloadURL(); });
}

/* ---------- 迷你提示 ---------- */
function toastMini(message) {
  const el = document.createElement('div');
  el.className = 'toast info';
  el.innerHTML = `${icon('check')}<span></span>`;
  el.lastElementChild.textContent = message;
  el.style.position = 'fixed';
  el.style.top = '20px';
  el.style.right = '20px';
  el.style.zIndex = '99';
  document.body.appendChild(el);
  setTimeout(() => el.remove(), 2600);
}

/* ---------- 事件绑定 ---------- */
main.addEventListener('click', (e) => {
  const crumb = e.target.closest('.crumb[data-depth]');
  if (crumb) {
    e.preventDefault();
    pathSegs = pathSegs.slice(0, Number(crumb.dataset.depth));
    updateURL(true);
    load();
    return;
  }
  if (e.target.closest('a, button')) return; // 让下载按钮正常工作
  const row = e.target.closest('.file-row');
  if (!row) return;
  if (row.dataset.type === 'dir') {
    pathSegs = pathSegs.concat([row.dataset.name]);
    updateURL(true);
    load();
  } else {
    location.href = row.dataset.url;
  }
});

window.addEventListener('popstate', () => {
  const p = new URLSearchParams(location.search);
  pathSegs = (p.get('path') || '').split('/').filter(Boolean);
  load();
});

updateURL(false);
load();
