# Stage 1: Build frontend
# Node 24 LTS matches the frontend runtime contract. This version tag is mutable;
# a verified registry digest still needs to be recorded before release.
FROM node:24-alpine3.22 AS builder
WORKDIR /app
COPY package*.json ./
RUN npm config set registry https://registry.npmmirror.com && npm ci
COPY . .
RUN npm run build

# Stage 2: Serve with nginx
FROM nginx:1.27.4-alpine
RUN apk add --no-cache openssl
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
EXPOSE 80 443
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
