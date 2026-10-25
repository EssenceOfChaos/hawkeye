package lifecycle

import "hawkeye/internal/email"

// UnsubscribePath is the page linked in each email's footer; UnsubscribeAPIPath is what mail clients POST to for their own "Unsubscribe" button.
const (
	UnsubscribePath    = "/unsubscribe"
	UnsubscribeAPIPath = "/api/v1/lifecycle/unsubscribe"
)

// Build renders the email for a kind. token is the recipient's unsubscribe token. The text is deliberately plain and honest: what to do, how long it
// takes, how to get help, and no countdowns or pressure. Plan limits are not quoted here, so nothing goes stale when a plan changes.
func Build(kind, appBaseURL, address, token string) email.Content {
	start := appBaseURL + "/monitors/new?utm_source=hawkeye&utm_medium=email&utm_campaign=" + kind
	c := email.Content{
		AppBaseURL:         appBaseURL,
		Address:            address,
		UnsubscribeURL:     appBaseURL + UnsubscribePath + "?token=" + token,
		UnsubscribePostURL: appBaseURL + UnsubscribeAPIPath + "?token=" + token,
		ButtonURL:          start,
	}
	switch kind {
	case KindNoMonitor1d:
		c.Preheader = "Add a website in about a minute and HawkEye starts watching it."
		c.Heading = "Ready to watch your first website?"
		c.Paragraphs = []string{
			"Thanks for signing up for HawkEye. We noticed you haven't added a website yet.",
			"It takes about a minute: enter your site's address, choose how often to check it, and save. From then on HawkEye checks it around the clock, emails you when it's confirmed down, and tells you again when it's back.",
		}
		c.ButtonLabel = "Add your first website"
		c.Note = "Stuck, or have a question? Just reply to this email."
	case KindNoMonitor3d:
		c.Preheader = "Outages, expiring certificates and expiring domains, all from one monitor."
		c.Heading = "What one monitor does for you"
		c.Paragraphs = []string{"Here is what HawkEye does as soon as you add a website:"}
		c.Facts = []email.Fact{
			{Label: "Outages", Value: "Checked around the clock. You're emailed when the site is confirmed down, and when it recovers."},
			{Label: "Certificates", Value: "A warning before your SSL certificate expires."},
			{Label: "Domain", Value: "A heads-up before your domain registration runs out."},
		}
		c.ButtonLabel = "Add a website"
		c.Note = "Setup takes about a minute. Questions? Just reply to this email."
	case KindNoMonitor7d:
		c.Preheader = "Tell us what got in the way. Replies come straight to us."
		c.Heading = "Can we help you get started?"
		c.Paragraphs = []string{
			"It's been a week since you joined HawkEye and you haven't added a website yet. That's completely fine, but we'd like to know if something got in the way.",
			"Was a step confusing? Is something you need missing? Or is now just not the right time? Reply to this email with a sentence or two. Replies come straight to us, and they shape what we build next.",
			"If you'd still like to try it, adding a website takes about a minute.",
		}
		c.ButtonLabel = "Add a website"
		c.Note = "This is the last reminder we'll send about getting started."
	}
	return c
}
