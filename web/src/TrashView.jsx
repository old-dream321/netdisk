import React, { useCallback, useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Empty, Popconfirm, Space, Table, Tooltip, Typography } from 'antd';
import { ClearOutlined, DeleteOutlined, UndoOutlined } from '@ant-design/icons';
import { api, fmtSize, fmtTime } from './api';
import { FileTypeIcon } from './fileMeta';

const { Text } = Typography;

export default function TrashView({ refreshToken, onQuotaChange }) {
  const { message } = AntApp.useApp();
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.listTrash();
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

  const restore = async (r) => {
    try {
      const res = await api.restore(r.id);
      message.success(`已恢复 ${res.restored} 项`);
      load();
    } catch (e) {
      message.error(e.message);
    }
  };

  const purge = async (r) => {
    try {
      await api.purge(r.id);
      message.success('已彻底删除');
      load();
      if (onQuotaChange) onQuotaChange();
    } catch (e) {
      message.error(e.message);
    }
  };

  const emptyTrash = async () => {
    let done = 0;
    let failed = 0;
    for (const it of items) {
      try {
        await api.purge(it.id);
        done++;
      } catch (e) {
        // 目录被级联删除后，它的子项再删会 404，直接跳过
        if (e.status !== 404) failed++;
      }
    }
    if (done) message.success(`已彻底删除 ${done} 项`);
    if (failed) message.error(`${failed} 项删除失败`);
    load();
    if (onQuotaChange) onQuotaChange();
  };

  const columns = [
    {
      title: '名称',
      render: (_, r) => (
        <div className="cell-name">
          <FileTypeIcon item={r} />
          <span className="cell-name-text">{r.name}</span>
        </div>
      ),
    },
    {
      title: '大小',
      width: 130,
      render: (_, r) => <Text type="secondary">{r.type === 2 ? '—' : fmtSize(r.size)}</Text>,
    },
    {
      title: '删除时间',
      width: 200,
      render: (_, r) => <Text type="secondary">{fmtTime(r.deleted_at)}</Text>,
    },
    {
      title: '操作',
      width: 190,
      align: 'right',
      render: (_, r) => (
        <Space size={0}>
          <Tooltip title="恢复">
            <Button type="text" icon={<UndoOutlined />} onClick={() => restore(r)} />
          </Tooltip>
          <Popconfirm
            title="彻底删除"
            description="删除后无法恢复，文件将释放占用的空间。"
            okText="彻底删除"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => purge(r)}
          >
            <Button type="text" danger icon={<DeleteOutlined />} title="彻底删除" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div className="files-view">
      <div className="files-toolbar">
        <Text type="secondary">回收站中的内容仍然占用存储空间</Text>
        <Popconfirm
          title="清空回收站"
          description={`将彻底删除全部 ${items.length} 项，且无法恢复。`}
          okText="清空"
          okButtonProps={{ danger: true }}
          cancelText="取消"
          onConfirm={emptyTrash}
          disabled={!items.length}
        >
          <Button danger icon={<ClearOutlined />} disabled={!items.length}>
            清空回收站
          </Button>
        </Popconfirm>
      </div>
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
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="回收站是空的，删除的文件会先放在这里" />
            ),
          }}
        />
        {items.length > 0 && <div className="table-foot">共 {items.length} 项</div>}
      </Card>
    </div>
  );
}
