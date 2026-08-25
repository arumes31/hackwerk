// Package auth defines HackWerk's authentication and authorization domain.
package auth

// Role is a closed set of internal user roles.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleDriver Role = "driver"
)

// Permission identifies one server-side capability.
type Permission string

const (
	PermissionDashboardView       Permission = "dashboard.view"
	PermissionCalendarViewAll     Permission = "calendar.view_all"
	PermissionCustomerCreate      Permission = "customer.create"
	PermissionCustomerUpdate      Permission = "customer.update"
	PermissionCustomerArchive     Permission = "customer.archive"
	PermissionJobCreate           Permission = "job.create"
	PermissionJobUpdate           Permission = "job.update"
	PermissionJobArchive          Permission = "job.archive"
	PermissionWaitlistAdd         Permission = "waitlist.add"
	PermissionWaitlistPrioritize  Permission = "waitlist.prioritize"
	PermissionAppointmentPlan     Permission = "appointment.plan"
	PermissionAppointmentFix      Permission = "appointment.fix"
	PermissionAppointmentCancel   Permission = "appointment.cancel"
	PermissionAppointmentComplete Permission = "appointment.complete"
	PermissionAvailabilityOwn     Permission = "availability.update_own"
	PermissionAvailabilityOther   Permission = "availability.update_other"
	PermissionResourceManage      Permission = "resource.manage"
	PermissionDriverManage        Permission = "driver.manage"
	PermissionUserManage          Permission = "user.manage"
	PermissionNotificationResend  Permission = "notification.resend"
	PermissionPlanningView        Permission = "planning.view"
	PermissionPlanningAdopt       Permission = "planning.adopt"
	PermissionSettingsManage      Permission = "settings.manage"
	PermissionAuditView           Permission = "audit.view"
	PermissionCalendarFeedOwn     Permission = "calendar_feed.manage_own"
)

var driverPermissions = map[Permission]struct{}{
	PermissionDashboardView:       {},
	PermissionCalendarViewAll:     {},
	PermissionAppointmentComplete: {},
	PermissionCustomerCreate:      {},
	PermissionCustomerUpdate:      {},
	PermissionJobCreate:           {},
	PermissionJobUpdate:           {},
	PermissionWaitlistAdd:         {},
	PermissionAvailabilityOwn:     {},
	PermissionCalendarFeedOwn:     {},
}

// Valid reports whether role is supported.
func (role Role) Valid() bool {
	return role == RoleAdmin || role == RoleDriver
}

// Allows is deny-by-default. Administrators receive the closed application capability set.
func (role Role) Allows(permission Permission) bool {
	if role == RoleAdmin {
		return permission != ""
	}
	if role != RoleDriver {
		return false
	}
	_, allowed := driverPermissions[permission]
	return allowed
}

// Actor is the authenticated identity passed to application services.
type Actor struct {
	UserID             string
	Username           string
	DisplayName        string
	Role               Role
	DriverID           string
	MustChangePassword bool
	UserVersion        int32
	System             bool
}

// Require rejects an actor without the requested capability.
func (actor Actor) Require(permission Permission) error {
	if (!actor.System && actor.UserID == "") || !actor.Role.Allows(permission) {
		return ErrForbidden
	}
	return nil
}
