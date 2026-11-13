package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hawkeye/internal/discovery"
	"hawkeye/internal/monitors"
	"hawkeye/internal/ratelimit"
)

// suggestionsPerHour is the per-organization budget for suggest_monitors, shared with the app's "Set up from your domain" (same limiter key).
const suggestionsPerHour = 30

type SuggestMonitorsInput struct {
	Domain string `json:"domain" jsonschema:"the site's domain, such as example.com (a full address works too; only its host is used)"`
}

// SuggestedMonitor is one proposal, with create_monitor's arguments ready to pass on.
type SuggestedMonitor struct {
	Kind             string             `json:"kind" jsonschema:"website (the home page), health (a health endpoint), api (an API subdomain) or mail (the domain's MX records)"`
	Reason           string             `json:"reason"`
	Recommended      bool               `json:"recommended" jsonschema:"false when the monitor would probably report a working site as down"`
	AlreadyMonitored bool               `json:"already_monitored" jsonschema:"one of the organization's monitors already checks this"`
	CreateWith       CreateMonitorInput `json:"create_with" jsonschema:"the arguments for create_monitor"`
}

type Suggestions struct {
	Domain      string             `json:"domain"`
	Suggestions []SuggestedMonitor `json:"suggestions"`
	Note        string             `json:"note"`
}

func (t *tools) registerSetup(s *mcp.Server) {
	tool := readTool("suggest_monitors", "Suggest monitors for a domain",
		"Looks at a public website from HawkEye's servers and proposes monitors for it: the home page (which also brings SSL certificate and domain "+
			"expiry warnings), a health endpoint if it has one, an api subdomain, and the domain's mail (MX) records. It sends a few GET requests to "+
			"the site and changes nothing; create the ones the person wants with create_monitor. Limited to 30 an hour per organization.")
	tool.Annotations.OpenWorldHint = boolPtr(true)
	add(t, s, tool, access{}, t.suggestMonitors)
}

func (t *tools) suggestMonitors(ctx context.Context, c Caller, in SuggestMonitorsInput) (Suggestions, error) {
	if t.d.Discovery == nil {
		return Suggestions{}, refuse("Suggesting monitors is not available on this server.")
	}
	if t.d.Limiter != nil {
		res, err := t.d.Limiter.Allow(ctx, ratelimit.Key("monitor-suggestions:org", c.OrganizationID.String()), suggestionsPerHour, time.Hour)
		if err == nil && !res.Allowed {
			return Suggestions{}, refuse("This organization has asked for suggestions %d times in the last hour. Try again in %d minutes.",
				suggestionsPerHour, int(res.RetryAfter/time.Minute)+1)
		}
	}
	r, err := t.d.Discovery.Suggest(ctx, in.Domain)
	if err != nil {
		return Suggestions{}, suggestError(err)
	}
	proposed := make([]monitors.Target, len(r.Suggestions))
	for i, sg := range r.Suggestions {
		proposed[i] = monitors.Target{Type: sg.Monitor.Type, URL: sg.Monitor.URL}
		if sg.Monitor.DNSRecordType != nil {
			proposed[i].DNSRecordType = *sg.Monitor.DNSRecordType
		}
	}
	covered, err := t.d.Monitors.Covers(ctx, c.OrganizationID, proposed)
	if err != nil {
		return Suggestions{}, err
	}
	out := Suggestions{Domain: r.Domain, Suggestions: make([]SuggestedMonitor, len(r.Suggestions)),
		Note: "Nothing was created. Show the person these suggestions and create the ones they choose with create_monitor."}
	for i, sg := range r.Suggestions {
		cw := CreateMonitorInput{Name: sg.Monitor.Name}
		switch sg.Monitor.Type {
		case monitors.TypeDNS:
			cw.Type, cw.Host = "dns", sg.Monitor.URL
			if sg.Monitor.DNSRecordType != nil {
				cw.RecordType = *sg.Monitor.DNSRecordType
			}
		default:
			cw.Type, cw.URL = "website", sg.Monitor.URL
		}
		out.Suggestions[i] = SuggestedMonitor{Kind: sg.Kind, Reason: sg.Reason, Recommended: sg.Recommended, AlreadyMonitored: covered[i], CreateWith: cw}
	}
	return out, nil
}

// suggestError turns discovery's errors into messages the assistant can pass on.
func suggestError(err error) error {
	var invalid *discovery.InvalidHostError
	var limited *discovery.TargetLimitedError
	switch {
	case errors.As(err, &invalid):
		return refuse("%s", invalid.Reason)
	case errors.Is(err, discovery.ErrNotFound):
		return refuse("That domain could not be found. Check the spelling.")
	case errors.Is(err, discovery.ErrBlocked):
		return refuse("HawkEye can only set up publicly reachable websites.")
	case errors.As(err, &limited):
		return refuse("That domain was looked at several times recently. Try again in a few minutes.")
	case errors.Is(err, discovery.ErrBusy):
		return refuse("HawkEye is busy looking at other sites. Try again in a few seconds.")
	case errors.Is(err, context.DeadlineExceeded):
		return refuse("The site did not answer in time.")
	}
	return err
}
