// 后端接口封装 + 常用工具（与 Go 后端保持同一套接口约定）

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

export async function request(path, options = {}) {
  const init = {
    method: options.method || 'GET',
    credentials: 'same-origin',
    headers: { ...(options.headers || {}) },
  };
  if (options.json !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.json);
  }
  if (options.form) init.body = options.form;

  let res;
  try {
    res = await fetch('/api' + path, init);
  } catch {
    throw new ApiError('无法连接到服务器，请确认后端已启动', 0);
  }

  let data = null;
  if (res.status !== 204) {
    const ct = res.headers.get('content-type') || '';
    if (ct.includes('application/json')) {
      try { data = await res.json(); } catch { data = null; }
    }
  }
  if (!res.ok) {
    throw new ApiError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status);
  }
  return data;
}

export const api = {
  me: () => request('/users/me'),
  login: (username, password) => request('/users/login', { method: 'POST', json: { username, password } }),
  register: (username, password, email) =>
    request('/users/register', { method: 'POST', json: { username, password, email } }),
  logout: () => request('/users/logout', { method: 'POST' }),

  // parentId 传 undefined 表示不带 parent_id（后端会返回全部文件，用于"移动"的目录树）
  listFiles: (parentId, status = 1) =>
    request('/files/list', { method: 'POST', json: { parent_id: parentId, status, limit: 1000 } }),
  listTrash: () => request('/files/list', { method: 'POST', json: { status: 2, limit: 1000 } }),
  createDir: (name, parentId) => request('/files/createdir', { method: 'POST', json: { name, parent_id: parentId } }),
  rename: (id, name) => request('/files/' + id, { method: 'PATCH', json: { name } }),
  move: (id, parentId) => request('/files/' + id, { method: 'PATCH', json: { parent_id: parentId } }),
  remove: (id) => request('/files/' + id, { method: 'DELETE' }),
  purge: (id) => request('/files/' + id + '?permanent=true', { method: 'DELETE' }),
  restore: (id) => request('/files/' + id + '/restore', { method: 'POST' }),

  createShare: (fileId, exp) =>
    request('/shares/create', { method: 'POST', json: exp ? { file_id: Number(fileId), exp } : { file_id: Number(fileId) } }),
  listShares: () => request('/shares?limit=1000'),
  revokeShare: (id) => request('/shares/' + id, { method: 'DELETE' }),
};

// 上传：用 XHR 才能拿到进度（fetch 没有上传进度事件）
export function uploadFile(file, parentId, { onProgress } = {}) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('file', file);
    fd.append('parent_id', String(parentId));
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/files/upload');
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try { resolve(JSON.parse(xhr.responseText)); } catch { resolve(null); }
        return;
      }
      let msg = `上传失败（HTTP ${xhr.status}）`;
      try {
        const d = JSON.parse(xhr.responseText);
        if (d && d.error) msg = d.error;
      } catch { /* ignore */ }
      reject(new ApiError(msg, xhr.status));
    };
    xhr.onerror = () => reject(new ApiError('网络错误，上传中断', 0));
    xhr.send(fd);
  });
}

export function download(url) {
  const a = document.createElement('a');
  a.href = url;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
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
    } catch {
      return false;
    }
  }
}

/* ---------------- 格式化 / 文件类型 ---------------- */

export function fmtSize(value) {
  let n = Number(value) || 0;
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  const v = n >= 100 ? Math.round(n) : Number(n.toFixed(1));
  return v + ' ' + units[i];
}

export function fmtTime(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  const p = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

const EXT_GROUPS = [
  ['image', ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif']],
  ['video', ['mp4', 'mkv', 'avi', 'mov', 'webm', 'flv', 'wmv', 'm4v']],
  ['audio', ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma']],
  ['archive', ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz']],
  ['sheet', ['xls', 'xlsx', 'csv']],
  ['ppt', ['ppt', 'pptx']],
  ['doc', ['doc', 'docx', 'pdf', 'txt', 'md', 'rtf']],
  ['code', ['js', 'ts', 'jsx', 'tsx', 'py', 'go', 'java', 'c', 'cpp', 'h', 'hpp', 'cs', 'rs', 'rb', 'php', 'html', 'css', 'scss', 'json', 'xml', 'sh', 'yaml', 'yml', 'sql']],
];

export function fileKind(item) {
  if (item.type === 2) return 'folder';
  const name = item.name || '';
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
  for (const [kind, list] of EXT_GROUPS) {
    if (list.includes(ext)) return kind;
  }
  return 'file';
}
