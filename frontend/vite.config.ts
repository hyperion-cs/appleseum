import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { routePaths, routeRedirects } from './src/app/routes.ts'
import { productDisplayName, productTagline, defaultHttpPort } from './src/api/types.ts'
const defaultDevBackendPort = 18_080

export default defineConfig({
  plugins: [
    vue(),
    {
      name: 'product-title',

      transformIndexHtml: (html) =>
        html
          .replaceAll('__PRODUCT_NAME__', productDisplayName)
          .replaceAll('__PRODUCT_TAGLINE__', productTagline),
    },
    {
      name: 'route-entry-pages',
      enforce: 'post',

      generateBundle(_, bundle) {
        const index = bundle['index.html']
        if (!index || index.type !== 'asset') throw new Error('Missing frontend entry page')
        // Extensionless HTML entries let the existing file server handle direct route requests.
        for (const path of [...Object.values(routePaths), ...Object.keys(routeRedirects)]) {
          this.emitFile({ type: 'asset', fileName: path.slice(1), source: index.source })
        }
      },
    },
  ],
  build: { target: 'safari15' },
  server: {
    host: '0.0.0.0',
    port: defaultHttpPort,
    strictPort: true,
    proxy: { '/api': { target: `http://127.0.0.1:${defaultDevBackendPort}`, changeOrigin: false } },
  },
})
