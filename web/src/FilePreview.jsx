// 文件在线预览弹窗：图片 / 视频 / 音频 / 文本
import React, { useEffect, useState } from 'react';
import { App as AntApp, Button, Modal, Spin } from 'antd';
import { DownloadOutlined } from '@ant-design/icons';
import { download, fmtSize } from './api';
import { FileTypeIcon } from './fileMeta';

const IMG = /\.(png|jpe?g|gif|webp|svg|bmp|ico|avif)$/i;
const VID = /\.(mp4|webm|mov|m4v)$/i;
const AUD = /\.(mp3|wav|flac|aac|ogg|m4a)$/i;
const TXT = /\.(txt|md|markdown|json|js|mjs|cjs|ts|tsx|jsx|py|go|java|c|h|cpp|hpp|cs|rs|rb|php|css|scss|html|xml|yaml|yml|toml|ini|conf|log|csv|sh|sql|vue)$/i;

const TEXT_MAX = 1024 * 1024; // 文本预览大小上限：1MB

/** 按文件名/大小判断可预览类型：'image' | 'video' | 'audio' | 'text' | null */
export function kindByName(name, size) {
  const n = name || '';
  if (IMG.test(n)) return 'image';
  if (VID.test(n)) return 'video';
  if (AUD.test(n)) return 'audio';
  if (TXT.test(n) && Number(size || 0) <= TEXT_MAX) return 'text';
  return null;
}

/** 主应用文件对象 → 预览类型（目录不可预览） */
export function previewKind(item) {
  if (!item || item.type !== 1) return null;
  return kindByName(item.name, item.size);
}

/** 通用预览弹窗：主应用传 /api/files/:id/download，分享页传分享下载链接 */
export default function FilePreview({ name, size, kind, url, onClose }) {
  const { message } = AntApp.useApp();
  const [text, setText] = useState(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (kind !== 'text') return undefined;
    let aborted = false;
    setLoading(true);
    setText(null);
    fetch(url, { credentials: 'same-origin' })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error('HTTP ' + r.status))))
      .then((t) => {
        if (!aborted) setText(t);
      })
      .catch(() => {
        if (!aborted) message.error('读取文件内容失败');
      })
      .finally(() => {
        if (!aborted) setLoading(false);
      });
    return () => {
      aborted = true;
    };
  }, [kind, url, message]);

  return (
    <Modal
      open
      centered
      width={860}
      onCancel={onClose}
      title={
        <span className="preview-title">
          <FileTypeIcon item={{ name, type: 1 }} size={26} />
          <span className="preview-title-name">{name}</span>
          <span className="preview-title-size">{fmtSize(size)}</span>
        </span>
      }
      footer={[
        <Button key="download" type="primary" icon={<DownloadOutlined />} onClick={() => download(url)}>
          下载文件
        </Button>,
        <Button key="close" onClick={onClose}>
          关闭
        </Button>,
      ]}
    >
      <div className="preview-body">
        {kind === 'image' && <img src={url} alt={name} />}
        {kind === 'video' && <video src={url} controls preload="metadata" />}
        {kind === 'audio' && <audio src={url} controls />}
        {kind === 'text' && (
          <Spin spinning={loading}>
            <pre className="preview-text">{text ?? ''}</pre>
          </Spin>
        )}
        {!kind && <div className="preview-unsupported">该类型暂不支持在线预览，请下载后查看</div>}
      </div>
    </Modal>
  );
}
