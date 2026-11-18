#!/bin/sh
# Fork addition (plan 50): restores the dump the guide's list.php downloaded into an empty
# database and compares it with the original: every table's checksum, the view, the
# function and the trigger. Run inside the examples test's PHP machine.
set -e
db=$1
dump=/work/backup-work/restored.sql
mariadb -e "drop database if exists $db; create database $db character set utf8mb4"
mariadb "$db" < "$dump"
for t in customers orders a_lines audit big; do
  a=$(mariadb -N -e "checksum table site.$t" | awk '{print $2}')
  b=$(mariadb -N -e "checksum table $db.$t" | awk '{print $2}')
  [ "$a" = "$b" ] || { echo "table $t differs ($a / $b)"; exit 1; }
done
[ "$(mariadb -N -e "select count(*) from $db.big_customers")" = "$(mariadb -N -e "select count(*) from site.big_customers")" ] || { echo "the view differs"; exit 1; }
mariadb -N -e "select $db.double_it(21)" | grep -qx 42 || { echo "the function is missing"; exit 1; }
mariadb -e "insert into $db.orders values (99, 1, 1.00)"
mariadb -N -e "select count(*) from $db.audit where what = 'order 99'" | grep -qx 1 || { echo "the trigger is missing"; exit 1; }
echo "restored into $db and compared: tables, view, trigger, function"
rm -f "$dump"
