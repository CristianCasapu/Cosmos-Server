# Fork addition (plan 50): the guide's PHP script run 40 times on a small database: the bucket keeps
# two objects (db/site.sql, files/site.zip), the rest are versions that the bucket's rule clears, and
# list.php brings back the dump of three runs ago. Run in the examples test's PHP machine, in /work.
set -e
mariadb -e "create database tiny; create table tiny.c (n int); insert into tiny.c values (0); grant all on tiny.* to site@localhost;"
cp backup-config.php /tmp/full-config.php
sed -i "s/'name' => 'site'/'name' => 'tiny'/" backup-config.php
grep -q "=> 'tiny'" backup-config.php
for i in $(seq 1 40); do
  mariadb -e "update tiny.c set n = $i"
  php backup.php > /dev/null
done
php list.php 3 > /tmp/list.txt
versions=$(grep -c '^[0-9][0-9]*  ' /tmp/list.txt)
echo "versions of db/site.sql: $versions"
test "$versions" -ge 40
# the dump of three runs ago is the one of run 38 (mysqldump may break the line after VALUES)
tr -d '\n' < backup-work/restored.sql | grep -Eq "INSERT INTO \`c\` VALUES *\(('38'|38)\)" || { echo "the dump of three runs ago does not hold 38"; grep -A1 'INSERT INTO' backup-work/restored.sql | head -3; exit 1; }
cat > /tmp/count.php <<'PHPEND'
<?php
require 'vendor/autoload.php';
$c = require 'backup-config.php';
$s3 = new Aws\S3\S3Client(['version' => 'latest', 'region' => 'us-east-1', 'endpoint' => getenv('ENDPOINT'), 'use_path_style_endpoint' => true,
    'credentials' => ['key' => $c['key'], 'secret' => $c['secret']], 'http' => is_file('cosmos-ca.pem') ? ['verify' => 'cosmos-ca.pem'] : []]);
foreach (['db/', 'files/'] as $prefix) {
    $n = count($s3->listObjectsV2(['Bucket' => $c['bucket'], 'Prefix' => $prefix])['Contents'] ?? []);
    if ($n !== 1) {
        fwrite(STDERR, "$prefix holds $n objects\n");
        exit(1);
    }
}
echo "two objects, the rest versions\n";
PHPEND
php /tmp/count.php
cp /tmp/full-config.php backup-config.php
rm -f backup-work/restored.sql
