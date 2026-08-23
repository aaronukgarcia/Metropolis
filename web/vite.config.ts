import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // Dev convenience: WS + API traffic rides the Vite dev server so the
      // page is same-origin; point VITE_METROPOLIS_WS at the Go binary's
      // listen address to override.
      // Override the Go server address by editing this constant; kept
      // static so the build needs no Node type machinery.
      "/ws": {
        target: "ws://localhost:8080",
        ws: true,
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
  },
});
