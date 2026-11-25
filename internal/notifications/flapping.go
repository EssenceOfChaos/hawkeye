package notifications

import (
	"strconv"
	"time"

	"hawkeye/internal/email"
)

// Flap episode statuses, as stored (internal/incidents/flap.go).
const (
	flapSettled = "settled"
)

func utc(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func outages(n int) string {
	if n == 1 {
		return "1 outage"
	}
	return strconv.Itoa(n) + " outages"
}

// withFlapNote adds, to the outage alert of a monitor that had been flapping and has now stayed down, why it comes now and not earlier.
func withFlapNote(p []string, a Alert, name string) []string {
	if a.Event != EventOutage || a.FlappingSince == nil {
		return p
	}
	return append(p[:1:1], append([]string{name + " had been flapping since " + utc(*a.FlappingSince) + " (" + outages(a.FlapOutages) +
		"), so HawkEye held back an alert for each outage. This time it has stayed offline, so you are being alerted."}, p[1:]...)...)
}

// flapChatNote is withFlapNote's one line for the chat channels.
func flapChatNote(a Alert) string {
	if a.Event != EventOutage || a.FlappingSince == nil {
		return ""
	}
	return "Flapping since " + utc(*a.FlappingSince) + ", and now staying offline"
}

// renderFlap is the "flapping" alert, and the message when the episode ends: it settled, or flap suppression was turned off (or the monitor paused).
func renderFlap(a Alert, name, url string) email.Message {
	c := email.Content{
		ButtonLabel: "View the monitor", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note: "Turn flap suppression off in the monitor's settings. Manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	if url != "" && !a.IsHeartbeat() {
		label := "Website"
		if a.IsDNS() {
			label = "Host name"
		}
		c.Facts = append(c.Facts, email.Fact{Label: label, Value: url})
	}
	c.Facts = append(c.Facts, email.Fact{Label: "Flapping since", Value: utc(a.StartedAt)}, email.Fact{Label: "Outages", Value: strconv.Itoa(a.FlapOutages)})
	settle := Duration(FlapSettle)
	if a.Event == EventFlapping {
		c.Tone, c.Picture = email.ToneAlert, email.PictureMonitorDown
		c.Heading = name + " keeps going offline and coming back"
		c.Preheader = name + " has had " + outages(a.FlapOutages) + " in the last hour."
		c.Paragraphs = []string{
			"HawkEye has recorded " + outages(a.FlapOutages) + " of " + name + " in the last hour, each followed by a recovery. This is called flapping: " +
				"an overloaded server, a process that keeps crashing and restarting, or a failing node behind a load balancer are common causes.",
			"To spare you an alert for every outage, HawkEye is holding back the outage and recovery alerts for " + name + " while this lasts. " +
				"You will get one message when it has stayed up for " + settle + ", and an alert straight away if it stays offline.",
		}
		if a.Cause != "" {
			c.Facts = append(c.Facts, email.Fact{Label: "Latest result", Value: clean(a.Cause)})
		}
		return c.Message(a.From, a.To, "HawkEye alert: "+name+" is flapping")
	}
	if a.FlapStatus == flapSettled {
		c.Tone = email.ToneSuccess
		c.Heading = name + " has stopped flapping"
		c.Preheader = name + " has stayed up for " + settle + "."
		c.Paragraphs = []string{
			name + " has stayed up for " + settle + " without another outage. HawkEye recorded " + outages(a.FlapOutages) + " while it was flapping.",
			"Its outage and recovery alerts are back on.",
		}
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts, email.Fact{Label: "Flapped for", Value: Duration(a.ResolvedAt.Add(-FlapSettle).Sub(a.StartedAt))})
		}
		return c.Message(a.From, a.To, "HawkEye: "+name+" has stopped flapping")
	}
	c.Heading = "Outage alerts for " + name + " are back on"
	c.Preheader = c.Heading + "."
	c.Paragraphs = []string{"Flap suppression was turned off for " + name + ", or the monitor was paused, so HawkEye no longer holds back its outage " +
		"and recovery alerts."}
	return c.Message(a.From, a.To, "HawkEye: outage alerts for "+name+" are back on")
}

// flapChat is renderFlap for the chat channels.
func flapChat(a Alert, m chatMessage) chatMessage {
	name := clean(a.MonitorName)
	m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	if a.IsHeartbeat() {
		m.Target = ""
	}
	if a.Event == EventFlapping {
		m.Title, m.Tone = "🔁 "+name+" is flapping", toneBad
		m.Lines = []string{outages(a.FlapOutages) + " in the last hour, each followed by a recovery"}
		if a.Cause != "" {
			m.Lines = append(m.Lines, "Latest: "+clean(a.Cause))
		}
		m.Lines = append(m.Lines, "Outage alerts are held back until it has stayed up for "+Duration(FlapSettle)+". You’ll be alerted at once if it stays offline.")
		return m
	}
	if a.FlapStatus == flapSettled {
		m.Title, m.Tone = "✅ "+name+" has stopped flapping", toneGood
		m.Lines = []string{"Up for " + Duration(FlapSettle) + " after " + outages(a.FlapOutages) + ". Outage alerts are back on."}
		return m
	}
	m.Title, m.Tone = "🔔 Outage alerts for "+name+" are back on", toneWarning
	m.Lines = []string{"Flap suppression was turned off, or the monitor was paused."}
	return m
}
