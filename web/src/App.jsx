import React, { useEffect, useState } from 'react';
import {
  App as AntApp,
  Avatar,
  Button,
  Card,
  Dropdown,
  Form,
  Input,
  Layout,
  Menu,
  Progress,
  Segmented,
  Space,
  Spin,
  Typography,
} from 'antd';
import {
  CloudFilled,
  DeleteOutlined,
  DownOutlined,
  FolderOutlined,
  LinkOutlined,
  LockOutlined,
  LogoutOutlined,
  MailOutlined,
  ReloadOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { api, fmtSize } from './api.js';
import FilesView from './FilesView.jsx';
import SharesView from './SharesView.jsx';
import TrashView from './TrashView.jsx';

const { Sider, Header, Content } = Layout;
const { Text } = Typography;

export default function App() {
  const [user, setUser] = useState(undefined); // undefined=检查中, null=未登录

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null));
  }, []);

  if (user === undefined) {
    return (
      <div className="boot">
        <Spin size="large" />
      </div>
    );
  }
  if (!user) {
    return <AuthScreen onSuccess={setUser} />;
  }
  return <Shell user={user} onUserChange={setUser} />;
}

/* ---------------- 登录 / 注册 ---------------- */

function AuthScreen({ onSuccess }) {
  const { message } = AntApp.useApp();
  const [mode, setMode] = useState('login');
  const [loading, setLoading] = useState(false);
  const [form] = Form.useForm();

  const switchMode = (m) => {
    setMode(m);
    form.resetFields();
  };

  const submit = async (values) => {
    setLoading(true);
    try {
      if (mode === 'register') {
        await api.register(values.username, values.password, values.email);
      }
      await api.login(values.username, values.password);
      const me = await api.me();
      onSuccess(me);
      message.success(`欢迎回来，${me.username}`);
    } catch (e) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="auth-blob auth-blob-a" />
      <div className="auth-blob auth-blob-b" />
      <Card className="auth-card">
        <div className="auth-logo">
          <CloudFilled />
        </div>
        <h1 className="auth-title">NetDisk 云盘</h1>
        <p className="auth-sub">安全、简单地管理你的文件与分享</p>
        <Segmented
          block
          value={mode}
          onChange={switchMode}
          options={[
            { label: '登录', value: 'login' },
            { label: '注册', value: 'register' },
          ]}
        />
        <Form form={form} layout="vertical" onFinish={submit} requiredMark={false} style={{ marginTop: 22 }}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input prefix={<UserOutlined />} placeholder="请输入用户名" maxLength={64} autoComplete="username" />
          </Form.Item>
          {mode === 'register' && (
            <Form.Item
              name="email"
              label="邮箱"
              rules={[
                { required: true, message: '请输入邮箱' },
                { type: 'email', message: '邮箱格式不正确' },
              ]}
            >
              <Input prefix={<MailOutlined />} placeholder="you@example.com" autoComplete="email" />
            </Form.Item>
          )}
          <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="请输入密码"
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
            />
          </Form.Item>
          <Button type="primary" htmlType="submit" block size="large" loading={loading}>
            {mode === 'login' ? '登录' : '注册并登录'}
          </Button>
        </Form>
        <p className="auth-foot">新用户默认赠送 1 GB 空间</p>
      </Card>
    </div>
  );
}

/* ---------------- 主界面 ---------------- */

const VIEW_META = {
  files: { label: '我的文件', icon: <FolderOutlined /> },
  shares: { label: '我的分享', icon: <LinkOutlined /> },
  trash: { label: '回收站', icon: <DeleteOutlined /> },
};

function Shell({ user, onUserChange }) {
  const [view, setView] = useState('files');
  const [tick, setTick] = useState(0);

  const quota = Number(user.quota) || 0;
  const used = Number(user.used) || 0;
  const unlimited = quota <= 0;
  const percent = unlimited ? 0 : Math.min(100, Math.round((used / quota) * 1000) / 10);

  const refreshUser = () => {
    api.me().then(onUserChange).catch(() => {});
  };

  const logout = async () => {
    try {
      await api.logout();
    } catch {
      /* 忽略 */
    }
    onUserChange(null);
  };

  return (
    <Layout className="app-shell">
      <Sider width={236} collapsedWidth={64} breakpoint="lg" theme="light" className="app-sider">
        <div className="app-logo">
          <span className="app-logo-mark">
            <CloudFilled />
          </span>
          <span className="app-logo-text">
            <b>NetDisk</b>
            <small>个人云盘</small>
          </span>
        </div>
        <Menu
          className="app-menu"
          mode="inline"
          selectedKeys={[view]}
          items={Object.entries(VIEW_META).map(([key, m]) => ({ key, icon: m.icon, label: m.label }))}
          onClick={(e) => setView(e.key)}
        />
        <div className="app-sider-bottom">
          <div className="storage-card">
            <div className="storage-head">
              <Text type="secondary">存储空间</Text>
              <b>{unlimited ? '不限量' : percent + '%'}</b>
            </div>
            <Progress percent={percent} showInfo={false} strokeColor="#6366f1" strokeWidth={8} />
            <Text type="secondary" className="storage-text">
              {unlimited ? `已用 ${fmtSize(used)}` : `${fmtSize(used)} / ${fmtSize(quota)}`}
            </Text>
          </div>
        </div>
      </Sider>
      <Layout>
        <Header className="app-header">
          <div className="header-title">{VIEW_META[view].label}</div>
          <Space size={12}>
            <Button icon={<ReloadOutlined />} onClick={() => setTick((t) => t + 1)}>
              刷新
            </Button>
            <Dropdown
              menu={{
                items: [
                  { key: 'profile', label: `${user.username}（${user.email}）`, disabled: true },
                  { type: 'divider' },
                  { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
                ],
                onClick: ({ key }) => {
                  if (key === 'logout') logout();
                },
              }}
            >
              <span className="user-chip">
                <Avatar size={30} style={{ background: '#6366f1' }}>
                  {(user.username || '?').slice(0, 1).toUpperCase()}
                </Avatar>
                <span className="user-chip-name">{user.username}</span>
                <DownOutlined style={{ fontSize: 10, color: '#999' }} />
              </span>
            </Dropdown>
          </Space>
        </Header>
        <Content className="app-content">
          {view === 'files' && <FilesView refreshToken={tick} onQuotaChange={refreshUser} />}
          {view === 'shares' && <SharesView refreshToken={tick} />}
          {view === 'trash' && <TrashView refreshToken={tick} onQuotaChange={refreshUser} />}
        </Content>
      </Layout>
    </Layout>
  );
}
