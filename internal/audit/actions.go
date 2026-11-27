package audit

import "strings"

// The actions the log records. An action is "<category>.<verb>"; the web app has a sentence for each (web/src/pages/SecurityLogPage.tsx), and
// TestEveryActionHasACategory keeps this list and the categories in step.

// Account log: things that happen to a person's account.
const (
	SignedUp                 = "account.signed_up"
	SignedIn                 = "account.signed_in"      // details: method (password, google, github, authenticator, recovery_code), via (google, github: before the code)
	SignInFailed             = "account.sign_in_failed" // details: reason (password, code)
	SignedOut                = "account.signed_out"
	PasswordChanged          = "account.password_changed"            // details: other_sessions_ended, first_password (set on an account that had none)
	IdentityConnected        = "account.sign_in_method_connected"    // details: provider (google, github), unverified_account_reset
	IdentityDisconnected     = "account.sign_in_method_disconnected" // details: provider
	PasswordReset            = "account.password_reset"              // details: api_keys_revoked
	EmailVerified            = "account.email_verified"
	TwoFactorEnabled         = "account.two_factor_enabled"
	TwoFactorDisabled        = "account.two_factor_disabled"
	RecoveryCodesRegenerated = "account.recovery_codes_regenerated"
	DataExported             = "account.data_exported"
)

// Organization log: changes to an organization and the things in it.
const (
	OrganizationRenamed     = "organization.renamed" // details: from, to
	OwnershipTransferred    = "organization.ownership_transferred"
	TwoFactorRequirement    = "organization.two_factor_requirement_changed" // details: required
	AIDraftsSetting         = "organization.ai_drafts_changed"              // details: enabled
	AIAnalysisSetting       = "organization.ai_analysis_changed"            // details: enabled
	AlertGroupingSetting    = "organization.alert_grouping_changed"         // details: enabled
	SubscriptionCanceled    = "billing.subscription_canceled"
	MemberInvited           = "member.invited"
	InvitationResent        = "member.invitation_resent"
	InvitationRevoked       = "member.invitation_revoked"
	MemberJoined            = "member.joined"
	MemberRemoved           = "member.removed"
	MemberLeft              = "member.left"     // details: account_deleted
	APIKeyCreated           = "api_key.created" // details: scope, expires_at
	APIKeyRevoked           = "api_key.revoked"
	AppConnected            = "connected_app.connected" // details: scope
	AppDisconnected         = "connected_app.disconnected"
	AlertDestinationAdded   = "alert_destination.added"   // details: channel
	AlertDestinationChanged = "alert_destination.changed" // details: enabled, routing
	AlertDestinationRemoved = "alert_destination.removed"
	MonitorCreated          = "monitor.created"
	MonitorChanged          = "monitor.changed" // details: fields (the names of the settings that were sent)
	MonitorDeleted          = "monitor.deleted"
	MonitorPaused           = "monitor.paused"
	MonitorResumed          = "monitor.resumed"
	BadgeEnabled            = "monitor.badge_enabled"
	BadgeDisabled           = "monitor.badge_disabled"
	PingAddressRotated      = "monitor.ping_address_rotated"
	DNSChangeAccepted       = "monitor.dns_change_accepted" // details: change_id
	StatusPageCreated       = "status_page.created"
	StatusPageChanged       = "status_page.changed"
	StatusPageDeleted       = "status_page.deleted"
	// A status page's custom domain (details: hostname). Set and removed are the owner's; verified, failing and disabled are HawkEye's own checks
	// (actor: system), and failing carries the problem it found.
	StatusPageDomainSet      = "status_page.domain_set"
	StatusPageDomainRemoved  = "status_page.domain_removed"
	StatusPageDomainVerified = "status_page.domain_verified"
	StatusPageDomainFailing  = "status_page.domain_failing" // details: hostname, problem
	StatusPageDomainDisabled = "status_page.domain_disabled"
	MaintenanceScheduled     = "maintenance.scheduled" // details: starts_at, ends_at, monitors
	MaintenanceDeleted       = "maintenance.deleted"
	// Deploys a pipeline reports (internal/deploys): started (details: tag or monitors, minutes, note, extended), finished (details: verified, failed)
	// and a check on its own (details: passed, failed).
	DeployStarted         = "deploy.started"
	DeployFinished        = "deploy.finished"
	DeployChecked         = "deploy.checked"
	IncidentUpdatePosted  = "incident_update.posted" // details: status
	IncidentUpdateDeleted = "incident_update.deleted"
	DomainAdded           = "domain.added"
	DomainRemoved         = "domain.removed"
)

// AccountActions and OrganizationActions list every action of each log.
var (
	AccountActions = []string{SignedUp, SignedIn, SignInFailed, SignedOut, PasswordChanged, PasswordReset, EmailVerified, TwoFactorEnabled,
		TwoFactorDisabled, RecoveryCodesRegenerated, DataExported, IdentityConnected, IdentityDisconnected}
	OrganizationActions = []string{OrganizationRenamed, OwnershipTransferred, TwoFactorRequirement, AIDraftsSetting, AIAnalysisSetting, AlertGroupingSetting, SubscriptionCanceled, MemberInvited,
		InvitationResent, InvitationRevoked, MemberJoined, MemberRemoved, MemberLeft, APIKeyCreated, APIKeyRevoked, AppConnected, AppDisconnected,
		AlertDestinationAdded, AlertDestinationChanged, AlertDestinationRemoved, MonitorCreated, MonitorChanged, MonitorDeleted, MonitorPaused,
		MonitorResumed, BadgeEnabled, BadgeDisabled, PingAddressRotated, DNSChangeAccepted, StatusPageCreated, StatusPageChanged, StatusPageDeleted, StatusPageDomainSet,
		StatusPageDomainRemoved, StatusPageDomainVerified, StatusPageDomainFailing, StatusPageDomainDisabled, MaintenanceScheduled,
		MaintenanceDeleted, DeployStarted, DeployFinished, DeployChecked, IncidentUpdatePosted, IncidentUpdateDeleted, DomainAdded, DomainRemoved}
)

// The categories each log can be filtered by.
var (
	AccountCategories      = categories(AccountActions)
	OrganizationCategories = categories(OrganizationActions)
)

// Category is the part of an action before the dot.
func Category(action string) string {
	c, _, _ := strings.Cut(action, ".")
	return c
}

func categories(actions []string) []string {
	var out []string
	for _, a := range actions {
		if c := Category(a); !contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
