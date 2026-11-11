package monitors

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/checks"
	"hawkeye/internal/secrets"
	"hawkeye/internal/validation"
)

// Multi-step API checks ("workflows"): a monitor of type workflow whose requests are its steps (internal/checks/workflow.go). The monitor row's url
// is the first step's address; its other request fields are unused. Steps and secrets live in monitor_steps and monitor_secrets.

const TypeWorkflow = "workflow"

const (
	maxSecretValueChars = 2048
	// workflowTimeoutSeconds is stored as a workflow monitor's own timeout: steps have their own, and a run is bounded by checks.WorkflowTimeout.
	workflowTimeoutSeconds = 30
)

// StepInput is one step as a client sends it. Auth.Secret works as for a monitor: write-only, and when it is left out the secret stored for the step
// at the same position is kept, if its authentication type is unchanged.
type StepInput struct {
	Name               string              `json:"name"`
	Method             string              `json:"method"`
	URL                string              `json:"url"`
	Headers            []checks.Header     `json:"headers"`
	Body               *string             `json:"body"`
	Auth               *AuthInput          `json:"auth"`
	ExpectedStatusCode *int                `json:"expected_status_code"`
	Assertions         []checks.Assertion  `json:"assertions"`
	Extractions        []checks.Extraction `json:"extractions"`
	TimeoutSeconds     *int                `json:"timeout_seconds"`
}

// Step is a stored step as the API shows it: never its secret.
type Step struct {
	Name               string              `json:"name"`
	Method             string              `json:"method"`
	URL                string              `json:"url"`
	Headers            []checks.Header     `json:"headers"`
	Body               *string             `json:"body"`
	Auth               AuthInfo            `json:"auth"`
	ExpectedStatusCode *int                `json:"expected_status_code"`
	Assertions         []checks.Assertion  `json:"assertions"`
	Extractions        []checks.Extraction `json:"extractions"`
	TimeoutSeconds     int                 `json:"timeout_seconds"`
}

// SecretInfo says a secret exists, never its value.
type SecretInfo struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

// Workflow is a workflow monitor's steps and settings, as the API shows them.
type Workflow struct {
	Steps   []Step       `json:"steps"`
	Secrets []SecretInfo `json:"secrets"`
	Cookies bool         `json:"cookies"` // keep cookies from one step to the next during a run
}

// draftStep is a validated step: the stored form plus a new secret, if one was sent.
type draftStep struct {
	Step
	auth       checks.Auth // Secret: the new secret, or "" to keep the stored one (keepSecret)
	keepSecret bool
}

// workflowDraft is a validated workflow, ready to store or run.
type workflowDraft struct {
	steps    []draftStep
	secrets  map[string]*string // new values; nil removes the secret; names not listed are kept (on update)
	declared []string           // secrets named without a value, stored as not set yet
	cookies  bool
}

// validateWorkflow checks a workflow's steps and secrets. cur is the stored workflow on update (nil on create); its step secrets are kept by
// position and its secrets by name. Field errors go into bad, prefixed "steps[i]." or "secrets.".
func validateWorkflow(steps []StepInput, newSecrets map[string]*string, declared []string, cookies bool, cur *storedWorkflow, bad map[string]string) workflowDraft {
	d := workflowDraft{secrets: newSecrets, cookies: cookies}
	if len(steps) == 0 || len(steps) > checks.MaxWorkflowSteps {
		bad["steps"] = fmt.Sprintf("must have between 1 and %d steps", checks.MaxWorkflowSteps)
		return d
	}

	// Which secrets will exist once this change is applied.
	have := map[string]bool{}
	if cur != nil {
		for name := range cur.secrets {
			have[name] = true
		}
	}
	if len(newSecrets) > 0 {
		for name, v := range newSecrets {
			key := "secrets." + name
			switch {
			case !checks.NamePattern.MatchString(name):
				bad["secrets"] = fmt.Sprintf("%q is not a valid name: start with a letter or _, then letters, digits or _ (up to 40)", name)
			case v == nil:
				delete(have, name)
			case *v == "" || utf8.RuneCountInString(*v) > maxSecretValueChars:
				bad[key] = fmt.Sprintf("must be between 1 and %d characters", maxSecretValueChars)
			default:
				have[name] = true
			}
		}
	}
	// Secrets named without a value (by an assistant, through MCP): they exist, and a person enters their values in the app.
	for _, name := range declared {
		if !checks.NamePattern.MatchString(name) {
			bad["secrets"] = fmt.Sprintf("%q is not a valid name: start with a letter or _, then letters, digits or _ (up to 40)", name)
			continue
		}
		if !have[name] {
			have[name] = true
			d.declared = append(d.declared, name)
		}
	}
	if len(have) > checks.MaxWorkflowSecrets {
		bad["secrets"] = fmt.Sprintf("at most %d secrets", checks.MaxWorkflowSecrets)
	}

	extracted := map[string]bool{} // names extracted by the steps before the current one
	for i, in := range steps {
		prefix := fmt.Sprintf("steps[%d].", i)
		put := func(k, msg string) { bad[prefix+k] = msg }
		f := fields{kind: TypeHTTP, name: in.Name, url: in.URL, method: in.Method, expected: in.ExpectedStatusCode, timeout: DefaultTimeoutSeconds,
			headers: slices.Clone(in.Headers), body: in.Body, assertions: slices.Clone(in.Assertions)}
		if in.TimeoutSeconds != nil {
			f.timeout = *in.TimeoutSeconds
		}
		if strings.TrimSpace(f.method) == "" {
			f.method = "GET"
		}
		var curType string
		var curHas bool
		if cur != nil && i < len(cur.steps) {
			curType, curHas = cur.steps[i].Auth.Type, cur.steps[i].Auth.SecretSet
		}
		f.setAuth(in.Auth, curType, curHas)
		if in.Auth == nil {
			f.auth.Type, f.keepSecret = checks.AuthNone, false
		}

		f.name = strings.TrimSpace(f.name)
		if n := utf8.RuneCountInString(f.name); n < 1 || n > 100 {
			put("name", "must be between 1 and 100 characters")
		}
		f.method = strings.ToUpper(strings.TrimSpace(f.method))
		if !slices.Contains(Methods, f.method) {
			put("method", "must be one of "+strings.Join(Methods, ", "))
		}
		if u, reason := normalizeStepURL(f.url); reason != "" {
			put("url", reason)
		} else {
			f.url = u
		}
		if f.expected != nil && (*f.expected < 100 || *f.expected > 599) {
			put("expected_status_code", "must be between 100 and 599, or null")
		}
		if f.timeout < 1 || f.timeout > 30 {
			put("timeout_seconds", "must be between 1 and 30")
		}
		reqBad := map[string]string{}
		f.validateRequest(reqBad)
		for k, v := range reqBad {
			put(k, v)
		}

		if len(in.Extractions) > checks.MaxExtractionsPerStep {
			put("extractions", fmt.Sprintf("at most %d per step", checks.MaxExtractionsPerStep))
		}
		exts := make([]checks.Extraction, len(in.Extractions))
		for j, x := range in.Extractions {
			x.Name, x.From, x.Path = strings.TrimSpace(x.Name), strings.TrimSpace(x.From), strings.TrimSpace(x.Path)
			if err := checks.ValidateExtraction(x); err != nil {
				put(fmt.Sprintf("extractions[%d]", j), err.Error())
			}
			exts[j] = x
		}
		if len(exts) > 0 && f.method == "HEAD" {
			put("extractions", "HEAD responses have no body; use GET to read values")
		}

		// Every value a step uses must exist when it runs: secrets from the workflow, other names from an earlier step.
		texts := map[string]string{"url": f.url, "auth": f.auth.Username + " " + f.auth.Secret}
		if f.body != nil {
			texts["body"] = *f.body
		}
		for j, h := range f.headers {
			texts[fmt.Sprintf("headers[%d].value", j)] = h.Value
		}
		for where, text := range texts {
			for _, name := range checks.Placeholders(text) {
				if secret, ok := strings.CutPrefix(name, "secret."); ok {
					if !have[secret] {
						put(where, fmt.Sprintf("uses {{secret.%s}}, but the workflow has no secret named %s", secret, secret))
					}
				} else if !extracted[name] {
					put(where, fmt.Sprintf("uses {{%s}}, but no earlier step extracts %s", name, name))
				}
			}
		}
		for _, x := range exts {
			extracted[x.Name] = true
		}

		d.steps = append(d.steps, draftStep{
			Step: Step{Name: f.name, Method: f.method, URL: f.url, Headers: f.headers, Body: f.body,
				Auth:               AuthInfo{Type: f.auth.Type, Username: f.auth.Username, HeaderName: f.auth.HeaderName, SecretSet: f.auth.Secret != "" || f.keepSecret},
				ExpectedStatusCode: f.expected, Assertions: f.assertions, Extractions: exts, TimeoutSeconds: f.timeout},
			auth: f.auth, keepSecret: f.keepSecret,
		})
	}
	return d
}

// normalizeStepURL checks a step's address. Placeholders may appear in the path and query, never in the scheme or host: where a step goes is
// written down, not taken from a response (the SSRF-safe client checks it again after substitution in any case).
func normalizeStepURL(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	a, b := placeholderFill(raw, "hawkeyea"), placeholderFill(raw, "hawkeyeb")
	ua, ra := NormalizeURL(a)
	if ra != "" {
		return "", ra
	}
	ub, rb := NormalizeURL(b)
	pa, errA := url.Parse(ua)
	pb, errB := url.Parse(ub)
	if rb != "" || errA != nil || errB != nil || pa.Scheme != pb.Scheme || pa.Host != pb.Host {
		return "", "the scheme and host must be written out; use {{name}} only in the path or query"
	}
	if len(raw) > 2048 {
		return "", "must be at most 2048 characters"
	}
	if raw == a {
		return ua, "" // no placeholders: store the normalized form
	}
	return raw, ""
}

// placeholderFill replaces every placeholder in s with v.
func placeholderFill(s, v string) string { return checks.FillPlaceholders(s, v) }

// storedWorkflow is a workflow as read for an update or a run: the API view, plus sealed values.
type storedWorkflow struct {
	steps       []Step
	stepSecrets []string          // sealed, by position ("" for none)
	secrets     map[string]string // name -> sealed value
	cookies     bool
}

// querier is a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadWorkflow reads a workflow monitor's steps and secrets (sealed).
func loadWorkflow(ctx context.Context, q querier, id uuid.UUID) (*storedWorkflow, error) {
	w := &storedWorkflow{secrets: map[string]string{}}
	if err := q.QueryRow(ctx, `SELECT workflow_cookies FROM monitors WHERE id = $1`, id).Scan(&w.cookies); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT name, method, url, headers, body, auth_type, COALESCE(auth_username, ''), COALESCE(auth_header_name, ''),
			COALESCE(auth_secret, ''), expected_status_code, assertions, extractions, timeout_seconds
		FROM monitor_steps WHERE monitor_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st Step
		var sealed string
		if err := rows.Scan(&st.Name, &st.Method, &st.URL, &st.Headers, &st.Body, &st.Auth.Type, &st.Auth.Username, &st.Auth.HeaderName, &sealed,
			&st.ExpectedStatusCode, &st.Assertions, &st.Extractions, &st.TimeoutSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		st.Auth.SecretSet = sealed != ""
		w.steps = append(w.steps, st)
		w.stepSecrets = append(w.stepSecrets, sealed)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	srows, err := q.Query(ctx, `SELECT name, value FROM monitor_secrets WHERE monitor_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var name string
		var value *string // NULL: declared, not entered yet
		if err := srows.Scan(&name, &value); err != nil {
			return nil, err
		}
		w.secrets[name] = ""
		if value != nil {
			w.secrets[name] = *value
		}
	}
	return w, srows.Err()
}

// LoadWorkflow reads a workflow monitor's steps and the names of its secrets, as the API shows them (never a secret's value).
func LoadWorkflow(ctx context.Context, q querier, id uuid.UUID) (*Workflow, error) {
	w, err := loadWorkflow(ctx, q, id)
	if err != nil {
		return nil, err
	}
	return w.view(), nil
}

func (w *storedWorkflow) view() *Workflow {
	out := &Workflow{Steps: w.steps, Secrets: []SecretInfo{}, Cookies: w.cookies}
	if out.Steps == nil {
		out.Steps = []Step{}
	}
	for name, sealed := range w.secrets {
		out.Secrets = append(out.Secrets, SecretInfo{Name: name, Set: sealed != ""})
	}
	sort.Slice(out.Secrets, func(i, j int) bool { return out.Secrets[i].Name < out.Secrets[j].Name })
	return out
}

// input is a stored step as an update starts from: its credentials are kept (by position) unless a new secret is sent.
func (st Step) input() StepInput {
	t := st.TimeoutSeconds
	return StepInput{Name: st.Name, Method: st.Method, URL: st.URL, Headers: slices.Clone(st.Headers), Body: st.Body,
		Auth:               &AuthInput{Type: st.Auth.Type, Username: st.Auth.Username, HeaderName: st.Auth.HeaderName},
		ExpectedStatusCode: st.ExpectedStatusCode, Assertions: slices.Clone(st.Assertions), Extractions: slices.Clone(st.Extractions), TimeoutSeconds: &t}
}

func stepAAD(monitorID uuid.UUID) string             { return monitorID.String() + "/step" }
func secretAAD(monitorID uuid.UUID, n string) string { return monitorID.String() + "/secret/" + n }

// saveWorkflow writes a validated workflow for monitor id, replacing its steps and applying secret changes. cur is the stored workflow (nil when
// the monitor is new).
func (s *Service) saveWorkflow(ctx context.Context, tx pgx.Tx, id uuid.UUID, d workflowDraft, cur *storedWorkflow) error {
	if s.box == nil {
		return fmt.Errorf("protect credentials: %w", secrets.ErrNoKey)
	}
	if _, err := tx.Exec(ctx, `UPDATE monitors SET workflow_cookies = $2 WHERE id = $1`, id, d.cookies); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitor_steps WHERE monitor_id = $1`, id); err != nil {
		return err
	}
	for i, st := range d.steps {
		var sealed string
		switch {
		case st.auth.Type == checks.AuthNone:
		case st.auth.Secret != "":
			v, err := s.box.Seal(st.auth.Secret, stepAAD(id))
			if err != nil {
				return fmt.Errorf("protect credentials: %w", err)
			}
			sealed = v
		case st.keepSecret && cur != nil && i < len(cur.stepSecrets):
			sealed = cur.stepSecrets[i]
		}
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_steps (monitor_id, position, name, method, url, headers, body, auth_type, auth_username,
				auth_header_name, auth_secret, expected_status_code, assertions, extractions, timeout_seconds)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), $12, $13, $14, $15)`,
			id, i, st.Name, st.Method, st.URL, st.Headers, st.Body, st.auth.Type, st.auth.Username, st.auth.HeaderName, sealed,
			st.ExpectedStatusCode, st.Assertions, st.Extractions, st.TimeoutSeconds); err != nil {
			return err
		}
	}
	for _, name := range d.declared {
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_secrets (monitor_id, name, value) VALUES ($1, $2, NULL) ON CONFLICT (monitor_id, name) DO NOTHING`,
			id, name); err != nil {
			return err
		}
	}
	for name, v := range d.secrets {
		if v == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM monitor_secrets WHERE monitor_id = $1 AND name = $2`, id, name); err != nil {
				return err
			}
			continue
		}
		sealed, err := s.box.Seal(*v, secretAAD(id, name))
		if err != nil {
			return fmt.Errorf("protect credentials: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_secrets (monitor_id, name, value) VALUES ($1, $2, $3)
			ON CONFLICT (monitor_id, name) DO UPDATE SET value = excluded.value`, id, name, sealed); err != nil {
			return err
		}
	}
	return nil
}

// attachWorkflows fills in Workflow for the workflow monitors in ms.
func attachWorkflows(ctx context.Context, q interface {
	Begin(context.Context) (pgx.Tx, error)
}, ms []Monitor) error {
	var any bool
	for _, m := range ms {
		if m.Type == TypeWorkflow {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	return pgx.BeginFunc(ctx, q, func(tx pgx.Tx) error {
		return attachWorkflowsTx(ctx, tx, ms)
	})
}

func attachWorkflowsTx(ctx context.Context, tx pgx.Tx, ms []Monitor) error {
	for i := range ms {
		if ms[i].Type != TypeWorkflow {
			continue
		}
		w, err := loadWorkflow(ctx, tx, ms[i].ID)
		if err != nil {
			return err
		}
		ms[i].Workflow = w.view()
	}
	return nil
}

// usesWriteMethod reports whether any step changes state on its target (such workflows need a verified address and run at most every 5 minutes).
func (d workflowDraft) usesWriteMethod() bool {
	for _, st := range d.steps {
		if IsWriteMethod(st.Method) {
			return true
		}
	}
	return false
}

// StepMethods lists the methods of a workflow input's steps, for the API's verified-email rule.
func StepMethods(steps []StepInput) []string {
	out := make([]string, len(steps))
	for i, st := range steps {
		out[i] = st.Method
	}
	return out
}

// Runnable turns a stored workflow into what the checker runs, opening its sealed values. A value that can't be opened is an operator problem
// (a missing or wrong key), reported as an error so no false failure is recorded.
func Runnable(ctx context.Context, q querier, box *secrets.Box, id uuid.UUID) (checks.Workflow, error) {
	w, err := loadWorkflow(ctx, q, id)
	if err != nil {
		return checks.Workflow{}, err
	}
	out := checks.Workflow{Cookies: w.cookies, Secrets: map[string]string{}}
	for name, sealed := range w.secrets {
		if sealed == "" {
			continue // not entered yet: a step that uses it fails as a workflow that needs fixing (it is paused until then in any case)
		}
		v, err := box.Open(sealed, secretAAD(id, name))
		if err != nil {
			return checks.Workflow{}, fmt.Errorf("open secret %s: %w", name, err)
		}
		out.Secrets[name] = v
	}
	for i, st := range w.steps {
		auth := checks.Auth{Type: st.Auth.Type, Username: st.Auth.Username, HeaderName: st.Auth.HeaderName}
		if w.stepSecrets[i] != "" {
			v, err := box.Open(w.stepSecrets[i], stepAAD(id))
			if err != nil {
				return checks.Workflow{}, fmt.Errorf("open step %d credentials: %w", i+1, err)
			}
			auth.Secret = v
		}
		out.Steps = append(out.Steps, stepTarget(st, auth))
	}
	return out, nil
}

func stepTarget(st Step, auth checks.Auth) checks.Step {
	body := ""
	if st.Body != nil {
		body = *st.Body
	}
	return checks.Step{Name: st.Name, Extractions: st.Extractions, Target: checks.Target{URL: st.URL, Method: st.Method, ExpectedStatus: st.ExpectedStatusCode,
		Timeout: time.Duration(st.TimeoutSeconds) * time.Second, Headers: st.Headers, Body: body, Auth: auth, Assertions: st.Assertions}}
}

// Draft is a workflow sent to be tried once without saving it (POST /monitors/test-workflow).
type Draft struct {
	Steps   []StepInput       `json:"steps"`
	Secrets map[string]string `json:"secrets"`
	Cookies bool              `json:"cookies"`
}

// TryWorkflow validates an unsaved workflow against the organization's plan and returns what the checker runs. Every secret it uses must be sent
// (nothing stored is read), and so must every step's credentials.
func (s *Service) TryWorkflow(ctx context.Context, orgID uuid.UUID, in Draft) (checks.Workflow, error) {
	newSecrets := map[string]*string{}
	for k, v := range in.Secrets {
		newSecrets[k] = &v
	}
	bad := map[string]string{}
	d := validateWorkflow(in.Steps, newSecrets, nil, in.Cookies, nil, bad)
	if len(bad) > 0 {
		return checks.Workflow{}, &validation.Error{Fields: bad}
	}
	if err := s.plans.CheckWorkflowSteps(ctx, s.pool, orgID, len(d.steps)); err != nil {
		return checks.Workflow{}, err
	}
	out := checks.Workflow{Cookies: d.cookies, Secrets: in.Secrets}
	for _, st := range d.steps {
		out.Steps = append(out.Steps, stepTarget(st.Step, st.auth))
	}
	return out, nil
}

// ErrSecretsNotSet is refusing to run a workflow whose secrets have not all been entered.
type ErrSecretsNotSet struct{ Names []string }

func (e *ErrSecretsNotSet) Error() string {
	return "enter the secrets " + strings.Join(e.Names, ", ") + " in HawkEye before turning this check on"
}

// unsetSecrets lists, per workflow among ids, the secrets that are declared but have no value yet.
func unsetSecrets(ctx context.Context, q querier, ids []uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT name FROM monitor_secrets WHERE monitor_id = ANY($1) AND value IS NULL ORDER BY name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RunnableWorkflow is Runnable for one of the organization's own workflow monitors, opened with the service's key (for running a saved workflow
// once, outside the schedule).
func (s *Service) RunnableWorkflow(ctx context.Context, orgID, id uuid.UUID) (checks.Workflow, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM monitors WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, id, orgID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && kind != TypeWorkflow {
		return checks.Workflow{}, ErrNotFound
	}
	if err != nil {
		return checks.Workflow{}, err
	}
	return Runnable(ctx, s.pool, s.box, id)
}
