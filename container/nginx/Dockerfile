# Stage 1: Build React App
FROM node:20-alpine as builder
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: Serve with Nginx
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY container/nginx/nginx.conf /etc/nginx/conf.d/default.conf
