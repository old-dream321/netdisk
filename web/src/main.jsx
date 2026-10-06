import React, { useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN.js';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn.js';
import 'antd/dist/reset.css';
import './styles.css';
import App from './App.jsx';
import { applyMode, initTheme, saveMode, themeConfig } from './theme.js';

dayjs.locale('zh-cn');

function Root() {
  const [mode, setMode] = useState(initTheme);
  useEffect(() => {
    applyMode(mode);
  }, [mode]);

  const toggleMode = () =>
    setMode((m) => {
      const next = m === 'dark' ? 'light' : 'dark';
      saveMode(next);
      return next;
    });

  return (
    <ConfigProvider locale={zhCN} theme={themeConfig(mode)}>
      <AntApp>
        <App mode={mode} onToggleMode={toggleMode} />
      </AntApp>
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')).render(<Root />);
