import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 开发时：前端开发服务器在 127.0.0.1:5173，接口代理到 Go 服务（127.0.0.1:7788）。
// Go 服务要用 -dev-ui http://127.0.0.1:5173 启动，才会放行这个地址的 Host 和 Origin。
const api = "http://127.0.0.1:7788";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: api },
      "/login": { target: api },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // 本机工具，不在意首屏体积；代码编辑器已按需加载
    chunkSizeWarningLimit: 1000,
  },
});
