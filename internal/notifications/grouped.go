package notifications

import (
	"strconv"
	"strings"

	"hawkeye/internal/email"
)

// How many monitors a grouped message names; the rest are counted. Chat messages are shorter, to stay inside every chat app's size limits.
const (
	groupListedEmail = 25
	groupListedChat  = 10
)

// Group combines alerts of one event to one destination into one alert that renders as a single message listing them (see the dispatcher's
// grouping). The first alert supplies the destination and addresses.
func Group(alerts []Alert) Alert {
	if len(alerts) == 1 {
		return alerts[0]
	}
	g := alerts[0]
	return Alert{Event: g.Event, To: g.To, From: g.From, AppBaseURL: g.AppBaseURL, Group: alerts}
}

func monitorsCount(n int) string {
	if n == 1 {
		return "1 monitor"
	}
	return strconv.Itoa(n) + " monitors"
}

// groupWording is a grouped message's title (without emoji) and opening paragraph, by event.
func groupWording(event string, n int) (title, intro string) {
	switch event {
	case EventRecovery:
		return monitorsCount(n) + " are back online", "These monitors recovered within a few minutes of each other, so they are in one message."
	case EventFlapping:
		return monitorsCount(n) + " are flapping", "These monitors keep going offline and coming back. HawkEye is holding back their outage and recovery alerts while " +
			"this lasts, and will tell you when each one settles, or alert you straight away if one stays offline."
	case EventFlappingEnded:
		return monitorsCount(n) + " have stopped flapping", "Their outage and recovery alerts are back on."
	}
	return monitorsCount(n) + " may be offline", "HawkEye confirmed outages of these monitors within a few minutes of each other, so they are in one message " +
		"instead of " + strconv.Itoa(n) + ". When several monitors fail together, they often share a server, a hosting provider or a DNS provider."
}

// memberDetail is what a grouped message says about one of its alerts, after the monitor's name.
func memberDetail(a Alert) string {
	var parts []string
	if a.MonitorURL != "" && !a.IsHeartbeat() {
		parts = append(parts, clean(a.MonitorURL))
	}
	switch a.Event {
	case EventOutage:
		if a.Cause != "" {
			parts = append(parts, clean(a.Cause))
		}
		if a.ResolvedAt != nil {
			parts = append(parts, "back online since "+a.ResolvedAt.UTC().Format("15:04 UTC"))
		}
	case EventRecovery:
		if a.ResolvedAt != nil {
			parts = append(parts, "offline for "+Duration(a.ResolvedAt.Sub(a.StartedAt)))
		}
	case EventFlapping:
		parts = append(parts, outages(a.FlapOutages)+" in the last hour")
	case EventFlappingEnded:
		if a.FlapStatus == flapSettled {
			parts = append(parts, "up for "+Duration(FlapSettle))
		} else {
			parts = append(parts, "flap suppression turned off")
		}
	}
	return strings.Join(parts, " · ")
}

func groupLink(a Alert) string {
	if a.Event == EventFlapping || a.Event == EventFlappingEnded {
		return a.AppBaseURL + "/monitors"
	}
	return a.AppBaseURL + "/incidents"
}

// renderGroup is the email for a group of alerts.
func renderGroup(a Alert) email.Message {
	n := len(a.Group)
	title, intro := groupWording(a.Event, n)
	c := email.Content{
		Heading: title, Preheader: title + ".", Paragraphs: []string{intro},
		ButtonLabel: "Open HawkEye", ButtonURL: groupLink(a), AppBaseURL: a.AppBaseURL,
		Note: "HawkEye groups alerts that arrive together into one message. Turn this off, or manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	subject := "HawkEye alert: " + title
	switch a.Event {
	case EventRecovery:
		c.Tone, subject = email.ToneSuccess, "HawkEye recovery: "+title
	case EventFlappingEnded:
		c.Tone, subject = email.ToneSuccess, "HawkEye: "+title
	default:
		c.Tone, c.Picture = email.ToneAlert, email.PictureMonitorDown
	}
	for i, m := range a.Group {
		if i == groupListedEmail {
			c.Paragraphs = append(c.Paragraphs, "And "+monitorsCount(n-groupListedEmail)+" more: open HawkEye to see them all.")
			break
		}
		c.Facts = append(c.Facts, email.Fact{Label: clean(m.MonitorName), Value: memberDetail(m)})
	}
	if a.Event == EventOutage {
		c.Paragraphs = append(c.Paragraphs, "HawkEye keeps checking them and will tell you when they recover.")
	}
	return c.Message(a.From, a.To, subject)
}

// groupChat is a group of alerts for the chat channels: one line per monitor.
func groupChat(a Alert) chatMessage {
	n := len(a.Group)
	title, _ := groupWording(a.Event, n)
	m := chatMessage{Link: groupLink(a)}
	switch a.Event {
	case EventRecovery:
		m.Title, m.Tone = "✅ "+title, toneGood
	case EventFlapping:
		m.Title, m.Tone = "🔁 "+title, toneBad
	case EventFlappingEnded:
		m.Title, m.Tone = "✅ "+title, toneGood
	default:
		m.Title, m.Tone = "🔴 "+title, toneBad
	}
	for i, x := range a.Group {
		if i == groupListedChat {
			m.Lines = append(m.Lines, "…and "+monitorsCount(n-groupListedChat)+" more")
			break
		}
		line := "• " + clean(x.MonitorName)
		if d := memberDetail(x); d != "" {
			line += ": " + d
		}
		m.Lines = append(m.Lines, truncateRunes(line, 240))
	}
	return m
}
