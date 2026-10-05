import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  App as AntApp,
  Breadcrumb,
  Button,
  Card,
  DatePicker,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Progress,
  Radio,
  Space,
  Spin,
  Table,
  Tooltip,
  Tree,
  Typography,
  Upload,
} from 'antd';
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FolderAddOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  RightOutlined,
  ShareAltOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { api, copyText, download, fmtSize, uploadFile } from './api';
import { FileTypeIcon } from './fileMeta';

const { Text } = Typography;

/* 构建"移动"用的目录树：目标是目录时，需要排除自身与所有子孙目录 */
function buildMoveTree(dirs, moving) {
  const banned = new Set([moving.id]);
  if (moving.type === 2) {
    let frontier = [moving.id];
    while (frontier.length) {
      const next = [];
      for (const d of dirs) {
        if (!banned.has(d.id) && frontier.includes(d.parent_id)) {
          banned.add(d.id);
          next.push(d.id);
        }
      }
      frontier = next;
    }
  }
  const byParent = new Map();
  for (const d of dirs) {
    if (banned.has(d.id)) continue;
    if (!byParent.has(d.parent_id)) byParent.set(d.parent_id, []);
    byParent.get(d.parent_id).push(d);
  }
  const make = (pid) =>
    (byParent.get(pid) || [])
      .sort((a, b) => a.name.localeCompare(b.name, 'zh'))
      .map((d) => {
        const children = make(d.id);
        return children.length
          ? { title: d.name, key: String(d.id), children }
          : { title: d.name, key: String(d.id) };
      });
  return [{ title: '根目录', key: '0', children: make(0) }];
}

export default function FilesView({ refreshToken, onQuotaChange }) {
  const { message } = AntApp.useApp();

  const [stack, setStack] = useState([{ id: 0, name: '我的文件' }]);
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);
  const [uploads, setUploads] = useState([]);
  const seqRef = useRef(0);
  const currentId = stack[stack.length - 1].id;

  // 新建文件夹
  const [mkdirOpen, setMkdirOpen] = useState(false);
  const [mkdirBusy, setMkdirBusy] = useState(false);
  const [mkdirForm] = Form.useForm();
  // 重命名
  const [renameTarget, setRenameTarget] = useState(null);
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameForm] = Form.useForm();
  // 移动
  const [moveTarget, setMoveTarget] = useState(null);
  const [moveBusy, setMoveBusy] = useState(false);
  const [moveLoading, setMoveLoading] = useState(false);
  const [moveTree, setMoveTree] = useState([]);
  const [moveKey, setMoveKey] = useState('0');
  // 分享
  const [shareTarget, setShareTarget] = useState(null);
  const [shareBusy, setShareBusy] = useState(false);
  const [sharePhase, setSharePhase] = useState('config');
  const [shareExp, setShareExp] = useState('7');
  const [shareDate, setShareDate] = useState(null);
  const [shareLink, setShareLink] = useState('');

  const load = useCallback(
    async (id) => {
      const seq = ++seqRef.current;
      setLoading(true);
      try {
        const data = await api.listFiles(id);
        if (seq === seqRef.current) setItems((data && data.items) || []);
      } catch (e) {
        if (seq === seqRef.current) message.error(e.message);
      } finally {
        if (seq === seqRef.current) setLoading(false);
      }
    },
    [message],
  );

  useEffect(() => {
    load(currentId);
  }, [currentId, refreshToken, load]);

  /* ---------------- 上传 ---------------- */

  const doUpload = useCallback(
    (file, parentId) => {
      const uid = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      setUploads((u) => [...u, { uid, name: file.name, percent: 0, status: 'uploading' }]);
      const patch = (p) => setUploads((u) => u.map((it) => (it.uid === uid ? { ...it, ...p } : it)));
      return uploadFile(file, parentId, { onProgress: (p) => patch({ percent: Math.round(p * 100) }) })
        .then(() => {
          patch({ status: 'done', percent: 100 });
          if (parentId === currentId) load(parentId);
          if (onQuotaChange) onQuotaChange();
          setTimeout(() => setUploads((u) => u.filter((it) => it.uid !== uid)), 3000);
        })
        .catch((e) => {
          patch({ status: 'error', error: e.message });
          message.error(`${file.name}：${e.message}`);
          setTimeout(() => setUploads((u) => u.filter((it) => it.uid !== uid)), 6000);
          throw e;
        });
    },
    [currentId, load, message, onQuotaChange],
  );

  const uploadProps = useMemo(
    () => ({
      multiple: true,
      showUploadList: false,
      customRequest: ({ file, onSuccess, onError }) => {
        doUpload(file, currentId)
          .then(() => onSuccess && onSuccess())
          .catch((e) => onError && onError(e));
      },
    }),
    [doUpload, currentId],
  );

  /* ---------------- 操作 ---------------- */

  const enterDir = (record) => setStack((s) => [...s, { id: record.id, name: record.name }]);
  const gotoCrumb = (index) => setStack((s) => s.slice(0, index + 1));

  const submitMkdir = async () => {
    let values;
    try {
      values = await mkdirForm.validateFields();
    } catch {
      return;
    }
    setMkdirBusy(true);
    try {
      await api.createDir(values.name.trim(), currentId);
      message.success('文件夹已创建');
      setMkdirOpen(false);
      load(currentId);
    } catch (e) {
      message.error(e.message);
    } finally {
      setMkdirBusy(false);
    }
  };

  const openRename = (record) => {
    renameForm.setFieldsValue({ name: record.name });
    setRenameTarget(record);
  };

  const submitRename = async () => {
    let values;
    try {
      values = await renameForm.validateFields();
    } catch {
      return;
    }
    const name = values.name.trim();
    if (name === renameTarget.name) {
      setRenameTarget(null);
      return;
    }
    setRenameBusy(true);
    try {
      await api.rename(renameTarget.id, name);
      message.success('已重命名');
      setRenameTarget(null);
      load(currentId);
    } catch (e) {
      message.error(e.message);
    } finally {
      setRenameBusy(false);
    }
  };

  const openMove = async (record) => {
    setMoveTarget(record);
    setMoveKey(String(record.parent_id));
    setMoveTree([]);
    setMoveLoading(true);
    try {
      const data = await api.listFiles(undefined); // 不带 parent_id：返回全部文件
      const dirs = ((data && data.items) || []).filter((x) => x.type === 2);
      setMoveTree(buildMoveTree(dirs, record));
    } catch (e) {
      message.error('加载目录失败：' + e.message);
    } finally {
      setMoveLoading(false);
    }
  };

  const submitMove = async () => {
    const target = Number(moveKey);
    if (target === moveTarget.parent_id) {
      setMoveTarget(null);
      return;
    }
    setMoveBusy(true);
    try {
      await api.move(moveTarget.id, target);
      message.success('已移动');
      setMoveTarget(null);
      load(currentId);
    } catch (e) {
      message.error(e.message);
    } finally {
      setMoveBusy(false);
    }
  };

  const openShare = (record) => {
    setShareTarget(record);
    setSharePhase('config');
    setShareExp('7');
    setShareDate(null);
    setShareLink('');
  };

  const submitShare = async () => {
    let exp;
    if (shareExp === 'custom') {
      if (!shareDate) {
        message.warning('请选择到期日期');
        return;
      }
      exp = shareDate.endOf('day').toISOString();
    } else if (shareExp !== 'forever') {
      exp = dayjs().add(Number(shareExp), 'day').toISOString();
    }
    setShareBusy(true);
    try {
      const data = await api.createShare(shareTarget.id, exp);
      setShareLink(location.origin + data.url);
      setSharePhase('result');
    } catch (e) {
      message.error(e.message);
    } finally {
      setShareBusy(false);
    }
  };

  const copyShareLink = async () => {
    if (await copyText(shareLink)) message.success('链接已复制');
    else message.error('复制失败，请手动复制');
  };

  const removeItem = async (record) => {
    try {
      await api.remove(record.id);
      message.success('已移入回收站');
      load(currentId);
    } catch (e) {
      message.error(e.message);
    }
  };

  /* ---------------- 渲染 ---------------- */

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (_, record) => (
        <div className="cell-name">
          <FileTypeIcon item={record} />
          {record.type === 2 ? (
            <a className="cell-name-link" onClick={() => enterDir(record)}>
              {record.name}
              <RightOutlined className="cell-name-arrow" />
            </a>
          ) : (
            <span className="cell-name-text">{record.name}</span>
          )}
        </div>
      ),
    },
    {
      title: '大小',
      width: 130,
      render: (_, record) => (
        <Text type="secondary">{record.type === 2 ? '—' : fmtSize(record.size)}</Text>
      ),
    },
    {
      title: '操作',
      width: 230,
      align: 'right',
      render: (_, record) => (
        <Space size={0} onClick={(e) => e.stopPropagation()}>
          <Tooltip title={record.type === 2 ? '打包下载' : '下载'}>
            <Button
              type="text"
              icon={<DownloadOutlined />}
              onClick={() =>
                download(record.type === 2 ? `/api/files/${record.id}/zip` : `/api/files/${record.id}/download`)
              }
            />
          </Tooltip>
          <Tooltip title="分享">
            <Button type="text" icon={<ShareAltOutlined />} onClick={() => openShare(record)} />
          </Tooltip>
          <Tooltip title="重命名">
            <Button type="text" icon={<EditOutlined />} onClick={() => openRename(record)} />
          </Tooltip>
          <Tooltip title="移动">
            <Button type="text" icon={<FolderOpenOutlined />} onClick={() => openMove(record)} />
          </Tooltip>
          <Popconfirm
            title="移入回收站"
            description={`确定把「${record.name}」移入回收站吗？`}
            okText="移入回收站"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => removeItem(record)}
          >
            <Button type="text" danger icon={<DeleteOutlined />} title="移入回收站" />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div className="files-view">
      <div className="files-toolbar">
        <Breadcrumb
          items={stack.map((s, i) => ({
            title:
              i === stack.length - 1 ? (
                <span className="crumb-current">{s.name}</span>
              ) : (
                <a onClick={() => gotoCrumb(i)}>{s.name}</a>
              ),
          }))}
        />
        <Space>
          <Button
            icon={<FolderAddOutlined />}
            onClick={() => {
              mkdirForm.resetFields();
              setMkdirOpen(true);
            }}
          >
            新建文件夹
          </Button>
          <Upload {...uploadProps}>
            <Button type="primary" icon={<UploadOutlined />}>
              上传文件
            </Button>
          </Upload>
        </Space>
      </div>

      <Upload.Dragger {...uploadProps} openFileDialogOnClick={false} className="drop-area">
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
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={
                    <span>
                      这里还没有文件
                      <br />
                      <Text type="secondary">把文件拖进来，或点击「上传文件」</Text>
                    </span>
                  }
                />
              ),
            }}
          />
          {items.length > 0 && <div className="table-foot">共 {items.length} 项</div>}
        </Card>
      </Upload.Dragger>

      {uploads.length > 0 && (
        <Card size="small" className="upload-panel" title="上传任务">
          <div className="upload-list">
            {uploads.map((u) => (
              <div key={u.uid} className="upload-item">
                <div className="upload-line">
                  <Text ellipsis className="upload-name">
                    {u.name}
                  </Text>
                  {u.status === 'uploading' && <Text type="secondary">{u.percent}%</Text>}
                  {u.status === 'done' && <Text type="success">完成</Text>}
                  {u.status === 'error' && <Text type="danger">失败</Text>}
                </div>
                <Progress
                  percent={u.percent}
                  size="small"
                  showInfo={false}
                  status={u.status === 'done' ? 'success' : u.status === 'error' ? 'exception' : 'active'}
                />
              </div>
            ))}
          </div>
        </Card>
      )}

      <Modal
        title="新建文件夹"
        open={mkdirOpen}
        onCancel={() => setMkdirOpen(false)}
        onOk={submitMkdir}
        confirmLoading={mkdirBusy}
        okText="创建"
        cancelText="取消"
      >
        <Form form={mkdirForm} layout="vertical" requiredMark={false}>
          <Form.Item
            name="name"
            label="文件夹名称"
            rules={[{ required: true, whitespace: true, message: '请输入文件夹名称' }]}
          >
            <Input placeholder="例如：学习资料" maxLength={255} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="重命名"
        open={!!renameTarget}
        onCancel={() => setRenameTarget(null)}
        onOk={submitRename}
        confirmLoading={renameBusy}
        okText="保存"
        cancelText="取消"
      >
        <Form form={renameForm} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label="新名称" rules={[{ required: true, whitespace: true, message: '请输入名称' }]}>
            <Input maxLength={255} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={moveTarget ? `移动「${moveTarget.name}」` : '移动'}
        open={!!moveTarget}
        onCancel={() => setMoveTarget(null)}
        onOk={submitMove}
        confirmLoading={moveBusy}
        okText="移动到此处"
        cancelText="取消"
        width={520}
      >
        {moveLoading ? (
          <div className="modal-loading">
            <Spin />
          </div>
        ) : (
          <Tree
            treeData={moveTree}
            defaultExpandAll
            blockNode
            showIcon
            icon={<FolderOutlined />}
            selectedKeys={[moveKey]}
            onSelect={(keys) => {
              if (keys.length) setMoveKey(String(keys[0]));
            }}
          />
        )}
        <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
          不能移动到自身或它的子目录里
        </Text>
      </Modal>

      <Modal
        title={shareTarget ? `分享「${shareTarget.name}」` : '分享'}
        open={!!shareTarget}
        onCancel={() => setShareTarget(null)}
        footer={
          sharePhase === 'config'
            ? [
                <Button key="cancel" onClick={() => setShareTarget(null)}>
                  取消
                </Button>,
                <Button key="ok" type="primary" loading={shareBusy} onClick={submitShare}>
                  创建分享链接
                </Button>,
              ]
            : [
                <Button key="done" onClick={() => setShareTarget(null)}>
                  完成
                </Button>,
                <Button key="open" type="primary" onClick={() => window.open(shareLink, '_blank')}>
                  打开链接
                </Button>,
              ]
        }
      >
        {sharePhase === 'config' ? (
          <>
            <Text type="secondary">创建后，任何拿到链接的人都可以访问，无需登录。</Text>
            <div style={{ marginTop: 16 }}>
              <Radio.Group value={shareExp} onChange={(e) => setShareExp(e.target.value)}>
                <Space direction="vertical">
                  <Radio value="forever">永久有效</Radio>
                  <Radio value="7">7 天</Radio>
                  <Radio value="30">30 天</Radio>
                  <Radio value="custom">自定义到期日期</Radio>
                </Space>
              </Radio.Group>
              {shareExp === 'custom' && (
                <DatePicker
                  style={{ marginTop: 12, width: '100%' }}
                  value={shareDate}
                  onChange={setShareDate}
                  disabledDate={(d) => d && d.isBefore(dayjs().startOf('day'))}
                  placeholder="选择到期日期"
                />
              )}
            </div>
          </>
        ) : (
          <>
            <Input value={shareLink} readOnly addonAfter={<a onClick={copyShareLink}>复制</a>} />
            <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
              可以在「我的分享」中随时取消这个链接。
            </Text>
          </>
        )}
      </Modal>
    </div>
  );
}
