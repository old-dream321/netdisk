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
  Typography,
} from 'antd';
import { ClockCircleOutlined, CloudFilled, CopyOutlined, DownloadOutlined } from '@ant-design/icons';
import zhCN from 'antd/locale/zh_CN';
import { copyText, fmtSize, fmtTime } from './api';
import { FileTypeIcon } from './fileMeta';
import 'antd/dist/reset.css';
import './styles.css';

const { Text, Title } = Typography;

/* ---------------- 内容 ---------------- */

function ShareContent() {
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
  const ext = (data.name || '').toLowerCase();
  const canPreview = Number(data.size) <= 80 * 1024 * 1024;
  const isImg = /\.(png|jpe?g|gif|webp|svg|bmp|avif|ico)$/.test(ext);
  const isVid = /\.(mp4|webm|mov|m4v)$/.test(ext);
  const isAud = /\.(mp3|wav|flac|aac|ogg|m4a)$/.test(ext);

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
      {canPreview && isImg && (
        <div className="share-preview">
          <Image src={data.download_url} style={{ maxHeight: 420, borderRadius: 12 }} />
        </div>
      )}
      {canPreview && isVid && (
        <div className="share-preview">
          <video controls preload="metadata" src={data.download_url} />
        </div>
      )}
      {canPreview && isAud && (
        <div className="share-preview">
          <audio controls preload="metadata" src={data.download_url} />
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
  const crumbs = [{ name: rootName || '分享内容', depth: 0 }].concat(
    pathSegs.map((s, i) => ({ name: s, depth: i + 1 })),
  );

  const columns = [
    {
      title: '名称',
      render: (_, r) => (
        <Space>
          <FileTypeIcon item={{ name: r.name, type: r.type === 'dir' ? 2 : 1 }} size={34} />
          {r.type === 'dir' ? (
            <a onClick={() => onNavigate(pathSegs.concat([r.name]))}>{r.name}</a>
          ) : (
            <Text>{r.name}</Text>
          )}
        </Space>
      ),
    },
    {
      title: '大小',
      width: 120,
      render: (_, r) => <Text type="secondary">{r.type === 'dir' ? '文件夹' : fmtSize(r.size)}</Text>,
    },
    {
      title: '',
      width: 100,
      align: 'right',
      render: (_, r) => (r.type === 'dir' ? null : <a href={r.download_url}>下载</a>),
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
    </Card>
  );
}

/* ---------------- 挂载 ---------------- */

ReactDOM.createRoot(document.getElementById('share-root')).render(
  <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: '#6366f1', borderRadius: 10 } }}>
    <AntApp>
      <ShareContent />
    </AntApp>
  </ConfigProvider>,
);
