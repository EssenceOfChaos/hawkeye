package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hawkeye/internal/audit"
	"hawkeye/internal/checks"
	"hawkeye/internal/monitors"
	"hawkeye/internal/ratelimit"
)

// Multi-step API checks over MCP. Two rules keep them safe to hand to an assistant:
//
//   - Secret values never pass through the conversation. create_workflow takes the names of the secrets the steps use; the workflow is saved
//     paused, and only a person entering the values in HawkEye can turn it on (monitors.ErrSecretsNotSet). A step's credentials must be a
//     placeholder ({{token}} or {{secret.name}}), never a literal password or key.
//   - An assistant can't make HawkEye send state-changing requests on demand: test_workflow refuses drafts with POST, PUT, PATCH or DELETE steps (a
//     saved workflow, which a person approved by saving it, may be run). create_workflow is marked destructive, so Claude asks before saving one,
//     and steps that write need a verified address and run at most every 5 minutes, as in the app.

// testTimeout matches the app's test runs (POST /monitors/test-workflow).
const testTimeout = 25 * time.Second

// testRunsPerHour is shared with the app's test runs (the same limiter key).
const testRunsPerHour = 60

// WorkflowStep is one step as an assistant writes it.
type WorkflowStep struct {
	Name           string              `json:"name" jsonschema:"what the step does, such as Sign in or Get account"`
	Method         string              `json:"method,omitempty" jsonschema:"GET (the default), HEAD, POST, PUT, PATCH, DELETE or OPTIONS"`
	URL            string              `json:"url" jsonschema:"a public http or https address; {{name}} and {{secret.name}} may be used in the path and query, never in the host"`
	Headers        []checks.Header     `json:"headers,omitempty" jsonschema:"extra request headers; values may use {{name}}; never put a password or key here, use auth_value with a secret"`
	Body           string              `json:"body,omitempty" jsonschema:"request body for POST, PUT, PATCH or DELETE; in JSON write values inside quotes, such as \"{{secret.password}}\""`
	AuthType       string              `json:"auth_type,omitempty" jsonschema:"none (the default), basic, bearer or header"`
	AuthUsername   string              `json:"auth_username,omitempty" jsonschema:"for basic"`
	AuthHeaderName string              `json:"auth_header_name,omitempty" jsonschema:"for header, such as X-API-Key"`
	AuthValue      string              `json:"auth_value,omitempty" jsonschema:"the password, token or key, written only as a placeholder: {{token}} saved by an earlier step, or {{secret.name}}"`
	ExpectedStatus int                 `json:"expected_status,omitempty" jsonschema:"the status code the step must return; absent: any status below 400"`
	Checks         []checks.Assertion  `json:"checks,omitempty" jsonschema:"conditions on the response body: body_contains, body_not_contains, or json_path with an operator (equals, not_equals, contains, exists, not_exists)"`
	Save           []checks.Extraction `json:"save,omitempty" jsonschema:"values to save for later steps: {name, from (json_path, header or cookie), path}"`
	TimeoutSeconds int                 `json:"timeout_seconds,omitempty" jsonschema:"1 to 30; default 10"`
}

type CreateWorkflowInput struct {
	Name                 string         `json:"name" jsonschema:"a name for the check, such as Checkout API"`
	Steps                []WorkflowStep `json:"steps" jsonschema:"the requests, in the order they run"`
	Secrets              []string       `json:"secrets,omitempty" jsonschema:"names of the secrets the steps use as {{secret.name}}; a person enters their values in HawkEye, never here"`
	Cookies              bool           `json:"cookies,omitempty" jsonschema:"carry cookies from step to step, for sites that sign in with a session cookie"`
	CheckIntervalMinutes int            `json:"check_interval_minutes,omitempty" jsonschema:"how often to run it; default the plan's fastest (5 minutes when a step writes)"`
}

type TestWorkflowInput struct {
	MonitorID string         `json:"monitor_id,omitempty" jsonschema:"run a saved multi-step API check now, with its stored secrets; give this or steps"`
	Steps     []WorkflowStep `json:"steps,omitempty" jsonschema:"a draft to try, without saving; it can't use secrets or POST, PUT, PATCH or DELETE"`
	Cookies   bool           `json:"cookies,omitempty"`
}

type CreatedWorkflow struct {
	Monitor        MonitorDetail `json:"monitor"`
	SecretsToEnter []string      `json:"secrets_to_enter,omitempty" jsonschema:"secrets a person must enter in HawkEye before the check can run; it is paused until then"`
	EditURL        string        `json:"edit_url" jsonschema:"where a person enters the secrets and reviews the steps"`
	Note           string        `json:"note"`
}

type WorkflowTestResult struct {
	Passed      bool                 `json:"passed"`
	Summary     string               `json:"summary"`
	NeedsFixing bool                 `json:"needs_fixing" jsonschema:"true when the workflow itself is wrong (a value no earlier step saves, or a value to save that isn't in the response), not the API"`
	TotalMs     *int                 `json:"total_ms,omitempty"`
	Steps       []checks.StepOutcome `json:"steps" jsonschema:"the steps that ran, in order; a run stops at the first failure"`
}

// onlyPlaceholder is a credential written as a single {{name}} or {{secret.name}}.
var onlyPlaceholder = regexp.MustCompile(`^\s*\{\{\s*(secret\.)?[A-Za-z_][A-Za-z0-9_]{0,39}\s*\}\}\s*$`)

// toSteps turns the assistant's steps into the monitors package's, refusing literal credentials.
func toSteps(in []WorkflowStep) ([]monitors.StepInput, error) {
	out := make([]monitors.StepInput, len(in))
	for i, st := range in {
		s := monitors.StepInput{Name: st.Name, Method: st.Method, URL: st.URL, Headers: st.Headers, Assertions: st.Checks, Extractions: st.Save}
		if s.Method == "" {
			s.Method = "GET"
		}
		if st.Body != "" {
			b := st.Body
			s.Body = &b
		}
		if st.ExpectedStatus != 0 {
			e := st.ExpectedStatus
			s.ExpectedStatusCode = &e
		}
		if st.TimeoutSeconds != 0 {
			t := st.TimeoutSeconds
			s.TimeoutSeconds = &t
		}
		if st.AuthType != "" && st.AuthType != checks.AuthNone {
			if !onlyPlaceholder.MatchString(st.AuthValue) {
				return nil, refuse("Step %d: auth_value must be a placeholder, such as {{token}} from an earlier step or {{secret.api_key}}. Never send a password, token or key "+
					"through this connection: list the secret's name in secrets, and a person enters its value in HawkEye.", i+1)
			}
			v := st.AuthValue
			s.Auth = &monitors.AuthInput{Type: st.AuthType, Username: st.AuthUsername, HeaderName: st.AuthHeaderName, Secret: &v}
		}
		out[i] = s
	}
	return out, nil
}

func writesAny(steps []monitors.StepInput) bool {
	for _, s := range steps {
		if monitors.IsWriteMethod(s.Method) {
			return true
		}
	}
	return false
}

func (s *tools) createWorkflow(ctx context.Context, c Caller, in CreateWorkflowInput) (CreatedWorkflow, error) {
	steps, err := toSteps(in.Steps)
	if err != nil {
		return CreatedWorkflow{}, err
	}
	if writesAny(steps) && !c.User.EmailVerified() {
		return CreatedWorkflow{}, refuse("Steps that use POST, PUT, PATCH or DELETE need a verified email address on the key's account. Verify it in HawkEye first.")
	}
	ci := monitors.CreateInput{Type: monitors.TypeWorkflow, Name: in.Name, Steps: steps, SecretNames: in.Secrets}
	cookies := in.Cookies
	ci.Cookies = &cookies
	if in.CheckIntervalMinutes > 0 {
		secs := in.CheckIntervalMinutes * 60
		ci.CheckIntervalSeconds = &secs
	}
	m, err := s.d.Monitors.Create(ctx, c.OrganizationID, ci)
	if err != nil {
		return CreatedWorkflow{}, err
	}
	s.record(ctx, c, audit.MonitorCreated, "monitor", m.ID.String(), m.Name, map[string]any{"type": m.Type})
	out := CreatedWorkflow{Monitor: s.detail(m), EditURL: s.appURL("/monitors/" + m.ID.String() + "/edit")}
	if m.Workflow != nil {
		for _, sec := range m.Workflow.Secrets {
			if !sec.Set {
				out.SecretsToEnter = append(out.SecretsToEnter, sec.Name)
			}
		}
	}
	if len(out.SecretsToEnter) > 0 {
		out.Note = fmt.Sprintf("Saved and paused. Ask the person to open %s and enter %s, then turn the check on. Secret values are never sent through this "+
			"connection. After that, test_workflow with this monitor_id runs it once.", out.EditURL, strings.Join(out.SecretsToEnter, ", "))
	} else {
		out.Note = "Saved and running. Use test_workflow with this monitor_id to see each step now."
	}
	return out, nil
}

func (s *tools) testWorkflow(ctx context.Context, c Caller, in TestWorkflowInput) (WorkflowTestResult, error) {
	if s.d.WorkflowChecker == nil {
		return WorkflowTestResult{}, refuse("Testing multi-step API checks is not available on this server.")
	}
	var wf checks.Workflow
	switch {
	case in.MonitorID != "" && len(in.Steps) > 0:
		return WorkflowTestResult{}, refuse("Give either monitor_id or steps, not both.")
	case in.MonitorID != "":
		id, err := parseID(in.MonitorID, monitors.ErrNotFound)
		if err != nil {
			return WorkflowTestResult{}, err
		}
		m, err := s.d.Monitors.Get(ctx, c.OrganizationID, id)
		if err != nil {
			return WorkflowTestResult{}, err
		}
		if m.Type != monitors.TypeWorkflow {
			return WorkflowTestResult{}, refuse("That monitor is not a multi-step API check.")
		}
		if m.Workflow != nil {
			var unset []string
			for _, sec := range m.Workflow.Secrets {
				if !sec.Set {
					unset = append(unset, sec.Name)
				}
			}
			if len(unset) > 0 {
				return WorkflowTestResult{}, &monitors.ErrSecretsNotSet{Names: unset}
			}
		}
		if wf, err = s.d.Monitors.RunnableWorkflow(ctx, c.OrganizationID, id); err != nil {
			return WorkflowTestResult{}, fmt.Errorf("load workflow: %w", err)
		}
	case len(in.Steps) > 0:
		steps, err := toSteps(in.Steps)
		if err != nil {
			return WorkflowTestResult{}, err
		}
		if writesAny(steps) {
			return WorkflowTestResult{}, refuse("A draft with POST, PUT, PATCH or DELETE steps can't be tried from here, because it would change data on demand. " +
				"Save it with create_workflow (the person approves it) and test it by monitor_id, or the person can use Run test in HawkEye.")
		}
		for _, st := range steps {
			if st.Auth != nil && st.Auth.Secret != nil && strings.Contains(*st.Auth.Secret, "secret.") ||
				st.Body != nil && strings.Contains(*st.Body, "{{secret.") || strings.Contains(st.URL, "{{secret.") || headerUsesSecret(st.Headers) {
				return WorkflowTestResult{}, refuse("A draft can't use secrets, because their values never pass through this connection. Save it with " +
					"create_workflow, have the person enter the secrets in HawkEye, then test it by monitor_id.")
			}
		}
		if wf, err = s.d.Monitors.TryWorkflow(ctx, c.OrganizationID, monitors.Draft{Steps: steps, Cookies: in.Cookies}); err != nil {
			return WorkflowTestResult{}, err
		}
	default:
		return WorkflowTestResult{}, refuse("Give monitor_id (a saved check) or steps (a draft).")
	}

	// Test runs send requests from HawkEye's servers on demand: the same hourly budget as the app's Run test.
	if s.d.Limiter != nil {
		res, err := s.d.Limiter.Allow(ctx, ratelimit.Key("test-workflow:org", c.OrganizationID.String()), testRunsPerHour, time.Hour)
		if err == nil && !res.Allowed {
			return WorkflowTestResult{}, refuse("This organization has run %d tests in the last hour. Try again in %d minutes.", testRunsPerHour, int(res.RetryAfter/time.Minute)+1)
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	r, err := s.d.WorkflowChecker.RunWorkflow(runCtx, wf)
	if errors.Is(err, context.DeadlineExceeded) {
		return WorkflowTestResult{}, refuse("The test took longer than 25 seconds, so it was stopped. Scheduled runs allow 60 seconds.")
	}
	if err != nil {
		return WorkflowTestResult{}, err
	}
	out := WorkflowTestResult{Passed: r.Success, NeedsFixing: r.ErrorCategory == checks.CatWorkflowConfig, TotalMs: r.ResponseTimeMs, Steps: r.Steps}
	out.Summary = fmt.Sprintf("All %d steps passed.", len(r.Steps))
	if !r.Success {
		out.Summary = r.ErrorSummary
	}
	return out, nil
}

func headerUsesSecret(hs []checks.Header) bool {
	for _, h := range hs {
		if strings.Contains(h.Value, "{{secret.") {
			return true
		}
	}
	return false
}

// workflowTool is a write tool that reaches services outside HawkEye (the steps' addresses), so it says so.
func workflowTool(name, title, desc string, destructive bool) *mcp.Tool {
	t := writeTool(name, title, desc, destructive, false)
	t.Annotations.OpenWorldHint = boolPtr(true)
	return t
}
