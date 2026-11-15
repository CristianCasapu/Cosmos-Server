package telegram

// A notification that carries its sentences (utils.NotificationInfo) is
// told with them: the plain sentence of what happened, the object, how
// often it happened, and a link to its details in the panel. The values in
// the sentence are covered by code entities like every other value.

import (
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	sentenceVar      = regexp.MustCompile(`\{\{(\w+)\}\}`)
	hostnameOK       = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	refOK            = regexp.MustCompile(`^[0-9a-f]{16}$`)
	categoryOfSource = map[string]string{
		"containers": CategoryApps,
		"storage":    CategoryStorage,
	}
)

// writeSentence writes an English sentence: its words as they are, its
// values as values.
func writeSentence(c *composer, text string, vars map[string]string, config utils.Config) {
	last := 0
	for _, m := range sentenceVar.FindAllStringSubmatchIndex(text, -1) {
		c.fixed(text[last:m[0]])
		c.value(hideSecrets(vars[text[m[2]:m[3]]], config), maxFreeText)
		last = m[1]
	}
	c.fixed(text[last:])
}

// panelLink is the address of a notification's details, or "" when the
// server has no name to be reached by.
func panelLink(ref string, config utils.Config) string {
	host := strings.TrimSpace(config.HTTPConfig.Hostname)
	if !refOK.MatchString(ref) || !hostnameOK.MatchString(host) || host == "localhost" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	scheme, port := "http", config.HTTPConfig.HTTPPort
	if utils.IsHTTPS {
		scheme = "https"
		if p := upnp.ExternalHTTPSPort(); p > 0 {
			port = strconv.Itoa(p)
		} else {
			port = config.HTTPConfig.HTTPSPort
		}
	}
	address := scheme + "://" + host
	if port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		if _, err := strconv.Atoi(port); err == nil {
			address += ":" + port
		}
	}
	return address + "/cosmos-ui/notifications/r/" + ref
}

func noticeFromInfo(n utils.Notification, config utils.Config) notice {
	info := n.Fork
	category := categoryOfSource[info.Source]
	if category == "" {
		category = CategorySystem
	}
	switch {
	case info.Kind == "alert.disk":
		category = CategoryStorage
	case info.Kind == "alert.triggered" && info.What != nil && appAlertNames[info.What.Vars["name"]]:
		category = CategoryApps
	}
	priority := priorityNormal
	level := strings.ToLower(n.Level)
	if level == "info" || level == "success" || info.Resolves != "" {
		priority = priorityLow
	}
	label := "Notification"
	if info.Title != nil {
		// the title's words without its values, for summaries
		label = strings.TrimSpace(sentenceVar.ReplaceAllString(utils.NotificationEnglish(info.Title.Key), "…"))
	}
	nt := notice{category: category, key: "n:" + info.Kind, label: label, priority: priority, title: label + "."}
	what := ""
	var vars map[string]string
	if info.What != nil {
		what, vars = utils.NotificationEnglish(info.What.Key), info.What.Vars
	}
	link := panelLink(info.Ref, config)
	count, first := info.Count, int64(0)
	if len(info.Seen) > 0 {
		first = info.Seen[0]
	}
	nt.details = func(c *composer) {
		if what != "" {
			writeSentence(c, what, vars, config)
		}
		if info.Subject != nil && info.Subject.Name != "" {
			c.line().fixed("About: ").value(hideSecrets(info.Subject.Name, config), maxValue)
		}
		if count > 1 {
			c.line().fixed("Seen " + strconv.Itoa(count) + " times")
			if first > 0 {
				c.fixed(" since " + utils.MillisToTime(first).Local().Format("2006-01-02 15:04"))
			}
			c.fixed(".")
		}
	}
	if link != "" {
		nt.link = link
	}
	return nt
}
