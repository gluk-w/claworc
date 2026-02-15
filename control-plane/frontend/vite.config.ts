import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";
import path from "path";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: "autoUpdate",
      devOptions: {
        enabled: true,
      },
      manifest: {
        name: "Openclaw Orchestrator",
        short_name: "Claworc",
        description: "Kubernetes dashboard for managing OpenClaw instances",
        start_url: "/",
        display: "standalone",
        background_color: "#111827",
        theme_color: "#111827",
        icons: [
          {
            src: "/pwa_images/launchericon-72-72.png",
            sizes: "72x72",
            type: "image/png",
          },
          {
            src: "/pwa_images/launchericon-96-96.png",
            sizes: "96x96",
            type: "image/png",
          },
          {
            src: "/pwa_images/launchericon-144-144.png",
            sizes: "144x144",
            type: "image/png",
          },
          {
            src: "/pwa_images/launchericon-192-192.png",
            sizes: "192x192",
            type: "image/png",
          },
          {
            src: "/pwa_images/launchericon-512-512.png",
            sizes: "512x512",
            type: "image/png",
          },
          {
            src: "/pwa_images/launchericon-512-512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "maskable",
          },
        ],
      },
      workbox: {
        globPatterns: ["**/*.{js,css,html,ico,png,svg,woff2}"],
        navigateFallback: "/index.html",
        runtimeCaching: [
          {
            urlPattern: /^https?:\/\/.*\/api\//,
            handler: "NetworkOnly",
          },
        ],
      },
    }),
  ],
  optimizeDeps: {
    include: ["@novnc/novnc"],
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8000",
        changeOrigin: true,
        autoRewrite: true,
        ws: true,
      },
      "/health": {
        target: "http://127.0.0.1:8000",
        changeOrigin: true,
        autoRewrite: true,
      },
    },
  },
});
