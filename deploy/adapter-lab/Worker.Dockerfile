# Build the unmodified, pinned CF-Server-Monitor Worker in a real workerd
# runtime with local D1 and Durable Objects. No Cloudflare account is used.
FROM node:22-bookworm-slim
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN ./node_modules/.bin/esbuild src/index.js --bundle --format=esm \
    --platform=neutral --main-fields=module,main --external:cloudflare:* \
    --external:node:* --outfile=worker.mjs
CMD ["node", "/lab/worker.mjs"]
