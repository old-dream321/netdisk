/* ============================================================
   NetDisk 前端逻辑
   零依赖单页应用：认证 / 文件管理 / 分享 / 回收站
   ============================================================ */
'use strict';

/* ======================== 工具 ======================== */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

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

/* ======================== API ======================== */
class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function request(path, opts = {}) {
  const init = {
    method: opts.method || 'GET',
    credentials: 'same-origin',
    headers: Object.assign({}, opts.headers),
  };
  if (opts.json !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(opts.json);
  }
  let res;
  try {
    res = await fetch('/api' + path, init);
  } catch (_) {
    throw new ApiError('无法连接到服务器，请确认后端已启动', 0);
  }
  if (res.status === 401 && !opts.silent401) {
    onSessionLost();
    throw new ApiError('登录已过期，请重新登录', 401);
  }
  let data = null;
  if (res.status !== 204) {
    const ct = res.headers.get('content-type') || '';
    if (ct.includes('application/json')) {
      try { data = await res.json(); } catch (_) { data = null; }
    }
  }
  if (!res.ok) {
    throw new ApiError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status);
  }
  return data;
}

const api = {
  me: (opts = {}) => request('/users/me', opts),
  login: (username, password) =>
    request('/users/login', { method: 'POST', json: { username, password }, silent401: true }),
  register: (username, password, email) =>
    request('/users/register', { method: 'POST', json: { username, password, email } }),
  logout: () => request('/users/logout', { method: 'POST' }),

  listFiles: (parentId, status = 1) =>
    request('/files/list', { method: 'POST', json: { parent_id: parentId, status, limit: 1000 } }),
  listAll: () => request('/files/list', { method: 'POST', json: { status: 1, limit: 1000 } }),
  listTrash: () => request('/files/list', { method: 'POST', json: { status: 2, limit: 1000 } }),
  createDir: (name, parentId) =>
    request('/files/createdir', { method: 'POST', json: { name, parent_id: parentId } }),
  rename: (id, name) => request('/files/' + id, { method: 'PATCH', json: { name } }),
  move: (id, parentId) => request('/files/' + id, { method: 'PATCH', json: { parent_id: parentId } }),
  remove: (id) => request('/files/' + id, { method: 'DELETE' }),
  purge: (id) => request('/files/' + id + '?permanent=true', { method: 'DELETE' }),
  restore: (id) => request('/files/' + id + '/restore', { method: 'POST' }),

  createShare: (fileId, exp) =>
    request('/shares/create', { method: 'POST', json: exp ? { file_id: fileId, exp } : { file_id: fileId } }),
  listShares: () => request('/shares?limit=1000'),
  revokeShare: (id) => request('/shares/' + id, { method: 'DELETE' }),
};

/* ======================== 状态 ======================== */
const state = {
  user: null,
  view: 'files',
  stack: [{ id: 0, name: '我的文件' }],
  files: [],
  filesMeta: null,
  shares: [],
  trash: [],
};
let allCache = null;
let loadSeq = 0;

function invalidate() { allCache = null; }
function currentFolderId() { return state.stack[state.stack.length - 1].id; }

async function getAllItems() {
  if (allCache) return allCache;
  const res = await api.listAll();
  allCache = {
    map: new Map(res.items.map((f) => [f.id, f])),
    items: res.items,
    truncated: res.count >= res.limit,
  };
  return allCache;
}

/* ======================== 通知 / 弹窗 ======================== */
function toast(message, type = 'info') {
  const root = $('#toast-root');
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  const ico = type === 'success' ? 'check' : type === 'error' ? 'alert' : 'info';
  el.innerHTML = `${icon(ico)}<span></span>`;
  el.lastElementChild.textContent = message;
  el.addEventListener('click', () => dismiss());
  root.appendChild(el);
  const dismiss = () => {
    if (!el.isConnected) return;
    el.classList.add('hide');
    setTimeout(() => el.remove(), 220);
  };
  setTimeout(dismiss, type === 'error' ? 5200 : 3200);
}

function openModal({ title, body, footer = '', large = false, onClose }) {
  const root = $('#modal-root');
  root.innerHTML = `
    <div class="modal-backdrop">
      <div class="modal ${large ? 'modal-lg' : ''}" role="dialog" aria-modal="true">
        <div class="modal-head">
          <h3>${esc(title)}</h3>
          <button class="icon-btn" data-close title="关闭">${icon('x')}</button>
        </div>
        <div class="modal-body">${body}</div>
        ${footer ? `<div class="modal-foot">${footer}</div>` : ''}
      </div>
    </div>`;
  const backdrop = root.firstElementChild;
  let closed = false;
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  const close = () => {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey);
    root.innerHTML = '';
    if (onClose) onClose();
  };
  document.addEventListener('keydown', onKey);
  backdrop.addEventListener('mousedown', (e) => { if (e.target === backdrop) close(); });
  $$('[data-close]', root).forEach((b) => b.addEventListener('click', close));
  const firstInput = root.querySelector('input:not([type=radio]), textarea');
  if (firstInput) {
    setTimeout(() => {
      firstInput.focus();
      if (firstInput.select && firstInput.type !== 'datetime-local') firstInput.select();
    }, 40);
  }
  return { root, close };
}

function confirmModal({ title, message, note = '', confirmText = '确认', danger = false }) {
  return new Promise((resolve) => {
    let done = false;
    const settle = (v) => { if (!done) { done = true; resolve(v); } };
    const m = openModal({
      title,
      body: `
        ${note ? `<div class="modal-note ${danger ? 'danger' : ''}">${icon('alert')}<span>${esc(note)}</span></div>` : ''}
        <p>${esc(message)}</p>`,
      footer: `
        <button class="btn" data-cancel>取消</button>
        <button class="btn ${danger ? 'btn-danger' : 'btn-primary'}" data-ok>${esc(confirmText)}</button>`,
      onClose: () => settle(false),
    });
    $('[data-cancel]', m.root).addEventListener('click', () => { settle(false); m.close(); });
    $('[data-ok]', m.root).addEventListener('click', () => { settle(true); m.close(); });
  });
}

/* ======================== 视图辅助 ======================== */
function emptyHTML(iconName, title, hint) {
  return `
    <div class="empty">
      <span class="empty-ico">${icon(iconName, 'xl')}</span>
      <h3>${esc(title)}</h3>
      <p>${esc(hint)}</p>
    </div>`;
}
function loadingHTML() {
  return `<div class="panel"><div class="loading-row"><span class="spinner"></span>加载中…</div></div>`;
}
function errorHTML(message) {
  return `
    <div class="panel">
      <div class="empty">
        <span class="empty-ico" style="background:var(--danger-weak);color:var(--danger)">${icon('alert', 'xl')}</span>
        <h3>加载失败</h3>
        <p>${esc(message)}</p>
        <button class="btn" style="margin-top:14px" data-action="refresh">${icon('refresh')}重试</button>
      </div>
    </div>`;
}
function renderContent(html) { $('#content').innerHTML = html; }

/* ======================== 文件类型 ======================== */
const EXT_GROUPS = [
  ['image', ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif']],
  ['video', ['mp4', 'mkv', 'avi', 'mov', 'webm', 'flv', 'wmv', 'm4v']],
  ['audio', ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma']],
  ['archive', ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz']],
  ['code', ['js', 'ts', 'jsx', 'tsx', 'py', 'go', 'java', 'c', 'cpp', 'h', 'hpp', 'cs', 'rs', 'rb', 'php', 'html', 'css', 'scss', 'json', 'xml', 'sh', 'yaml', 'yml', 'sql']],
  ['sheet', ['xls', 'xlsx', 'csv']],
  ['ppt', ['ppt', 'pptx']],
];

function fileKind(f) {
  if (f.type === 2) return { icon: 'folder', cls: 'ico-folder' };
  const name = f.name || '';
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
  for (const [group, list] of EXT_GROUPS) {
    if (list.includes(ext)) return { icon: group, cls: 'ico-' + group };
  }
  return { icon: 'file', cls: 'ico-file' };
}

/* ======================== 认证 ======================== */
let authMode = 'login';

function hideAuthError() { $('#auth-error').classList.add('hidden'); }
function showAuthError(message) {
  $('#auth-error-text').textContent = message;
  $('#auth-error').classList.remove('hidden');
}

function setAuthMode(mode) {
  authMode = mode;
  $('#auth-tabs').classList.toggle('register', mode === 'register');
  $$('.auth-tab', $('#auth-tabs')).forEach((t) => t.classList.toggle('active', t.dataset.mode === mode));
  $('#email-field').style.display = mode === 'register' ? '' : 'none';
  $('#auth-submit').textContent = mode === 'register' ? '注 册' : '登 录';
  $('#auth-email').required = mode === 'register';
  hideAuthError();
}

function showLogin() {
  $('#app').classList.add('hidden');
  $('#login-view').classList.remove('hidden');
  $('#auth-password').value = '';
  hideAuthError();
}

function showApp() {
  $('#login-view').classList.add('hidden');
  $('#app').classList.remove('hidden');
  renderUserCard();
}

function onSessionLost() {
  if (!state.user) return;
  state.user = null;
  invalidate();
  showLogin();
  toast('登录已过期，请重新登录', 'error');
}

async function handleAuthSubmit(e) {
  e.preventDefault();
  const username = $('#auth-username').value.trim();
  const password = $('#auth-password').value;
  const email = $('#auth-email').value.trim();
  hideAuthError();
  if (!username || !password) { showAuthError('用户名和密码不能为空'); return; }
  if (authMode === 'register' && !email) { showAuthError('请填写邮箱'); return; }

  const btn = $('#auth-submit');
  btn.disabled = true;
  btn.textContent = authMode === 'register' ? '注册中…' : '登录中…';
  try {
    let user;
    if (authMode === 'register') {
      await api.register(username, password, email);
      user = await api.login(username, password);
    } else {
      user = await api.login(username, password);
    }
    try {
      state.user = await api.me({ silent401: true });
    } catch (_) {
      state.user = user;
    }
    $('#auth-form').reset();
    showApp();
    startApp();
    warmCounts();
    toast(`欢迎回来，${state.user.username}`, 'success');
  } catch (err) {
    showAuthError(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = authMode === 'register' ? '注 册' : '登 录';
  }
}

/* ======================== 侧边栏 / 顶栏 ======================== */
function renderUserCard() {
  const u = state.user;
  if (!u) return;
  $('#user-avatar').textContent = (u.username || '?').trim().charAt(0) || '?';
  $('#user-name').textContent = u.username || '';
  $('#user-email').textContent = u.email || '';
  renderStorage();
}

function renderStorage() {
  const u = state.user;
  if (!u) return;
  const bar = $('#storage-bar');
  const text = $('#storage-text');
  const hint = $('#storage-hint');
  if (!u.quota || u.quota <= 0) {
    text.textContent = fmtSize(u.used) + ' 已用';
    bar.style.width = '0%';
    bar.classList.remove('danger');
    hint.textContent = '不限容量';
    return;
  }
  const pct = Math.min(100, (u.used / u.quota) * 100);
  text.textContent = fmtSize(u.used) + ' / ' + fmtSize(u.quota);
  bar.style.width = pct.toFixed(1) + '%';
  bar.classList.toggle('danger', pct > 90);
  hint.textContent = pct > 90
    ? '空间即将用尽'
    : '剩余 ' + fmtSize(Math.max(0, u.quota - u.used));
}

function updateNavCounts() {
  const set = (sel, n) => {
    const el = $(sel);
    el.textContent = n;
    el.classList.toggle('hidden', !n);
  };
  set('#shares-count', state.shares.length);
  set('#trash-count', state.trash.length);
}

function renderNav() {
  $$('#app .nav-item').forEach((a) => a.classList.toggle('active', a.dataset.nav === state.view));
  updateNavCounts();
}

let userTimer = null;
function scheduleUserRefresh() {
  clearTimeout(userTimer);
  userTimer = setTimeout(refreshUser, 600);
}
async function refreshUser() {
  try {
    state.user = await api.me();
    renderUserCard();
  } catch (_) { /* 401 已由 request 处理 */ }
}

function crumbsHTML() {
  const parts = state.stack.map((c, i) => {
    const current = i === state.stack.length - 1;
    const name = esc(c.name);
    return current
      ? `<span class="crumb current">${name}</span>`
      : `<a class="crumb" href="#/files/${c.id}">${name}</a>`;
  });
  return parts.join(icon('chevron-right', 'crumb-sep'));
}

function renderTopbar() {
  const top = $('#topbar');
  if (state.view === 'files') {
    top.innerHTML = `
      <div class="topbar-left"><div class="crumbs">${crumbsHTML()}</div></div>
      <div class="topbar-actions">
        <button class="btn" data-action="newdir">${icon('folder-plus')}<span class="label">新建文件夹</span></button>
        <button class="btn btn-primary" data-action="upload">${icon('upload')}<span class="label">上传文件</span></button>
        <button class="icon-btn" data-action="refresh" title="刷新">${icon('refresh')}</button>
      </div>`;
  } else if (state.view === 'shares') {
    top.innerHTML = `
      <div class="topbar-left">
        <h1 class="page-title">我的分享</h1>
        <span class="page-sub">${state.shares.length} 个链接</span>
      </div>
      <div class="topbar-actions">
        <button class="icon-btn" data-action="refresh" title="刷新">${icon('refresh')}</button>
      </div>`;
  } else {
    top.innerHTML = `
      <div class="topbar-left">
        <h1 class="page-title">回收站</h1>
        <span class="page-sub">${state.trash.length} 项</span>
      </div>
      <div class="topbar-actions">
        <button class="btn btn-danger-ghost" data-action="empty-trash" ${state.trash.length ? '' : 'disabled'}>
          ${icon('trash')}<span class="label">清空回收站</span>
        </button>
        <button class="icon-btn" data-action="refresh" title="刷新">${icon('refresh')}</button>
      </div>`;
  }
}

/* ======================== 数据加载 ======================== */
async function resolveStack(id) {
  const fallback = [{ id: 0, name: '我的文件' }, { id, name: '文件夹' }];
  try {
    const all = await getAllItems();
    const chain = [];
    let cur = all.map.get(id);
    if (!cur) return fallback;
    while (cur && cur.id !== 0) {
      chain.unshift({ id: cur.id, name: cur.name });
      cur = cur.parent_id ? all.map.get(cur.parent_id) : null;
    }
    chain.unshift({ id: 0, name: '我的文件' });
    return chain;
  } catch (_) {
    return fallback;
  }
}

async function loadFiles(id) {
  const seq = ++loadSeq;
  if (id === 0) {
    state.stack = [{ id: 0, name: '我的文件' }];
  } else {
    const idx = state.stack.findIndex((s) => s.id === id);
    if (idx >= 0) {
      state.stack = state.stack.slice(0, idx + 1);
    } else {
      state.stack = [{ id: 0, name: '我的文件' }];
      renderTopbar();
      const chain = await resolveStack(id);
      if (seq !== loadSeq) return;
      state.stack = chain;
    }
  }
  renderTopbar();
  renderNav();
  renderContent(loadingHTML());

  let data;
  try {
    data = await api.listFiles(id);
  } catch (err) {
    if (seq === loadSeq) renderContent(errorHTML(err.message));
    return;
  }
  if (seq !== loadSeq) return;
  state.files = data.items;
  state.filesMeta = { count: data.count, limit: data.limit };
  renderTopbar();
  renderContent(filesHTML());
}

async function loadShares() {
  const seq = ++loadSeq;
  renderTopbar();
  renderNav();
  renderContent(loadingHTML());
  try {
    const data = await api.listShares();
    if (seq !== loadSeq) return;
    state.shares = data.items;
  } catch (err) {
    if (seq === loadSeq) renderContent(errorHTML(err.message));
    return;
  }
  renderTopbar();
  renderNav();
  renderContent(sharesHTML());
}

async function loadTrash() {
  const seq = ++loadSeq;
  renderTopbar();
  renderNav();
  renderContent(loadingHTML());
  try {
    const data = await api.listTrash();
    if (seq !== loadSeq) return;
    state.trash = data.items;
  } catch (err) {
    if (seq === loadSeq) renderContent(errorHTML(err.message));
    return;
  }
  renderTopbar();
  renderNav();
  renderContent(trashHTML());
}

async function warmCounts() {
  try {
    const d = await api.listShares();
    state.shares = d.items;
  } catch (_) {}
  try {
    const d = await api.listTrash();
    state.trash = d.items;
  } catch (_) {}
  updateNavCounts();
  if (state.view === 'shares' || state.view === 'trash') renderTopbar();
}

function refreshCurrent() {
  if (state.view === 'files') loadFiles(currentFolderId());
  else if (state.view === 'shares') loadShares();
  else loadTrash();
}

/* ======================== 列表渲染 ======================== */
function actionBtn(action, iconName, title, danger = false) {
  return `<button class="icon-btn ${danger ? 'danger' : ''}" data-action="${action}" title="${esc(title)}" aria-label="${esc(title)}">${icon(iconName)}</button>`;
}

function filesHTML() {
  if (!state.files.length) {
    return emptyHTML('folder', '这里还没有文件', '把文件拖进页面，或点击右上角「上传文件」开始使用');
  }
  const rows = state.files.map((f) => {
    const kind = fileKind(f);
    const isDir = f.type === 2;
    const actions = isDir
      ? actionBtn('zip', 'download', '打包下载') +
        actionBtn('share', 'share', '分享') +
        actionBtn('rename', 'pencil', '重命名') +
        actionBtn('move', 'move', '移动') +
        actionBtn('delete', 'trash', '移入回收站', true)
      : actionBtn('download', 'download', '下载') +
        actionBtn('share', 'share', '分享') +
        actionBtn('rename', 'pencil', '重命名') +
        actionBtn('move', 'move', '移动') +
        actionBtn('delete', 'trash', '移入回收站', true);
    return `
      <div class="file-row" data-id="${f.id}" data-kind="${isDir ? 'dir' : 'file'}" tabindex="0">
        <div class="file-main">
          <span class="file-ico ${kind.cls}">${icon(kind.icon)}</span>
          <div class="file-text">
            <span class="file-name" title="${esc(f.name)}">${esc(f.name)}${isDir ? icon('chevron-right', 'chev') : ''}</span>
          </div>
        </div>
        <div class="file-size">${isDir ? '—' : esc(fmtSize(f.size))}</div>
        <div class="row-actions">${actions}</div>
      </div>`;
  }).join('');
  const more = state.filesMeta && state.filesMeta.count >= state.filesMeta.limit
    ? `（仅显示前 ${state.filesMeta.limit} 项）`
    : '';
  return `
    <div class="panel">
      ${rows}
      <div class="panel-foot">共 ${state.files.length} 项${more}</div>
    </div>`;
}

function sharesHTML() {
  if (!state.shares.length) {
    return emptyHTML('share', '还没有创建分享', '在文件列表中选择「分享」，即可生成对外链接');
  }
  const rows = state.shares.map((s) => {
    let badge = '<span class="badge badge-green">有效</span>';
    if (!s.available) badge = '<span class="badge badge-gray">文件已删除</span>';
    else if (s.expired) badge = '<span class="badge badge-amber">已过期</span>';
    const expiry = s.expires_at ? fmtTime(s.expires_at) + ' 到期' : '永久有效';
    return `
      <div class="file-row" data-share-id="${s.id}" data-token="${esc(s.token)}">
        <div class="file-main">
          <span class="file-ico ico-folder">${icon('link')}</span>
          <div class="file-text">
            <span class="file-name" title="${esc(s.file_name)}">${esc(s.file_name || '（文件已删除）')}</span>
            <span class="file-sub">${esc(expiry)}</span>
          </div>
        </div>
        <div class="file-size">${badge}</div>
        <div class="row-actions">
          ${actionBtn('copy-link', 'copy', '复制链接')}
          ${actionBtn('open-share', 'external', '打开链接')}
          ${actionBtn('revoke-share', 'trash', '取消分享', true)}
        </div>
      </div>`;
  }).join('');
  return `
    <div class="panel">
      ${rows}
      <div class="panel-foot">共 ${state.shares.length} 个分享链接</div>
    </div>`;
}

function trashHTML() {
  if (!state.trash.length) {
    return emptyHTML('trash', '回收站是空的', '删除的文件会先放在这里，之后可以恢复或彻底删除');
  }
  const rows = state.trash.map((f) => {
    const kind = fileKind(f);
    const isDir = f.type === 2;
    return `
      <div class="file-row" data-id="${f.id}" data-kind="trash">
        <div class="file-main">
          <span class="file-ico ${kind.cls}" style="opacity:.75">${icon(kind.icon)}</span>
          <div class="file-text">
            <span class="file-name" title="${esc(f.name)}">${esc(f.name)}</span>
            <span class="file-sub">${isDir ? '文件夹' : '文件'}${f.deleted_at ? ' · ' + esc(fmtTime(f.deleted_at)) + ' 删除' : ''}</span>
          </div>
        </div>
        <div class="file-size">${isDir ? '—' : esc(fmtSize(f.size))}</div>
        <div class="row-actions">
          ${actionBtn('restore', 'restore', '恢复')}
          ${actionBtn('purge', 'trash', '彻底删除', true)}
        </div>
      </div>`;
  }).join('');
  return `
    <div class="panel">
      ${rows}
      <div class="panel-foot">共 ${state.trash.length} 项 · 回收站内容仍占用存储空间</div>
    </div>`;
}

/* ======================== 操作 ======================== */
function findItem(id) {
  return state.files.find((f) => f.id === id) || (allCache && allCache.map.get(id)) || null;
}

function download(url) {
  const a = document.createElement('a');
  a.href = url;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function newDirModal() {
  const m = openModal({
    title: '新建文件夹',
    body: `
      <div class="field" style="margin-bottom:0">
        <label for="newdir-input">文件夹名称</label>
        <input class="input" id="newdir-input" placeholder="例如：学习资料" maxlength="255">
      </div>`,
    footer: `<button class="btn" data-close>取消</button><button class="btn btn-primary" id="newdir-ok">创建</button>`,
  });
  const input = $('#newdir-input', m.root);
  const ok = $('#newdir-ok', m.root);
  const submit = async () => {
    const name = input.value.trim();
    if (!name) { toast('名称不能为空', 'error'); return; }
    ok.disabled = true;
    try {
      await api.createDir(name, currentFolderId());
      toast('文件夹已创建', 'success');
      m.close();
      invalidate();
      loadFiles(currentFolderId());
    } catch (err) {
      toast(err.message, 'error');
      ok.disabled = false;
    }
  };
  ok.addEventListener('click', submit);
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
}

function renameModal(file) {
  const m = openModal({
    title: '重命名',
    body: `
      <div class="field" style="margin-bottom:0">
        <label for="rename-input">新名称</label>
        <input class="input" id="rename-input" value="${esc(file.name)}" maxlength="255">
      </div>`,
    footer: `<button class="btn" data-close>取消</button><button class="btn btn-primary" id="rename-ok">保存</button>`,
  });
  const input = $('#rename-input', m.root);
  const ok = $('#rename-ok', m.root);
  const submit = async () => {
    const name = input.value.trim();
    if (!name) { toast('名称不能为空', 'error'); return; }
    if (name === file.name) { m.close(); return; }
    ok.disabled = true;
    try {
      await api.rename(file.id, name);
      toast('已重命名', 'success');
      m.close();
      invalidate();
      loadFiles(currentFolderId());
    } catch (err) {
      toast(err.message, 'error');
      ok.disabled = false;
    }
  };
  ok.addEventListener('click', submit);
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
}

async function moveModal(file) {
  let all;
  try {
    all = await getAllItems();
  } catch (err) {
    toast('加载目录失败：' + err.message, 'error');
    return;
  }
  const kids = new Map();
  for (const it of all.items) {
    if (it.type !== 2) continue;
    if (!kids.has(it.parent_id)) kids.set(it.parent_id, []);
    kids.get(it.parent_id).push(it);
  }
  // 被移动的目录自身和它的子树不能作为目标：直接从候选里去掉。
  const banned = new Set([file.id]);
  if (file.type === 2) {
    const stack = [file.id];
    while (stack.length) {
      const cur = stack.pop();
      for (const c of kids.get(cur) || []) {
        banned.add(c.id);
        stack.push(c.id);
      }
    }
  }
  const treeHTML = (parentId) => {
    const list = (kids.get(parentId) || [])
      .filter((it) => !banned.has(it.id))
      .slice()
      .sort((a, b) => a.name.localeCompare(b.name, 'zh'));
    if (!list.length) return '';
    return `<ul>${list.map((it) => `
      <li>
        <div class="tree-node ${banned.has(it.id) ? 'disabled' : ''}" data-tree-id="${it.id}">
          ${icon('folder')}<span class="tree-name">${esc(it.name)}</span>
        </div>
        ${treeHTML(it.id)}
      </li>`).join('')}</ul>`;
  };
  const m = openModal({
    title: `移动「${file.name}」`,
    large: true,
    body: `
      <p class="muted" style="margin-bottom:10px">选择目标文件夹：</p>
      <div class="tree" id="move-tree">
        <div class="tree-node" data-tree-id="0">${icon('folder')}<span class="tree-name">根目录</span></div>
        ${treeHTML(0)}
      </div>
      ${all.truncated ? '<div class="storage-hint" style="margin-top:8px">目录较多，仅显示前 1000 项。</div>' : ''}`,
    footer: `<button class="btn" data-close>取消</button><button class="btn btn-primary" id="move-ok" disabled>移动到此处</button>`,
  });
  let selId = file.parent_id;
  const ok = $('#move-ok', m.root);
  const nodes = $$('.tree-node', m.root);
  const select = (id) => {
    selId = id;
    nodes.forEach((n) => n.classList.toggle('selected', Number(n.dataset.treeId) === id));
    ok.disabled = selId === file.parent_id;
  };
  nodes.forEach((n) => n.addEventListener('click', () => {
    if (n.classList.contains('disabled')) return;
    select(Number(n.dataset.treeId));
  }));
  select(selId);
  ok.addEventListener('click', async () => {
    ok.disabled = true;
    try {
      await api.move(file.id, selId);
      toast('已移动', 'success');
      m.close();
      invalidate();
      loadFiles(currentFolderId());
    } catch (err) {
      toast(err.message, 'error');
      ok.disabled = false;
    }
  });
}

function shareModal(file) {
  const m = openModal({
    title: `分享「${file.name}」`,
    body: `
      <p class="muted" style="margin-bottom:12px">创建后，任何拿到链接的人都能访问，无需登录。</p>
      <div class="option-list" id="exp-options">
        <label class="option selected"><input type="radio" name="exp" value="never" checked><span>永久有效</span></label>
        <label class="option"><input type="radio" name="exp" value="1"><span>1 天</span></label>
        <label class="option"><input type="radio" name="exp" value="7"><span>7 天</span></label>
        <label class="option"><input type="radio" name="exp" value="30"><span>30 天</span></label>
        <label class="option"><input type="radio" name="exp" value="custom"><span>自定义到期时间</span></label>
      </div>
      <div class="field hidden" id="exp-custom" style="margin:12px 0 0">
        <input class="input" type="datetime-local" id="exp-datetime">
      </div>`,
    footer: `<button class="btn" data-close>取消</button><button class="btn btn-primary" id="share-ok">创建分享链接</button>`,
  });

  const options = $$('#exp-options .option', m.root);
  const custom = $('#exp-custom', m.root);
  options.forEach((opt) => {
    opt.addEventListener('click', () => {
      options.forEach((o) => o.classList.toggle('selected', o === opt));
      custom.classList.toggle('hidden', $('input', opt).value !== 'custom');
      if ($('input', opt).value === 'custom') {
        const dt = $('#exp-datetime', m.root);
        const d = new Date(Date.now() + 60000);
        d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
        dt.min = d.toISOString().slice(0, 16);
      }
    });
  });

  const ok = $('#share-ok', m.root);
  ok.addEventListener('click', async () => {
    const selected = options.find((o) => $('input', o).checked);
    const value = selected ? $('input', selected).value : 'never';
    let exp;
    if (value === 'custom') {
      const raw = $('#exp-datetime', m.root).value;
      if (!raw) { toast('请选择到期时间', 'error'); return; }
      const d = new Date(raw);
      if (d.getTime() <= Date.now() + 60000) { toast('到期时间需要晚于当前时间', 'error'); return; }
      exp = d.toISOString();
    } else if (value !== 'never') {
      exp = new Date(Date.now() + Number(value) * 86400000).toISOString();
    }
    ok.disabled = true;
    try {
      const res = await api.createShare(file.id, exp);
      m.close();
      shareResultModal(res);
      api.listShares().then((d) => { state.shares = d.items; updateNavCounts(); }).catch(() => {});
    } catch (err) {
      toast(err.message, 'error');
      ok.disabled = false;
    }
  });
}

function shareResultModal(res) {
  const link = location.origin + res.url;
  const m = openModal({
    title: '分享链接已创建',
    body: `
      <div class="share-result">
        <div class="link-box">
          <input id="share-link" readonly value="${esc(link)}">
          <button class="btn btn-sm btn-primary" id="share-copy">${icon('copy')}复制</button>
        </div>
        <div class="share-expire">${icon('clock')}${res.expires_at ? esc(fmtTime(res.expires_at)) + ' 到期' : '永久有效'}</div>
        <div class="modal-note" style="margin-bottom:0">${icon('info')}<span>可以在「我的分享」中随时取消这个链接。</span></div>
      </div>`,
    footer: `<button class="btn" data-close>完成</button><button class="btn btn-primary" id="share-open">${icon('external')}打开链接</button>`,
  });
  $('#share-copy', m.root).addEventListener('click', async () => {
    if (await copyText(link)) toast('链接已复制', 'success');
    else toast('复制失败，请手动复制', 'error');
  });
  $('#share-open', m.root).addEventListener('click', () => window.open(link, '_blank', 'noopener'));
}

async function deleteFile(file) {
  const ok = await confirmModal({
    title: '移入回收站',
    message: `确定把「${file.name}」移入回收站吗？`,
    note: '删除后仍可在回收站中恢复。',
    confirmText: '移入回收站',
    danger: true,
  });
  if (!ok) return;
  try {
    await api.remove(file.id);
    toast('已移入回收站', 'success');
    invalidate();
    loadFiles(currentFolderId());
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function restoreItem(file) {
  try {
    const res = await api.restore(file.id);
    toast(`已恢复 ${res.restored} 项`, 'success');
    invalidate();
    loadTrash();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function purgeItem(file) {
  const ok = await confirmModal({
    title: '彻底删除',
    message: `确定彻底删除「${file.name}」吗？`,
    note: '彻底删除后无法恢复；如果是文件夹，其中的所有内容会一起删除。',
    confirmText: '彻底删除',
    danger: true,
  });
  if (!ok) return;
  try {
    await api.purge(file.id);
    toast('已彻底删除', 'success');
    invalidate();
    loadTrash();
    scheduleUserRefresh();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function emptyTrash() {
  const n = state.trash.length;
  const ok = await confirmModal({
    title: '清空回收站',
    message: '确定清空回收站吗？',
    note: `将彻底删除回收站中的 ${n} 项内容，且无法恢复。`,
    confirmText: '清空回收站',
    danger: true,
  });
  if (!ok) return;
  const items = state.trash.slice();
  let done = 0, failed = 0;
  for (const it of items) {
    try {
      await api.purge(it.id);
      done++;
    } catch (err) {
      if (err.status === 404) continue; // 已被父项连带删除
      failed++;
    }
  }
  invalidate();
  if (done) toast(`已彻底删除 ${done} 项`, 'success');
  if (failed) toast(`${failed} 项删除失败`, 'error');
  await refreshUser();
  loadTrash();
}

async function revokeShare(id) {
  const ok = await confirmModal({
    title: '取消分享',
    message: '确定取消这个分享链接吗？',
    note: '取消后链接立即失效，对方将无法再访问。',
    confirmText: '取消分享',
    danger: true,
  });
  if (!ok) return;
  try {
    await api.revokeShare(id);
    toast('分享已取消', 'success');
    loadShares();
  } catch (err) {
    toast(err.message, 'error');
  }
}

/* ======================== 上传 ======================== */
const uploads = new Map();
let uploadSeq = 0;
let uploaderRaf = 0;
let dirRefreshTimer = null;

function renderUploader() {
  if (uploaderRaf) return;
  uploaderRaf = requestAnimationFrame(() => {
    uploaderRaf = 0;
    renderUploaderNow();
  });
}

function renderUploaderNow() {
  const root = $('#uploader-root');
  if (!uploads.size) {
    root.innerHTML = '';
    return;
  }
  const items = Array.from(uploads.values());
  const active = items.filter((u) => u.status === 'uploading').length;
  root.innerHTML = `
    <div class="uploader">
      <div class="uploader-head">
        <span>${active ? `正在上传 ${active} 个文件` : '上传任务'}</span>
        <button class="icon-btn" id="uploader-clear" title="清除已完成">${icon('x')}</button>
      </div>
      ${items.map((u) => `
        <div class="upload-item">
          <div class="upload-line">
            <span class="upload-name" title="${esc(u.name)}">${esc(u.name)}</span>
            ${u.status === 'uploading'
              ? `<span class="upload-status">${Math.round(u.progress * 100)}%</span>`
              : u.status === 'done'
                ? '<span class="upload-status ok">已完成</span>'
                : `<span class="upload-status err" title="${esc(u.error)}">失败 · ${esc(u.error)}</span>`}
          </div>
          <div class="progress">
            <div class="progress-bar ${u.status === 'error' ? 'danger' : ''}"
                 style="width:${Math.round((u.status === 'done' ? 1 : u.progress) * 100)}%"></div>
          </div>
        </div>`).join('')}
    </div>`;
  const clearBtn = $('#uploader-clear', root);
  if (clearBtn) {
    clearBtn.addEventListener('click', () => {
      uploads.forEach((v, k) => { if (v.status !== 'uploading') uploads.delete(k); });
      renderUploader();
    });
  }
}

function scheduleDirRefresh(parentId) {
  if (state.view !== 'files' || currentFolderId() !== parentId) return;
  clearTimeout(dirRefreshTimer);
  dirRefreshTimer = setTimeout(() => loadFiles(currentFolderId()), 400);
}

function uploadFiles(fileList, parentId) {
  for (const file of fileList) {
    if (file.size > 1024 * 1024 * 1024) {
      toast(`「${file.name}」超过 1GiB 上限，已跳过`, 'error');
      continue;
    }
    startUpload(file, parentId);
  }
}

function startUpload(file, parentId) {
  const id = ++uploadSeq;
  const u = { id, name: file.name, progress: 0, status: 'uploading', error: '' };
  uploads.set(id, u);
  renderUploader();

  const fd = new FormData();
  fd.append('file', file);
  fd.append('parent_id', String(parentId));

  const xhr = new XMLHttpRequest();
  xhr.open('POST', '/api/files/upload');
  xhr.withCredentials = true;
  xhr.upload.onprogress = (e) => {
    if (e.lengthComputable) {
      u.progress = e.loaded / e.total;
      renderUploader();
    }
  };
  xhr.onload = () => {
    if (xhr.status >= 200 && xhr.status < 300) {
      u.status = 'done';
      u.progress = 1;
      renderUploader();
      invalidate();
      scheduleDirRefresh(parentId);
      scheduleUserRefresh();
      setTimeout(() => { uploads.delete(id); renderUploader(); }, 2600);
    } else {
      if (xhr.status === 401) {
        uploads.delete(id);
        renderUploader();
        onSessionLost();
        return;
      }
      let msg = `上传失败（HTTP ${xhr.status}）`;
      try {
        const d = JSON.parse(xhr.responseText);
        if (d && d.error) msg = d.error;
      } catch (_) {}
      u.status = 'error';
      u.error = msg;
      renderUploader();
      setTimeout(() => { uploads.delete(id); renderUploader(); }, 8000);
    }
  };
  xhr.onerror = () => {
    u.status = 'error';
    u.error = '网络错误，上传中断';
    renderUploader();
    setTimeout(() => { uploads.delete(id); renderUploader(); }, 8000);
  };
  xhr.send(fd);
}

/* ======================== 拖拽上传 ======================== */
let dragDepth = 0;

function hasFiles(e) {
  return e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
}
function showDrop() { $('#drop-overlay').classList.remove('hidden'); }
function hideDrop() { $('#drop-overlay').classList.add('hidden'); }

/* ======================== 路由与主流程 ======================== */
function parseRoute() {
  const raw = (location.hash || '').replace(/^#\/?/, '');
  const parts = raw.split('/').filter(Boolean);
  if (parts[0] === 'shares') return { view: 'shares' };
  if (parts[0] === 'trash') return { view: 'trash' };
  const id = parts[0] === 'files' && parts[1] ? Number(parts[1]) || 0 : 0;
  return { view: 'files', id };
}

async function route() {
  if (!state.user) return;
  const r = parseRoute();
  state.view = r.view;
  renderNav();
  if (r.view === 'files') await loadFiles(r.id);
  else if (r.view === 'shares') await loadShares();
  else await loadTrash();
}

function startApp() {
  if (!/^#\/(files|shares|trash)/.test(location.hash || '')) {
    location.hash = '#/files';
  }
  route();
}

/* ======================== 事件绑定 ======================== */
async function handleAction(action, el, row) {
  switch (action) {
    case 'upload':
      $('#file-input').click();
      break;
    case 'newdir':
      newDirModal();
      break;
    case 'refresh':
      refreshCurrent();
      break;
    case 'download': {
      const f = findItem(row.id);
      if (f) download('/api/files/' + f.id + '/download');
      break;
    }
    case 'zip': {
      const f = findItem(row.id);
      if (f) download('/api/files/' + f.id + '/zip');
      break;
    }
    case 'share': {
      const f = findItem(row.id);
      if (f) shareModal(f);
      break;
    }
    case 'rename': {
      const f = findItem(row.id);
      if (f) renameModal(f);
      break;
    }
    case 'move': {
      const f = findItem(row.id);
      if (f) moveModal(f);
      break;
    }
    case 'delete': {
      const f = findItem(row.id);
      if (f) deleteFile(f);
      break;
    }
    case 'restore': {
      const f = state.trash.find((it) => it.id === row.id);
      if (f) restoreItem(f);
      break;
    }
    case 'purge': {
      const f = state.trash.find((it) => it.id === row.id);
      if (f) purgeItem(f);
      break;
    }
    case 'empty-trash':
      emptyTrash();
      break;
    case 'copy-link': {
      const link = location.origin + '/s/' + row.token;
      if (await copyText(link)) toast('链接已复制', 'success');
      else toast('复制失败，请手动复制', 'error');
      break;
    }
    case 'open-share':
      window.open(location.origin + '/s/' + row.token, '_blank', 'noopener');
      break;
    case 'revoke-share': {
      const s = state.shares.find((it) => it.id === row.shareId);
      if (s) revokeShare(s.id);
      break;
    }
    default:
      break;
  }
}

function bindEvents() {
  /* 登录 / 注册 */
  $$('.auth-tab', $('#auth-tabs')).forEach((t) => {
    t.addEventListener('click', () => setAuthMode(t.dataset.mode));
  });
  $('#auth-form').addEventListener('submit', handleAuthSubmit);

  /* 退出登录 */
  $('#logout-btn').addEventListener('click', async () => {
    try { await api.logout(); } catch (_) {}
    state.user = null;
    state.shares = [];
    state.trash = [];
    state.files = [];
    state.stack = [{ id: 0, name: '我的文件' }];
    invalidate();
    updateNavCounts();
    showLogin();
  });

  /* 行内 / 顶栏动作（事件委托） */
  $('#app').addEventListener('click', async (e) => {
    const actionEl = e.target.closest('[data-action]');
    if (actionEl) {
      e.preventDefault();
      const rowEl = actionEl.closest('[data-id], [data-share-id]');
      const row = rowEl
        ? {
            id: Number(rowEl.dataset.id || 0),
            shareId: Number(rowEl.dataset.shareId || 0),
            token: rowEl.dataset.token || '',
          }
        : { id: 0, shareId: 0, token: '' };
      await handleAction(actionEl.dataset.action, actionEl, row);
      return;
    }
    const fileRow = e.target.closest('.file-row[data-id][data-kind]');
    if (fileRow && !e.target.closest('button, a')) {
      const kind = fileRow.dataset.kind;
      const id = Number(fileRow.dataset.id);
      if (kind === 'dir') location.hash = '#/files/' + id;
      else if (kind === 'file') download('/api/files/' + id + '/download');
    }
  });

  /* 上传输入框 */
  const fileInput = $('#file-input');
  fileInput.addEventListener('change', () => {
    if (fileInput.files.length) uploadFiles(Array.from(fileInput.files), currentFolderId());
    fileInput.value = '';
  });

  /* 拖拽上传 */
  window.addEventListener('dragenter', (e) => {
    if (!state.user || state.view !== 'files' || !hasFiles(e)) return;
    e.preventDefault();
    dragDepth++;
    showDrop();
  });
  window.addEventListener('dragover', (e) => {
    if (!state.user || state.view !== 'files' || !hasFiles(e)) return;
    e.preventDefault();
  });
  window.addEventListener('dragleave', () => {
    if (dragDepth > 0) dragDepth--;
    if (dragDepth === 0) hideDrop();
  });
  window.addEventListener('drop', (e) => {
    if (!state.user || state.view !== 'files' || !hasFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    hideDrop();
    if (e.dataTransfer.files.length) uploadFiles(Array.from(e.dataTransfer.files), currentFolderId());
  });

  window.addEventListener('hashchange', route);
}

/* ======================== 启动 ======================== */
(async function boot() {
  bindEvents();
  try {
    state.user = await api.me({ silent401: true });
    showApp();
    startApp();
    warmCounts();
  } catch (_) {
    showLogin();
  }
  const bootEl = $('#boot');
  if (bootEl) bootEl.remove();
})();
