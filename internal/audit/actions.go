package audit

import "strings"

// The actions the log records. An action is "<category>.<verb>"; the web app has a sentence for each (web/src/pages/SecurityLogPage.tsx), and
// TestEveryActionHasACategory keeps this list and the categories in step.

// Account log: things that happen to a person's account.
const (
	SignedUp                 = "account.signed_up"
	SignedIn                 = "account.signed_in"      // details: method (password, authenticator, recovery_code)
	SignInFailed             = "account.sign_in_failed" // details: reason (password, code)
	SignedOut                = "account.signed_out"
	PasswordChanged          = "account.password_changed" // details: other_sessions_ended
	PasswordReset            = "account.password_reset"   // details: api_keys_revoked
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
	StatusPageCreated       = "status_page.created"
	StatusPageChanged       = "status_page.changed"
	StatusPageDeleted       = "status_page.deleted"
	MaintenanceScheduled    = "maintenance.scheduled" // details: starts_at, ends_at, monitors
	MaintenanceDeleted      = "maintenance.deleted"
	IncidentUpdatePosted    = "incident_update.posted" // details: status
	IncidentUpdateDeleted   = "incident_update.deleted"
	DomainAdded             = "domain.added"
	DomainRemoved           = "domain.removed"
)

// AccountActions and OrganizationActions list every action of each log.
var (
	AccountActions = []string{SignedUp, SignedIn, SignInFailed, SignedOut, PasswordChanged, PasswordReset, EmailVerified, TwoFactorEnabled,
		TwoFactorDisabled, RecoveryCodesRegenerated, DataExported}
	OrganizationActions = []string{OrganizationRenamed, OwnershipTransferred, TwoFactorRequirement, SubscriptionCanceled, MemberInvited,
		InvitationResent, InvitationRevoked, MemberJoined, MemberRemoved, MemberLeft, APIKeyCreated, APIKeyRevoked, AppConnected, AppDisconnected,
		AlertDestinationAdded, AlertDestinationChanged, AlertDestinationRemoved, MonitorCreated, MonitorChanged, MonitorDeleted, MonitorPaused,
		MonitorResumed, BadgeEnabled, BadgeDisabled, PingAddressRotated, StatusPageCreated, StatusPageChanged, StatusPageDeleted, MaintenanceScheduled,
		MaintenanceDeleted, IncidentUpdatePosted, IncidentUpdateDeleted, DomainAdded, DomainRemoved}
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
