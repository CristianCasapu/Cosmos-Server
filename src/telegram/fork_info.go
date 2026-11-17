package telegram

// A notification that carries its sentences (utils.NotificationInfo) is
// told with them: the first line from the table of messages.go, what
// happened in the notification's own sentence, what it means and what to
// do from the table, how often it happened, and a button to its details in
// the panel. A notification that ends something (Resolves) is a closing
// message: a reply to the one that opened it.

import (
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

var categoryOfSource = map[string]string{
	"containers": CategoryApps,
	"storage":    CategoryStorage,
}

func noticeFromInfo(n utils.Notification, config utils.Config) notice {
	info := n.Fork
	vars := map[string]string{}
	for _, text := range []*utils.NotificationText{info.Title, info.What, info.Means} {
		if text == nil {
			continue
		}
		for key, value := range text.Vars {
			if _, set := vars[key]; !set {
				vars[key] = value
			}
		}
	}
	if info.Subject != nil && info.Subject.Name != "" {
		vars["subject"] = info.Subject.Name
	}

	nt := newNotice(info.Kind, vars)
	kt := nt.texts()
	if nt.kind == "notification.other" {
		// a kind the table does not know (yet): its own title and sentence
		title := ""
		if info.Title != nil {
			title = utils.RenderNotificationText(*info.Title)
		}
		if title == "" {
			title = info.Kind
		}
		nt.vars["title"] = title
		nt.vars["text"] = utils.RenderNotificationText(*info.What)
		if category := categoryOfSource[info.Source]; category != "" {
			nt.category = category
		} else {
			nt.category = CategorySystem
		}
	} else {
		nt.what = utils.NotificationEnglish(info.What.Key)
	}
	if info.Means != nil && kt.Means == "" {
		nt.means = utils.NotificationEnglish(info.Means.Key)
	}
	if info.Kind == "alert.triggered" && appAlertNames[vars["name"]] {
		nt.category = CategoryApps
	}
	if kt.Level || nt.kind == "notification.other" {
		nt.severity = levelSeverity(n.Level, nt.severity)
	}
	nt.raw = info.Raw
	if nt.kind == "error.job" {
		// plan 48: a job's error can quote its command line; its name is
		// enough here, the rest is in the panel behind the sign-in
		nt.raw = ""
	}
	if strings.TrimSpace(nt.raw) == strings.TrimSpace(utils.RenderNotificationText(*info.What)) {
		nt.raw = "" // said already
	}
	nt.ref = info.Ref
	nt.thread, nt.resolves = info.Thread, info.Resolves
	if info.Resolves != "" {
		nt.closing = true
	}
	nt.repeat = info.Repeat
	nt.count = info.Count
	if len(info.Seen) > 0 && info.Seen[0] > 0 {
		nt.first = utils.MillisToTime(info.Seen[0])
	}
	nt.key = "n:" + info.Kind
	return nt
}
