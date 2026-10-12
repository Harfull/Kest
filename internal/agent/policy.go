package agent

import "github.com/Harfull/Kest/internal/plugin"

// Decision is what the policy says to do with a tool call.
type Decision int

// Policy decisions.
const (
	// Allow runs the tool immediately.
	Allow Decision = iota
	// Confirm asks the owner first.
	Confirm
)

// Decide applies the tool policy from docs/architecture/security.md.
//
//   - read tools run freely.
//   - write tools run freely, unless untrusted content (an email, a web page, a
//     third-party API response) has entered this conversation turn. Then the
//     call may have been steered by that content, so the owner must approve it.
//   - destructive tools always need approval.
//   - outbound tools (anything that sends data to a third party) are treated as
//     destructive no matter what the plugin declared.
//
// It returns the effective risk and, for Confirm, a short reason.
func Decide(risk plugin.Risk, outbound, tainted bool) (eff plugin.Risk, d Decision, reason string) {
	eff = risk
	if outbound {
		eff = plugin.RiskDestructive
	}
	switch eff {
	case plugin.RiskRead:
		return eff, Allow, ""
	case plugin.RiskWrite:
		if tainted {
			return eff, Confirm, "content from outside was read earlier in this conversation"
		}
		return eff, Allow, ""
	default:
		if outbound {
			return eff, Confirm, "this sends information to someone else"
		}
		return eff, Confirm, "this can't easily be undone"
	}
}
