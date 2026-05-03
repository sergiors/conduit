FROM node:20-alpine AS build
COPY . /app
WORKDIR /app
RUN npm ci
RUN npm run build

FROM caddy:2-alpine
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/build/client /usr/share/caddy
EXPOSE 80
