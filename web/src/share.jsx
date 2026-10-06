import React, { useCallback, useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import {
  App as AntApp,
  Breadcrumb,
  Button,
  Card,
  ConfigProvider,
  Empty,
  Image,
  Result,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  ClockCircleOutlined,
  CloudFilled,
  CopyOutlined,
  DownloadOutlined,
  MoonOutlined,
  SunOutlined,
} from '@ant-design/icons';
import zhCN from 'antd/locale/zh_CN';
import { copyText, fmtSize, fmtTime } from './api';
import { FileTypeIcon } from './fileMeta';
import FilePreview, { kindByName } from './FilePreview.jsx';
import { applyMode, initTheme, saveMode, themeConfig } from './theme.js';
import 'antd/dist/reset.css';
import './styles.css';

const { Text, Title } = Typography;

/* ---------------- 内容 ---------------- */

function ShareContent({ mode, onToggleMode }) {
  const token = new URLSearchParams(location.search).get('token') || '';
  const [pathSegs, setPathSegs] = useState(() =>
    (new URLSearchParams(location.search).get('path') || '').split('/').filter(Boolean),
  );
  const [status, setStatus] = useState('loading');
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [rootName, setRootName] = useState('');

  const load = useCallback(async () => {
    if (!token) {
      setStatus('error');
      setError('链接不完整：缺少分享标识');
      return;
    }
    setStatus('loading');
    const suffix = pathSegs.length ? '/' + pathSegs.map(encodeURIComponent).join('/') : '';
    let res;
    try {
      res = await fetch('/s/' + encodeURIComponent(token) + suffix, {
        headers: { Accept: 'application/json' },
        credentials: 'same-origin',
      });
    } catch {
      setStatus('error');
      setError('网络错误，无法连接到服务器');
      return;
    }
    if (res.status === 404) {
      let msg = '分享不存在或已失效';
      try {
        const d = await res.json();
        if (d && d.error) msg = d.error;
      } catch {
        /* ignore */
      }
      setStatus('error');
      setError(msg);
      return;
    }
    if (!res.ok) {
      setStatus('error');
      setError(`服务返回了 HTTP ${res.status}`);
      return;
    }
    const d = await res.json();
    setData(d);
    if (!pathSegs.length) setRootName(d.name || '');
    document.title = (d.name || '文件分享') + ' · NetDisk';
    setStatus('ok');
  }, [token, pathSegs]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    const onPop = () => {
      setPathSegs((new URLSearchParams(location.search).get('path') || '').split('/').filter(Boolean));
    };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  const navigate = (segs) => {
    const q = new URLSearchParams();
    q.set('token', token);
    if (segs.length) q.set('path', segs.join('/'));
    history.pushState(null, '', location.pathname + '?' + q.toString());
    setPathSegs(segs);
  };

  const suffix = pathSegs.length ? '/' + pathSegs.map(encodeURIComponent).join('/') : '';
  const zipUrl = '/s/' + encodeURIComponent(token) + suffix + '?download=1';

  return (
    <div className="share-page">
      <div className="share-header">
        <span className="app-logo-mark">
          <CloudFilled />
        </span>
        <b>NetDisk 云盘</b>
        <Tag color="geekblue" style={{ marginLeft: 10 }}>
          {data && data.type === 'dir' ? '文件夹分享' : '文件分享'}
        </Tag>
        <Tooltip title={mode === 'dark' ? '切换到浅色模式' : '切换到深色模式'}>
          <Button
            type="text"
            shape="circle"
            style={{ marginLeft: 'auto' }}
            icon={mode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
            onClick={onToggleMode}
          />
        </Tooltip>
      </div>
      <div className="share-body">
        {status === 'loading' && (
          <div className="share-loading">
            <Spin size="large" />
          </div>
        )}
        {status === 'error' && (
          <Card className="share-card">
            <Result
              status="404"
              title="无法访问"
              subTitle={error}
              extra={
                <Button type="primary" href="/">
                  回到首页
                </Button>
              }
            />
          </Card>
        )}
        {status === 'ok' && data && data.type === 'file' && <ShareFile data={data} />}
        {status === 'ok' && data && data.type === 'dir' && (
          <ShareDir data={data} rootName={rootName} pathSegs={pathSegs} onNavigate={navigate} zipUrl={zipUrl} />
        )}
      </div>
      <div className="share-foot">由 NetDisk 提供分享服务</div>
    </div>
  );
}

/* ---------------- 单个文件 ---------------- */

function ShareFile({ data }) {
  const { message } = AntApp.useApp();
  const kind = kindByName(data.name, data.size);
  const [text, setText] = useState(null);
  const [textLoading, setTextLoading] = useState(false);

  useEffect(() => {
    if (kind !== 'text') return undefined;
    let aborted = false;
    setTextLoading(true);
    setText(null);
    fetch(data.download_url, { credentials: 'same-origin' })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error('HTTP ' + r.status))))
      .then((t) => {
        if (!aborted) setText(t);
      })
      .catch(() => {
        if (!aborted) setText('（无法加载文件内容，请下载查看）');
      })
      .finally(() => {
        if (!aborted) setTextLoading(false);
      });
    return () => {
      aborted = true;
    };
  }, [kind, data.download_url]);

  const copy = async () => {
    if (await copyText(location.href)) message.success('链接已复制');
    else message.error('复制失败，请手动复制');
  };

  return (
    <Card className="share-card">
      <div className="share-hero">
        <FileTypeIcon item={{ name: data.name, type: 1 }} size={76} />
        <Title level={3} style={{ margin: '16px 0 6px', wordBreak: 'break-all' }}>
          {data.name}
        </Title>
        <Text type="secondary">
          <ClockCircleOutlined /> {fmtSize(data.size)} ·{' '}
          {data.expires_at ? fmtTime(data.expires_at) + ' 到期' : '永久有效'}
        </Text>
      </div>
      {kind === 'image' && (
        <div className="share-preview">
          <Image src={data.download_url} style={{ maxHeight: 420, borderRadius: 12 }} />
        </div>
      )}
      {kind === 'video' && (
        <div className="share-preview">
          <video controls preload="metadata" src={data.download_url} />
        </div>
      )}
      {kind === 'audio' && (
        <div className="share-preview">
          <audio controls preload="metadata" src={data.download_url} />
        </div>
      )}
      {kind === 'text' && (
        <div className="share-preview">
          <Spin spinning={textLoading}>
            <pre className="preview-text">{text ?? ''}</pre>
          </Spin>
        </div>
      )}
      <div className="share-actions">
        <Button
          type="primary"
          size="large"
          icon={<DownloadOutlined />}
          onClick={() => {
            location.href = data.download_url;
          }}
        >
          下载文件
        </Button>
        <Button size="large" icon={<CopyOutlined />} onClick={copy}>
          复制分享链接
        </Button>
      </div>
    </Card>
  );
}

/* ---------------- 文件夹 ---------------- */

function ShareDir({ data, rootName, pathSegs, onNavigate, zipUrl }) {
  const items = data.items || [];
  const [preview, setPreview] = useState(null);
  const crumbs = [{ name: rootName || '分享内容', depth: 0 }].concat(
    pathSegs.map((s, i) => ({ name: s, depth: i + 1 })),
  );

  const columns = [
    {
      title: '名称',
      render: (_, r) => {
        const kind = r.type === 'dir' ? null : kindByName(r.name, r.size);
        return (
          <Space>
            <FileTypeIcon item={{ name: r.name, type: r.type === 'dir' ? 2 : 1 }} size={34} />
            {r.type === 'dir' ? (
              <a onClick={() => onNavigate(pathSegs.concat([r.name]))}>{r.name}</a>
            ) : kind ? (
              <a onClick={() => setPreview({ name: r.name, size: r.size, kind, url: r.download_url })}>{r.name}</a>
            ) : (
              <Text>{r.name}</Text>
            )}
          </Space>
        );
      },
    },
    {
      title: '大小',
      width: 120,
      render: (_, r) => <Text type="secondary">{r.type === 'dir' ? '文件夹' : fmtSize(r.size)}</Text>,
    },
    {
      title: '',
      width: 150,
      align: 'right',
      render: (_, r) => {
        if (r.type === 'dir') return null;
        const kind = kindByName(r.name, r.size);
        return (
          <Space size={12}>
            {kind && (
              <a onClick={() => setPreview({ name: r.name, size: r.size, kind, url: r.download_url })}>预览</a>
            )}
            <a href={r.download_url}>下载</a>
          </Space>
        );
      },
    },
  ];

  return (
    <Card className="share-card share-card-dir" styles={{ body: { paddingTop: 20 } }}>
      <div className="share-dir-head">
        <FileTypeIcon item={{ name: data.name, type: 2 }} size={52} />
        <div className="share-dir-title">
          <Title level={4} style={{ margin: 0, wordBreak: 'break-all' }}>
            {data.name}
          </Title>
          <Text type="secondary">
            {items.length} 项 · {data.expires_at ? fmtTime(data.expires_at) + ' 到期' : '永久有效'}
          </Text>
        </div>
        <Button
          type="primary"
          icon={<DownloadOutlined />}
          onClick={() => {
            location.href = zipUrl;
          }}
        >
          打包下载
        </Button>
      </div>
      <Breadcrumb
        style={{ margin: '2px 0 14px' }}
        items={crumbs.map((c, i) => ({
          title:
            i === crumbs.length - 1 ? (
              <span>{c.name}</span>
            ) : (
              <a onClick={() => onNavigate(pathSegs.slice(0, c.depth))}>{c.name}</a>
            ),
        }))}
      />
      <Table
        rowKey={(r) => r.name}
        size="middle"
        columns={columns}
        dataSource={items}
        pagination={false}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="这个文件夹是空的" /> }}
      />
      {preview && <FilePreview {...preview} onClose={() => setPreview(null)} />}
    </Card>
  );
}

/* ---------------- 挂载 ---------------- */

function ShareApp() {
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
        <ShareContent mode={mode} onToggleMode={toggleMode} />
      </AntApp>
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('share-root')).render(<ShareApp />);
