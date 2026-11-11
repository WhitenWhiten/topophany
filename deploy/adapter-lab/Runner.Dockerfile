FROM tailscale/tailscale:v1.90.9 AS tailscale
FROM metacubex/mihomo:v1.19.33 AS mihomo
FROM cloudflare/cloudflared:2026.10.5 AS cloudflared
FROM henrygd/beszel-agent:0.21.0 AS beszel
FROM python:3.13-alpine
RUN apk add --no-cache iproute2 wireguard-tools iputils util-linux
COPY --from=tailscale /usr/local/bin/tailscale /usr/local/bin/tailscale
COPY --from=tailscale /usr/local/bin/tailscaled /usr/local/bin/tailscaled
COPY --from=mihomo /mihomo /usr/local/bin/mihomo
COPY --from=cloudflared /usr/local/bin/cloudflared /usr/local/bin/cloudflared
COPY --from=beszel /agent /usr/local/bin/beszel-agent
CMD ["sleep", "infinity"]
