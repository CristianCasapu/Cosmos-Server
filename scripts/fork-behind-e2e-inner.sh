#!/bin/sh
# Fork addition (plan 44): the end-to-end proof of apps behind the proxy, run
# INSIDE a docker:29-dind that holds a test Cosmos (HTTPS 2053, self-signed,
# hostname server.example.org, admin / $PASSWORD) running outside Docker, as
# on a bare-metal server; scripts/fork-behind-e2e.sh sets that up. Every app
# runs on the --internal network "behind" (its gateway 172.30.0.1 is where
# Cosmos connects from) under two URLs: <app>.server.local (home) and
# <app>.example.org (domain; also asked through a stand-in for Cloudflare in
# front, which keeps Host with its port and adds the forwarded headers).
# For each app: the URL check says what the app objects to and proposes the
# repair; the repair is applied the way the panel applies it (the URL's
# setting through /api/url-check/apply, a variable through the container
# edit, a command in the container); the check says it now opens; then what
# a browser does is done through Cosmos (page, sign-in, a POST, WebSocket).
#   APPS="qbittorrent qbt-hotio qbt-official codeserver sonarr radarr transmission homeassistant nextcloud paperless forgejo vite webpack"
# Exit 1 when a step failed. Never against the owner's server.
set -u
PASSWORD=${PASSWORD:-Test-Passw0rd!x}
APPS=${APPS:-qbittorrent qbt-hotio qbt-official codeserver sonarr radarr transmission homeassistant nextcloud paperless forgejo vite webpack}
B=https://server.example.org:2053
J=/tmp/e2e-jar
fails=0
say() { echo "$*"; }
ok() { if [ "$1" = "$2" ]; then say "  ok   $3"; else say "  FAIL $3 (got '$1', want '$2')"; fails=$((fails+1)); fi; }
has() { case "$1" in *"$2"*) say "  ok   $3";; *) say "  FAIL $3 (got '$1', want it to contain '$2')"; fails=$((fails+1));; esac; }
api() { m=$1; p=$2; shift 2; curl -sk --resolve server.example.org:2053:127.0.0.1 -c $J -b $J -X "$m" -H 'Content-Type: application/json' "$@" "$B/cosmos/api/$p"; }
login() {
  api POST login -d "{\"nickname\":\"admin\",\"password\":\"$PASSWORD\"}" >/dev/null
  api POST sudo -d "{\"password\":\"$PASSWORD\"}" >/dev/null
}
route() { # name host target
  if [ "$(api GET "routes/$1" | jq -r .status)" != OK ]; then
    api POST routes -d "{\"Name\":\"$1\",\"UseHost\":true,\"Host\":\"$2\",\"Target\":\"$3\",\"Mode\":\"SERVAPP\"}" >/dev/null
    sleep 3; login
  fi
}
check() { api POST url-check -d "{\"route\":\"$1\"}" > /tmp/check.json; jq -r '.data.verdict + " " + (.data.reason // "")' /tmp/check.json; }
apply() { api POST url-check/apply -d "{\"route\":\"$1\",\"$2\":$3}" > /tmp/check.json; sleep 3; login; jq -r '.data.verdict' /tmp/check.json; }
repair() { jq -c "[.data.repairs[] | select(.kind == \"$1\")][0]" /tmp/check.json; }
# a browser's request through Cosmos: visit HOST PATH [curl args]; status in $code, headers in /tmp/h, body in /tmp/b
visit() {
  h=$1; p=$2; shift 2
  code=$(curl -sk --resolve "$h:2053:127.0.0.1" -D /tmp/h -o /tmp/b -w '%{http_code}' -c /tmp/vj -b /tmp/vj "$@" "https://$h:2053$p")
}
location() { tr -d '\r' < /tmp/h | sed -n 's/^[Ll]ocation: //p' | head -1; }
browser_post() { # host path data...: as a page of that host posts
  h=$1; p=$2; shift 2
  visit "$h" "$p" -H "Origin: https://$h:2053" -H "Referer: https://$h:2053/" -H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: cors' "$@"
}
# the stand-in for Cloudflare: nginx in front, Host kept with its port, its own X-Forwarded-For added
edge() { # host path [curl args]
  h=$1; p=$2; shift 2
  code=$(curl -s -D /tmp/h -o /tmp/b -w '%{http_code}' -c /tmp/ej -b /tmp/ej -H "Host: $h:2053" "$@" "http://$EDGE:8053$p")
}
wait_for() { # name url-path seconds
  for _ in $(seq 1 "$3"); do
    st=$(curl -s -o /dev/null -w "%{http_code}" "http://$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"):$2" 2>/dev/null)
    case "$st" in 2*|3*|401|403|400|404|421) return 0;; esac
    sleep 2
  done
  say "  FAIL $1 did not answer"; fails=$((fails+1))
}
run() { name=$1; shift; docker rm -f -v "$name" >/dev/null 2>&1; docker run -d --name "$name" --network behind "$@" >/dev/null; }
set_env() { # container NAME=value...: through Cosmos' container edit, as the panel's repair does
  c=$1; shift
  env=$(docker inspect -f '{{json .Config.Env}}' "$c")
  for kv in "$@"; do env=$(echo "$env" | jq -c --arg kv "$kv" --arg n "${kv%%=*}" '[.[] | select(startswith($n + "=") | not)] + [$kv]'); done
  api POST "servapps/$c/update" -d "{\"envVars\":$env}" >/dev/null
  sleep 8
}

login
docker network inspect behind >/dev/null 2>&1 || docker network create --internal --subnet 172.30.0.0/16 behind >/dev/null
# the edge stand-in, reached by the "visitor" at its own address
mkdir -p /tmp/edge && cat > /tmp/edge/default.conf <<'NGINX'
server {
  listen 8053;
  location / {
    proxy_pass https://172.30.0.1:2053;
    proxy_ssl_server_name on;
    proxy_ssl_name $host;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-For 203.0.113.9;
    proxy_set_header CF-Connecting-IP 203.0.113.9;
    proxy_set_header X-Forwarded-Proto https;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $http_connection;
  }
}
NGINX
docker rm -f e2e-edge >/dev/null 2>&1
docker run -d --name e2e-edge --network behind -v /tmp/edge:/etc/nginx/conf.d:ro nginx:1-alpine >/dev/null
EDGE=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' e2e-edge)

qbt() { # container image
  c=$1; image=$2
  say "== $c ($image)"
  run "$c" -e PUID=1000 -e PGID=1000 -e WEBUI_PORT=8080 "$image"
  route "$c-local" "$c.server.local" "http://$c:8080"; route "$c-ext" "$c.example.org" "http://$c:8080"
  wait_for "$c" 8080/ 60
  sleep 3
  pw=$(docker logs "$c" 2>&1 | sed -n 's/.*temporary password is provided for this session: //p' | tail -1 | tr -d '\r ')
  visit "$c.server.local" /; ok "$code" 401 "opens today? 401 Unauthorized"
  ok "$(check "$c-local")" "refused hostPort" "the check says why"
  ok "$(repair route | jq -r '.setting + " " + (.on|tostring) + " " + (.seen|tostring)')" "appOwnHost true true" "it proposes its own address, seen working"
  for r in "$c-local" "$c-ext"; do ok "$(apply "$r" appOwnHost true)" works "after one confirmation $r opens"; done
  visit "$c.server.local" /; ok "$code" 200 "the page opens"
  browser_post "$c.server.local" /api/v2/auth/login --data-urlencode username=admin --data-urlencode "password=$pw"; ok "$code" 204 "sign-in"
  visit "$c.server.local" /api/v2/app/version -H 'Sec-Fetch-Site: same-origin'; ok "$code" 200 "the API, signed in"
  browser_post "$c.server.local" /api/v2/torrents/add --data-urlencode 'urls=magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=e2e'; ok "$code" 200 "a torrent is added (POST)"
  visit "$c.server.local" '/api/v2/sync/maindata?rid=0' -H 'Sec-Fetch-Site: same-origin'; has "$(cat /tmp/b)" 0123456789abcdef0123456789abcdef01234567 "the long poll shows it, still signed in"
  edge "$c.example.org" /api/v2/auth/login -H "Origin: https://$c.example.org:2053" -H 'Sec-Fetch-Site: same-origin' --data-urlencode username=admin --data-urlencode "password=$pw"; ok "$code" 204 "sign-in through the edge stand-in on the domain name"
}

for app in $APPS; do
  case $app in
  qbittorrent) qbt qbittorrent lscr.io/linuxserver/qbittorrent:latest ;;
  qbt-hotio) qbt qbt-hotio ghcr.io/hotio/qbittorrent:release ;;
  qbt-official) qbt qbt-official qbittorrentofficial/qbittorrent-nox:latest ;;
  codeserver)
    say "== code-server"
    run codeserver -e PASSWORD=e2e-code-pass codercom/code-server:latest
    route codeserver-local codeserver.server.local http://codeserver:8080
    wait_for codeserver 8080/healthz 60
    ok "$(check codeserver-local)" "works " "the check: it opens"
    browser_post codeserver.server.local /login --data-urlencode password=e2e-code-pass; ok "$code" 302 "sign-in"
    visit codeserver.server.local '/?reconnectionToken=e2e&reconnection=false&skipWebSocketFrames=false' --http1.1 -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
      -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H 'Origin: https://codeserver.server.local:2053' -m 3
    ok "$code" 101 "the editor's WebSocket (403 before: X-Forwarded-Host lacked the port)"
    ;;
  sonarr|radarr)
    port=8989; [ "$app" = radarr ] && port=7878
    say "== $app"
    run "$app" -e PUID=1000 -e PGID=1000 "lscr.io/linuxserver/$app:latest"
    route "$app-local" "$app.server.local" "http://$app:$port"
    wait_for "$app" "$port/ping" 90
    ok "$(check "$app-local")" "works " "the check: it opens"
    visit "$app.server.local" /; loc=$(location)
    case "$loc" in http://*) ok "$loc" "https or relative" "signed-out redirect";; *) ok yes yes "signed-out redirect: ${loc:-none} (not http on the HTTPS port)";; esac
    ;;
  transmission)
    say "== Transmission with a host list that names one URL"
    run transmission -e PUID=1000 -e PGID=1000 -e HOST_WHITELIST=transmission.server.local lscr.io/linuxserver/transmission:latest
    route transmission-local transmission.server.local http://transmission:9091; route transmission-ext transmission.example.org http://transmission:9091
    wait_for transmission 9091/transmission/web/ 60
    visit transmission.example.org /transmission/rpc -X POST -d '{}'; ok "$code" 421 "the RPC under the second name today"
    ok "$(check transmission-ext)" "warning hostWhitelist" "the check names the variable"
    v=$(repair env | jq -r '.name + "=" + .value'); has "$v" transmission.server.local "with the home name"; has "$v" transmission.example.org "and the domain name"
    set_env transmission "HOST_WHITELIST=transmission.server.local,transmission.example.org"; wait_for transmission 9091/transmission/web/ 60
    ok "$(check transmission-ext)" "works " "after the edit it opens"
    visit transmission.example.org /transmission/rpc -X POST -d '{}'; ok "$code" 409 "the RPC answers its session handshake"
    ;;
  homeassistant)
    say "== Home Assistant"
    run homeassistant ghcr.io/home-assistant/home-assistant:stable
    route homeassistant-local homeassistant.server.local http://homeassistant:8123
    wait_for homeassistant 8123/ 120
    visit homeassistant.server.local /; ok "$code" 400 "opens today? 400"
    ok "$(check homeassistant-local)" "refused untrustedProxy" "the check recognises its log line"
    ok "$(repair route | jq -r '.setting + " " + (.seen|tostring)')" "noVisitorAddress true" "it proposes not to tell the visitor's address, seen working"
    ok "$(apply homeassistant-local noVisitorAddress true)" works "after one confirmation it opens"
    visit homeassistant.server.local /; ok "$code $(location)" "302 /onboarding.html" "the onboarding page"
    ;;
  nextcloud)
    say "== Nextcloud"
    docker rm -f -v nextcloud >/dev/null 2>&1; docker volume rm e2e-nextcloud >/dev/null 2>&1
    # a volume of its own, as a real install has: the edit re-creates the container
    run nextcloud -v e2e-nextcloud:/var/www/html -e SQLITE_DATABASE=nc -e NEXTCLOUD_ADMIN_USER=admin -e NEXTCLOUD_ADMIN_PASSWORD=e2e-nc-pass-1 nextcloud:latest
    route nextcloud-local nextcloud.server.local http://nextcloud:80; route nextcloud-ext nextcloud.example.org http://nextcloud:80
    for _ in $(seq 1 90); do docker exec -u www-data nextcloud php occ status 2>/dev/null | grep -q 'installed: true' && break; sleep 3; done
    wait_for nextcloud 80/status.php 90
    ok "$(check nextcloud-local)" "refused untrustedDomain" "the check recognises the page"
    repair command | jq -r '.lines[]' > /tmp/nc-cmds
    ok "$(wc -l < /tmp/nc-cmds | tr -d ' ')" 2 "one command per name"
    while read -r line; do docker exec -u www-data nextcloud sh -c "$line" >/dev/null; done < /tmp/nc-cmds
    ok "$(check nextcloud-local)" "warning httpLinks" "next: it would make http links"
    set_env nextcloud OVERWRITEPROTOCOL=https; wait_for nextcloud 80/status.php 60
    ok "$(check nextcloud-local)" "works " "it opens"
    visit nextcloud.server.local /login; token=$(sed -n 's/.*data-requesttoken="\([^"]*\)".*/\1/p' /tmp/b | head -1)
    browser_post nextcloud.server.local /login --data-urlencode user=admin --data-urlencode password=e2e-nc-pass-1 --data-urlencode "requesttoken=$token"
    ok "$code $(location)" "303 https://nextcloud.server.local:2053/apps/dashboard/" "sign-in"
    visit nextcloud.example.org /.well-known/caldav; ok "$(location)" "https://nextcloud.example.org:2053/remote.php/dav/" "service discovery stays on https"
    ;;
  paperless)
    say "== Paperless-ngx"
    run paperless-redis redis:7-alpine
    run paperless -e PAPERLESS_REDIS=redis://paperless-redis:6379 -e PAPERLESS_SECRET_KEY=e2e-secret-key-0123456789abcdef -e PAPERLESS_ADMIN_USER=admin -e PAPERLESS_ADMIN_PASSWORD=e2e-pl-pass ghcr.io/paperless-ngx/paperless-ngx:latest
    route paperless-local paperless.server.local http://paperless:8000; route paperless-ext paperless.example.org http://paperless:8000
    wait_for paperless 8000/accounts/login/ 180
    login_pl() {
      rm -f /tmp/vj; visit "$1" /accounts/login/
      csrf=$(sed -n 's/.*name="csrfmiddlewaretoken" value="\([^"]*\)".*/\1/p' /tmp/b | head -1)
      browser_post "$1" /accounts/login/ --data-urlencode "csrfmiddlewaretoken=$csrf" --data-urlencode login=admin --data-urlencode password=e2e-pl-pass
    }
    login_pl paperless.server.local; ok "$code" 403 "sign-in today: CSRF refused"
    ok "$(check paperless-local)" "warning csrfOrigin" "the check says the sign-in will fail"
    v=$(repair env | jq -r '.name + "=" + .value'); ok "$v" "PAPERLESS_CSRF_TRUSTED_ORIGINS=https://paperless.server.local:2053,https://paperless.example.org:2053" "with every address"
    set_env paperless "$v"; wait_for paperless 8000/accounts/login/ 180
    ok "$(check paperless-ext)" "works " "after the edit"
    login_pl paperless.server.local; ok "$code" 302 "sign-in at home"
    login_pl paperless.example.org; ok "$code" 302 "sign-in on the domain name"
    ;;
  forgejo)
    say "== Forgejo"
    run forgejo -e FORGEJO__security__INSTALL_LOCK=true -e FORGEJO__database__DB_TYPE=sqlite3 codeberg.org/forgejo/forgejo:11
    route forgejo-local forgejo.server.local http://forgejo:3000; route forgejo-ext forgejo.example.org http://forgejo:3000
    wait_for forgejo 3000/ 90
    ok "$(check forgejo-ext)" "warning rootUrl" "the check says its links name another address"
    v=$(repair env | jq -r '.name + "=" + .value'); ok "$v" "FORGEJO__server__ROOT_URL=https://forgejo.example.org:2053/" "the domain address"
    set_env forgejo "$v"; wait_for forgejo 3000/ 90
    ok "$(check forgejo-ext)" "works " "after the edit"
    visit forgejo.example.org /; has "$(cat /tmp/b)" "https://forgejo.example.org:2053/" "its page links its address"
    ;;
  vite|webpack)
    say "== $app dev server"
    mkdir -p "/tmp/$app"
    if [ "$app" = vite ]; then
      printf 'FROM node:22-alpine\nWORKDIR /app\nRUN npm create -y vite@latest app -- --template vanilla >/dev/null && cd app && npm install >/dev/null\nWORKDIR /app/app\nCMD ["npx","vite","--host","0.0.0.0","--port","5173"]\n' > /tmp/vite/Dockerfile
      port=5173
    else
      printf 'FROM node:22-alpine\nWORKDIR /app\nRUN npm init -y >/dev/null && npm install webpack webpack-cli webpack-dev-server >/dev/null && mkdir src public && echo "console.log(1)" > src/index.js && echo "<html>dev</html>" > public/index.html\nCMD ["sh","-c","npx webpack serve --mode development --host 0.0.0.0 --port 8080 $DEV_ARGS"]\n' > /tmp/webpack/Dockerfile
      port=8080
    fi
    docker build -q -t "e2e/$app" "/tmp/$app" >/dev/null
    run "$app" "e2e/$app"
    route "$app-local" "$app.server.local" "http://$app:$port"; route "$app-ext" "$app.example.org" "http://$app:$port"
    wait_for "$app" "$port/" 60
    if [ "$app" = vite ]; then
      ok "$(check vite-local)" "refused hostNotAllowed" "the check recognises Vite's answer"
      v=$(repair env | jq -r '.name + "=" + .value'); ok "$v" "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=vite.server.local,vite.example.org" "the variable with every name"
      run vite -e "$v" e2e/vite
    else
      ok "$(check webpack-local)" "refused hostNotAllowed" "the check recognises webpack's answer"
      has "$(repair file | jq -r '.lines | join(" ")')" "'webpack.example.org'," "the lines name every address"
      run webpack -e "DEV_ARGS=--allowed-hosts webpack.server.local --allowed-hosts webpack.example.org" e2e/webpack
    fi
    wait_for "$app" "$port/" 60
    ok "$(check "$app-ext")" "works " "after the change it opens"
    ;;
  esac
done

say "== the result is kept for the panel"
ok "$(api POST url-checks -d '{"routes":["qbittorrent-local","nosuch"]}' | jq -r '.data | keys | join(",")')" "qbittorrent-local" "url-checks answers the known URLs only"
say "fails: $fails"
[ "$fails" = 0 ]
