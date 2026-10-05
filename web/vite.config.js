import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// 构建产物输出到 dist/，由 Go 后端托管（router.go 的 frontendDir 指向 web/dist）。
// 多页面入口：index.html（主应用） + share.html（公开分享页），
// 后者配合后端 /s/:token 的浏览器 302 跳转使用。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    rollupOptions: {
      input: {
        main: 'index.html',
        share: 'share.html',
      },
    },
  },
  server: {
    port: 5173,
    // 开发模式下把后端接口代理过来（先用 go run . 启动后端）
    proxy: {
      '/api': 'http://localhost:8080',
      '/s': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
});
