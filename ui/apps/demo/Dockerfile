FROM node:22-alpine AS build
WORKDIR /workspace
COPY package.json package-lock.json* ./
COPY packages ./packages
COPY apps ./apps
RUN npm ci
RUN npm run build --workspace @calypr/loom-ui
RUN npm run build --workspace @calypr/loom-demo

FROM nginx:1.27-alpine
COPY apps/demo/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /workspace/apps/demo/dist /usr/share/nginx/html
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
