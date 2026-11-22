package httpapi

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/deploys"
)

// Deploys (internal/deploys, docs/plan-deploy-maintenance.md): a customer's CI pipeline quiets alerts while it deploys and asks for a check
// afterwards. Open to deploy keys (which can do nothing else), the owner's own write keys and the owner's session.

// deployOutcome is the body of a finish or a check: 200 when every monitor passed, 422 otherwise, so `curl --fail-with-body` fails the pipeline
// and prints which monitor failed.
func deployOutcome(c *echo.Context, d *deploys.Deploy, checks []deploys.Check, passed bool, verified bool) error {
	// A struct, not a map, so the sentence a person reads in a CI log comes first.
	body := struct {
		Message string          `json:"message"`
		Passed  bool            `json:"passed"`
		Checks  []deploys.Check `json:"checks"`
		Deploy  *deploys.Deploy `json:"deploy,omitempty"`
	}{Passed: passed, Checks: checks, Deploy: d}
	failed := 0
	for _, ch := range checks {
		if !ch.Passed {
			failed++
		}
	}
	switch {
	case !verified:
		body.Message = "Deploy finished: alerts are back on. No check was asked for."
	case passed:
		body.Message = "Every monitor passed its check (" + strconv.Itoa(len(checks)) + " checked)."
	default:
		msg := strconv.Itoa(failed) + " of " + strconv.Itoa(len(checks)) + " monitors failed their check:"
		for _, ch := range checks {
			if !ch.Passed && ch.Error != nil {
				msg += " " + ch.Name + " (" + *ch.Error + ");"
			}
		}
		body.Message = msg[:len(msg)-1] + "."
		return c.JSON(http.StatusUnprocessableEntity, body)
	}
	return c.JSON(http.StatusOK, body)
}

func failedCount(checks []deploys.Check) int {
	n := 0
	for _, ch := range checks {
		if !ch.Passed {
			n++
		}
	}
	return n
}

func (h *handlers) startDeploy(c *echo.Context) error {
	var in deploys.StartInput
	if err := bind(c, &in); err != nil {
		return err
	}
	d, extended, err := h.deploys.Start(c.Request().Context(), currentOrg(c), principalTokenID(c), in)
	if err != nil {
		return mapError(err)
	}
	details := map[string]any{"minutes": in.Minutes, "monitors": d.Monitors, "extended": extended}
	if d.Tag != nil {
		details["tag"] = *d.Tag
	}
	if d.Note != nil {
		details["note"] = *d.Note
	}
	if in.Minutes == 0 {
		details["minutes"] = deploys.DefaultMinutes
	}
	h.recordOrg(c, audit.DeployStarted, target{"deploy", d.ID.String(), deployName(d)}, details)
	status := http.StatusCreated
	if extended {
		status = http.StatusOK
	}
	return c.JSON(status, map[string]any{"deploy": d, "extended": extended})
}

func (h *handlers) finishDeploy(c *echo.Context) error {
	verify := c.QueryParam("verify") != "false"
	d, checks, passed, err := h.deploys.Finish(c.Request().Context(), currentOrg(c), principalTokenID(c), c.Param("deployID"), verify)
	if err != nil {
		return mapError(err)
	}
	details := map[string]any{}
	if verify {
		details["verified"], details["failed"] = passed, failedCount(checks)
	}
	h.recordOrg(c, audit.DeployFinished, target{"deploy", d.ID.String(), deployName(d)}, details)
	return deployOutcome(c, &d, checks, passed, verify)
}

func (h *handlers) checkDeploy(c *echo.Context) error {
	var in deploys.Target
	if err := bind(c, &in); err != nil {
		return err
	}
	checks, passed, err := h.deploys.CheckNow(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.DeployChecked, target{"deploy", "", ""}, map[string]any{"passed": passed, "failed": failedCount(checks)})
	return deployOutcome(c, nil, checks, passed, true)
}

func (h *handlers) listDeploys(c *echo.Context) error {
	list, err := h.deploys.List(c.Request().Context(), currentOrg(c), 20)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"deploys": list})
}

// deployName is how the security log names a deploy: its tag, or "every monitor".
func deployName(d deploys.Deploy) string {
	if d.Tag != nil {
		return *d.Tag
	}
	if d.AllMonitors {
		return "every monitor"
	}
	return strconv.Itoa(d.Monitors) + " monitors"
}
