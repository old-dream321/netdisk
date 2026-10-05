import React from 'react';
import ReactDOM from 'react-dom/client';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN.js';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn.js';
import 'antd/dist/reset.css';
import './styles.css';
import App from './App.jsx';

dayjs.locale('zh-cn');

ReactDOM.createRoot(document.getElementById('root')).render(
  <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: '#6366f1', borderRadius: 10 } }}>
    <AntApp>
      <App />
    </AntApp>
  </ConfigProvider>,
);
