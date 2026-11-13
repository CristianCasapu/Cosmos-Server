package security

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The dashboard's overview: everything its top half shows, for one period,
// in one request. The periods are computed here, from the server's clock.

// overviewPeriod is a period the dashboard offers and the size of the bars
// of its timeline.
type overviewPeriod struct {
	length time.Duration
	bucket time.Duration
}

var overviewPeriods = map[string]overviewPeriod{
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"30d": {30 * 24 * time.Hour, 24 * time.Hour},
}

// TopAddresses is how many addresses the overview lists.
const TopAddresses = 10

// overviewNow and countryOf are replaced by the tests.
var (
	overviewNow = time.Now
	// countryOf reads the country of an address from the GeoLite file
	// Cosmos ships (no network); "" when the file or the address is unknown.
	countryOf = func(ip string) string {
		country, err := utils.GetIPLocation(ip)
		if err != nil {
			return ""
		}
		return country
	}
)

// GroupStat is what one group did in a window of time.
type GroupStat struct {
	Total int64 `json:"total"`
	// Addresses is the number of different client addresses.
	Addresses int64 `json:"addresses"`
}

// Window is what the incidents of a span of time add up to.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Total and Addresses count the incidents of every group but
	// GroupOther (the mostly harmless ones).
	Total     int64                `json:"total"`
	Addresses int64                `json:"addresses"`
	Groups    map[string]GroupStat `json:"groups"`
	// Kinds is the number of events per kind (only kinds that occurred).
	Kinds map[string]int64 `json:"kinds"`
}

// GroupBucket is one bar of the timeline.
type GroupBucket struct {
	Start  time.Time        `json:"start"`
	Groups map[string]int64 `json:"groups"`
}

// KindTotal is how often one kind occurred.
type KindTotal struct {
	Kind  string `json:"kind"`
	Total int64  `json:"total"`
}

// Address is one of the busiest client addresses and what it did.
type Address struct {
	IP    string    `json:"ip"`
	Total int64     `json:"total"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	// Local is true for an address of the local network (private,
	// loopback, link-local, shared).
	Local bool `json:"local"`
	// Country is the ISO code Cosmos' own GeoLite file gives, "" if none.
	Country string      `json:"country"`
	Kinds   []KindTotal `json:"kinds"`
}

// Overview is the answer of OverviewRoute.
type Overview struct {
	Now    time.Time `json:"now"`
	Period string    `json:"period"`
	// BucketSeconds is the length of a bar of the timeline, ZoneOffset the
	// minutes east of UTC its days start at.
	BucketSeconds int64   `json:"bucketSeconds"`
	ZoneOffset    int     `json:"zoneOffset"`
	Groups        []Group `json:"groups"`
	// Current is the period, Previous the one before it (as long), Recent
	// the last hour and Day the last 24 hours, whatever the period.
	Current  Window        `json:"current"`
	Previous Window        `json:"previous"`
	Recent   Window        `json:"recent"`
	Day      Window        `json:"day"`
	Buckets  []GroupBucket `json:"buckets"`
	Top      []Address     `json:"top"`
}

func emptyGroups() map[string]GroupStat {
	stats := make(map[string]GroupStat, len(groups))
	for _, group := range groups {
		stats[group.ID] = GroupStat{}
	}
	return stats
}

// window adds up the incidents of [from, to).
func window(from time.Time, to time.Time) (Window, error) {
	result := Window{From: from, To: to, Groups: emptyGroups(), Kinds: map[string]int64{}}
	where, args, err := Filter{From: from, To: to, Class: ClassIncident}.where()
	if err != nil {
		return result, err
	}
	db, err := readDB()
	if err != nil {
		return result, err
	}

	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT kind, SUM(n) FROM "+tableName+" WHERE "+where+" GROUP BY kind"), args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var kind string
		var total int64
		if err := rows.Scan(&kind, &total); err != nil {
			rows.Close()
			return result, err
		}
		result.Kinds[kind] = total
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	expression, caseArgs := groupCase()
	rows, err = db.Query(utils.MonitoringRebind(
		"SELECT grp, SUM(n), COUNT(DISTINCT NULLIF(ip, '')) FROM (SELECT "+expression+" AS grp, n, ip FROM "+tableName+
			" WHERE "+where+") grouped GROUP BY grp"), append(caseArgs, args...)...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var group string
		var stat GroupStat
		if err := rows.Scan(&group, &stat.Total, &stat.Addresses); err != nil {
			rows.Close()
			return result, err
		}
		result.Groups[group] = stat
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	hostile := hostileKinds()
	hostileArgs := append([]interface{}{}, args...)
	for _, kind := range hostile {
		hostileArgs = append(hostileArgs, kind)
	}
	err = db.QueryRow(utils.MonitoringRebind(
		"SELECT COALESCE(SUM(n), 0), COUNT(DISTINCT NULLIF(ip, '')) FROM "+tableName+" WHERE "+where+
			" AND kind IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(hostile)), ", ")+")"), hostileArgs...).
		Scan(&result.Total, &result.Addresses)
	return result, err
}

// groupBuckets counts the incidents of [from, to) per group and span of
// size, spans starting at offset east of UTC, oldest first, dense.
func groupBuckets(from time.Time, to time.Time, size time.Duration, offset time.Duration) ([]GroupBucket, error) {
	buckets := []GroupBucket{}
	where, args, err := Filter{From: from, To: to, Class: ClassIncident}.where()
	if err != nil {
		return buckets, err
	}
	sizeMs, offsetMs := size.Milliseconds(), offset.Milliseconds()
	index := func(at time.Time) int64 { return (utils.TimeToMillis(at) + offsetMs) / sizeMs }
	first, last := index(from), index(to.Add(-time.Millisecond))
	if last-first+1 > MaxBuckets {
		return buckets, badFilter("too many buckets")
	}
	for i := first; i <= last; i++ {
		counts := make(map[string]int64, len(groups))
		for _, group := range groups {
			counts[group.ID] = 0
		}
		buckets = append(buckets, GroupBucket{Start: utils.MillisToTime(i*sizeMs - offsetMs), Groups: counts})
	}

	db, err := readDB()
	if err != nil {
		return buckets, err
	}
	expression, caseArgs := groupCase()
	queryArgs := append([]interface{}{offsetMs, sizeMs}, caseArgs...)
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT (ts + ?) / ? AS bucket, grp, SUM(n) FROM (SELECT "+expression+" AS grp, ts, n FROM "+tableName+
			" WHERE "+where+") grouped GROUP BY bucket, grp"), append(queryArgs, args...)...)
	if err != nil {
		return buckets, err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket, total int64
		var group string
		if err := rows.Scan(&bucket, &group, &total); err != nil {
			return buckets, err
		}
		if bucket < first || bucket > last {
			continue
		}
		buckets[bucket-first].Groups[group] += total
	}
	return buckets, rows.Err()
}

// isLocalClient reports whether an address is one of the local network.
func isLocalClient(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return utils.IsLocalIP(ip) || addr.IsLinkLocalUnicast() || addr.IsLoopback()
}

// topAddresses lists the addresses with the most incidents in [from, to)
// and, from one more query, what each of them did.
func topAddresses(from time.Time, to time.Time) ([]Address, error) {
	addresses := []Address{}
	filter := Filter{From: from, To: to, Class: ClassIncident}
	top, err := TopIPs(filter, TopAddresses)
	if err != nil || len(top) == 0 {
		return addresses, err
	}
	byIP := map[string]int{}
	ipArgs := []interface{}{}
	for i, count := range top {
		address := Address{IP: count.IP, Total: count.Total, First: count.First, Last: count.Last, Local: isLocalClient(count.IP), Kinds: []KindTotal{}}
		if !address.Local {
			address.Country = countryOf(count.IP)
		}
		addresses = append(addresses, address)
		byIP[count.IP] = i
		ipArgs = append(ipArgs, count.IP)
	}

	where, args, err := filter.where()
	if err != nil {
		return addresses, err
	}
	db, err := readDB()
	if err != nil {
		return addresses, err
	}
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT ip, kind, SUM(n) FROM "+tableName+" WHERE "+where+
			" AND ip IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(ipArgs)), ", ")+")"+
			" GROUP BY ip, kind ORDER BY ip, SUM(n) DESC, kind"), append(args, ipArgs...)...)
	if err != nil {
		return addresses, err
	}
	defer rows.Close()
	for rows.Next() {
		var ip string
		var kind KindTotal
		if err := rows.Scan(&ip, &kind.Kind, &kind.Total); err != nil {
			return addresses, err
		}
		if i, ok := byIP[ip]; ok {
			addresses[i].Kinds = append(addresses[i].Kinds, kind)
		}
	}
	return addresses, rows.Err()
}

// BuildOverview computes the overview of the period ending now.
func BuildOverview(period string, zoneOffset int) (Overview, error) {
	spec, known := overviewPeriods[period]
	if !known {
		return Overview{}, badFilter("period must be 24h, 7d or 30d")
	}
	if zoneOffset <= -1440 || zoneOffset >= 1440 {
		return Overview{}, badFilter("tz must be a number of minutes between -1439 and 1439")
	}
	now := overviewNow().UTC()
	from := now.Add(-spec.length)
	overview := Overview{
		Now:           now,
		Period:        period,
		BucketSeconds: int64(spec.bucket / time.Second),
		ZoneOffset:    zoneOffset,
		Groups:        Groups(),
	}
	var err error
	if overview.Current, err = window(from, now); err != nil {
		return overview, err
	}
	if overview.Previous, err = window(from.Add(-spec.length), from); err != nil {
		return overview, err
	}
	if overview.Recent, err = window(now.Add(-time.Hour), now); err != nil {
		return overview, err
	}
	if overview.Day, err = window(now.Add(-24*time.Hour), now); err != nil {
		return overview, err
	}
	if overview.Buckets, err = groupBuckets(from, now, spec.bucket, time.Duration(zoneOffset)*time.Minute); err != nil {
		return overview, err
	}
	overview.Top, err = topAddresses(from, now)
	return overview, err
}

// OverviewRoute godoc
// @Summary Security dashboard: overview of a period
// @Description Incidents of the last 24 hours, 7 or 30 days (computed on the server) per group, against the period before, per bar of the timeline, the last hour and day, and the busiest addresses with what they did. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param period query string false "24h (default), 7d or 30d"
// @Param tz query int false "Minutes east of UTC at which days start, -1439 to 1439"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/overview [get]
func OverviewRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	period := query.Get("period")
	if period == "" {
		period = "24h"
	}
	if len(period) > 8 {
		refuse(w, badFilter("period must be 24h, 7d or 30d"))
		return
	}
	offset := 0
	if value := query.Get("tz"); value != "" {
		minutes, err := strconv.Atoi(value)
		if err != nil || len(value) > 8 {
			refuse(w, badFilter("tz must be a number of minutes between -1439 and 1439"))
			return
		}
		offset = minutes
	}
	overview, err := BuildOverview(period, offset)
	if err != nil {
		refuse(w, err)
		return
	}
	answer(w, overview)
}
