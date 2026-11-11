import { fileURLToPath, URL } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

// Local Lid embeds web/dist with go:embed and serves it from one binary,
// so the build is kept as exactly one self-contained file: JS, CSS and
// datum-ui's font files all inlined (viteSingleFile + an unbounded
// assetsInlineLimit). The shell carries no secrets, so inlining is safe.
export default defineConfig({
  plugins: [tailwindcss(), react(), viteSingleFile()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    emptyOutDir: true,
    assetsInlineLimit: Number.MAX_SAFE_INTEGER,
    chunkSizeWarningLimit: 4096,
  },
  server: {
    // `bun run dev` against `local-lid serve` on its default port.
    proxy: { '/api': 'http://127.0.0.1:47790' },
  },
});
