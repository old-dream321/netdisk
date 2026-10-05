// 文件类型图标：彩色圆角底 + 线性图标，按文件扩展名区分
import {
  FolderFilled,
  FileImageOutlined,
  VideoCameraOutlined,
  CustomerServiceOutlined,
  FileZipOutlined,
  FileExcelOutlined,
  FilePptOutlined,
  FileTextOutlined,
  CodeOutlined,
  FileOutlined,
} from '@ant-design/icons';
import { fileKind } from './api';

const META = {
  folder: { icon: <FolderFilled />, color: '#6366f1', bg: 'rgba(99, 102, 241, .12)' },
  image: { icon: <FileImageOutlined />, color: '#ec4899', bg: 'rgba(236, 72, 153, .12)' },
  video: { icon: <VideoCameraOutlined />, color: '#f59e0b', bg: 'rgba(245, 158, 11, .12)' },
  audio: { icon: <CustomerServiceOutlined />, color: '#8b5cf6', bg: 'rgba(139, 92, 246, .12)' },
  archive: { icon: <FileZipOutlined />, color: '#64748b', bg: 'rgba(100, 116, 139, .14)' },
  sheet: { icon: <FileExcelOutlined />, color: '#10b981', bg: 'rgba(16, 185, 129, .12)' },
  ppt: { icon: <FilePptOutlined />, color: '#f97316', bg: 'rgba(249, 115, 22, .12)' },
  doc: { icon: <FileTextOutlined />, color: '#3b82f6', bg: 'rgba(59, 130, 246, .12)' },
  code: { icon: <CodeOutlined />, color: '#0ea5e9', bg: 'rgba(14, 165, 233, .12)' },
  file: { icon: <FileOutlined />, color: '#64748b', bg: 'rgba(100, 116, 139, .12)' },
};

export function FileTypeIcon({ item, size = 38 }) {
  const meta = META[fileKind(item)] || META.file;
  return (
    <span
      className="file-type-icon"
      style={{
        width: size,
        height: size,
        fontSize: Math.round(size * 0.45),
        color: meta.color,
        background: meta.bg,
      }}
    >
      {meta.icon}
    </span>
  );
}

export function kindColor(item) {
  return (META[fileKind(item)] || META.file).color;
}
