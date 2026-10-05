import React, { useCallback, useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Empty, Popconfirm, Space, Table, Tag, Tooltip, Typography } from 'antd';
import { CopyOutlined, DeleteOutlined, ExportOutlined, LinkOutlined } from '@ant-design/icons';
import { api, copyText, fmtTime } from './api';

const { Text } = Typography;

export default function SharesView({ refreshToken }) {
  const { message } = AntApp.useApp();
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.listShares();
      setItems((data && data.items) || []);
    } catch (e) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    load();
  }, [refreshToken, load]);

  const copyLink = async (r) => {
    if (await copyText(location.origin + r.url)) message.success('链接已复制');
    else message.error('复制失败，请手动复制');
  };

  const revoke = async (r) => {
    try {
      await api.revokeShare(r.id);
      message.success('分享已取消');
      load();
    } catch (e) {
      message.error(e.message);
    }
  };

  const columns = [
    {
      title: '文件',
      render: (_, r) => (
        <Space>
          <span className="share-badge">
            <LinkOutlined />
          </span>
          <Text strong>{r.file_name || '（文件已删除）'}</Text>
        </Space>
      ),
    },
    {
      title: '状态',
      width: 140,
      render: (_, r) => {
        if (!r.available) return <Tag>文件已删除</Tag>;
        if (r.expired) return <Tag color="orange">已过期</Tag>;
        return <Tag color="green">有效</Tag>;
      },
    },
    {
      title: '有效期',
      width: 220,
      render: (_, r) => (
        <Text type="secondary">{r.expires_at ? fmtTime(r.expires_at) + ' 到期' : '永久有效'}</Text>
      ),
    },
    {
      title: '操作',
      width: 200,
      align: 'right',
      render: (_, r) => (
        <Space size={0}>
          <Tooltip title="复制链接">
            <Button type="text" icon={<CopyOutlined />} onClick={() => copyLink(r)} />
          </Tooltip>
          <Tooltip title="打开链接">
            <Button
              type="text"
              icon={<ExportOutlined />}
              onClick={() => window.open(location.origin + r.url, '_blank')}
            />
          </Tooltip>
          <Popconfirm
            title="取消分享"
            description="取消后链接立即失效。"
            okText="取消分享"
            okButtonProps={{ danger: true }}
            cancelText="再想想"
            onConfirm={() => revoke(r)}
          >
            <Button type="text" danger icon={<DeleteOutlined />} title="取消分享" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card className="table-card" styles={{ body: { padding: 0 } }}>
      <Table
        rowKey="id"
        size="middle"
        loading={loading}
        columns={columns}
        dataSource={items}
        pagination={false}
        locale={{
          emptyText: (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有创建分享，在文件列表里点「分享」即可生成链接" />
          ),
        }}
      />
      {items.length > 0 && <div className="table-foot">共 {items.length} 个分享</div>}
    </Card>
  );
}
