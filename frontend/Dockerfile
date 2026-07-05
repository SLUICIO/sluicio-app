# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for the Sluicio frontend. Stage 1 builds the Vite
# bundle; stage 2 serves the static dist/ on nginx with SPA fallback.
#
# Build context is the frontend/ directory (not the repo root):
#
#   docker build -f frontend/Dockerfile -t sluicio/frontend ./frontend
#
# API requests (/api) are NOT proxied here — the outer reverse proxy
# (Caddy in deploy/server/) routes /api to cell-api. This image only
# serves the static SPA.

FROM node:20-bookworm-slim AS build

WORKDIR /app

# Install deps against the lockfile first for a cacheable layer.
COPY package.json package-lock.json ./
RUN npm ci

# Then build. `npm run build` runs `tsc --noEmit && vite build`.
# APP_VERSION (git-derived, scripts/version.sh) is baked into the bundle
# as __APP_VERSION__ — the footer version. Defaults to package.json when
# a bare `docker build` doesn't pass it.
ARG APP_VERSION=0.0.0-dev
ENV APP_VERSION=${APP_VERSION}
COPY . .
RUN npm run build

# ── Runtime image ──────────────────────────────────────────────────────
FROM nginx:1.27-alpine

# SPA routing: unknown paths fall back to index.html (React Router).
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
