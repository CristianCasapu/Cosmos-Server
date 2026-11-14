#!/bin/sh
# Fork addition: what a throwaway instance sees from "the internet", so that
# the security dashboard and the audit page of scripts/fork-ui-gallery.mjs have
# rows: wrong passwords, scanner paths, a forged session, an unknown API token.
# Never point it at the production instance.
#
# Run it from a container with curl whose address is not the one the gallery
# runs from (the server may ban it), once per "visitor":
#   docker run --rm --network <closed test network> --ip <address> \
#     --add-host polish.test:<the instance's address there> \
#     -v "$PWD/scripts:/s:ro" curlimages/curl:8.10.1 sh /s/fork-ui-gallery-noise.sh [logins|probes|all]
B=${BASE:-http://polish.test:18080}
what=${1:-all}
get() { curl -s -o /dev/null -m 5 -w "%{http_code} $1\n" "$B$1"; }
login() { curl -s -o /dev/null -m 5 -w "%{http_code} login as $1\n" -X POST -H 'Content-Type: application/json' -d "{\"nickname\":\"$1\",\"password\":\"$2\"}" "$B/cosmos/api/login"; }

if [ "$what" = logins ] || [ "$what" = all ]; then
  login admin 'admin'; login admin 'password1'; login root 'toor'; login admin 'Summer2026!'
  login maria '123456'; login administrator 'admin123'
fi
if [ "$what" = probes ] || [ "$what" = all ]; then
  for p in /.env /.git/config /wp-login.php /wp-admin/setup-config.php /phpmyadmin/index.php /cgi-bin/luci /actuator/health /.aws/credentials /xmlrpc.php /HNAP1; do get "$p"; done
  curl -s -o /dev/null -m 5 -w '%{http_code} forged session\n' -H 'Cookie: jwttoken=eyJhbGciOiJFZERTQSJ9.e30.AAAA' "$B/cosmos/api/me"
  curl -s -o /dev/null -m 5 -w '%{http_code} unknown API token\n' -H 'Authorization: Bearer cosmos_0000000000000000000000000000000000000000' "$B/cosmos/api/servapps"
fi
