# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS frontend
WORKDIR /src

COPY package.json package-lock.json ./
COPY web/package.json web/package.json
# This needs package-lock.json to carry every platform's optional native
# bindings (`@rolldown/binding-*`, `@tailwindcss/oxide-*`, `lightningcss-*` —
# Vite 8 and Tailwind 4 both ship Rust binaries). A clean-slate `npm install`
# records all of them; an incremental one over an existing node_modules prunes
# them to the host platform (npm/cli#4828) and, because they are `optional`,
# `npm ci` here still exits 0 — the build fails later with rolldown's
# "Cannot find native binding". If that happens, regenerate the lock with
# `rm -rf node_modules web/node_modules package-lock.json && npm install`.
RUN npm ci --no-audit --no-fund

COPY web web
RUN npm run build

FROM golang:1.27.1-alpine AS backend
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd cmd
COPY internal internal
COPY migrations migrations
COPY --from=frontend /src/internal/dashboard/dist internal/dashboard/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/faultline ./cmd/faultline

FROM alpine:3.21
RUN addgroup -S faultline && adduser -S -G faultline faultline \
    && mkdir -p /var/lib/faultline/spill \
    && chown -R faultline:faultline /var/lib/faultline

COPY --from=backend /out/faultline /usr/local/bin/faultline

USER faultline
EXPOSE 8080
VOLUME ["/var/lib/faultline/spill"]
ENTRYPOINT ["/usr/local/bin/faultline"]
