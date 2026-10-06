// 深色模式：偏好持久化在 localStorage（key: netdisk-theme），默认跟随系统
import { theme } from 'antd';

const THEME_KEY = 'netdisk-theme';

export function getInitialMode() {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === 'dark' || saved === 'light') return saved;
  } catch {
    /* ignore */
  }
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

/** 把当前模式写到 <html data-theme>，并设置 color-scheme（让滚动条/原生控件跟随） */
export function applyMode(mode) {
  const root = document.documentElement;
  root.dataset.theme = mode;
  root.style.colorScheme = mode;
}

export function saveMode(mode) {
  try {
    localStorage.setItem(THEME_KEY, mode);
  } catch {
    /* ignore */
  }
}

/** antd 主题配置 */
export function themeConfig(mode) {
  return {
    algorithm: mode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: { colorPrimary: '#6366f1', borderRadius: 10 },
  };
}

/** React 挂载前调用：应用初始模式并返回，避免刷新闪烁 */
export function initTheme() {
  const mode = getInitialMode();
  applyMode(mode);
  return mode;
}
