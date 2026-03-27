package security

import (
	"fmt"
	"strings"
)

// defaultBlockedAPIs are blocked by default in the sandbox.
var defaultBlockedAPIs = []string{
	"fetch",
	"XMLHttpRequest",
	"WebSocket",
	"navigator.sendBeacon",
}

// Sandbox wraps JS evaluations to block dangerous browser APIs.
type Sandbox struct {
	blockedAPIs []string
}

// NewSandbox creates a Sandbox with default blocked APIs.
func NewSandbox() *Sandbox {
	blocked := make([]string, len(defaultBlockedAPIs))
	copy(blocked, defaultBlockedAPIs)
	return &Sandbox{
		blockedAPIs: blocked,
	}
}

// BlockAPI adds an additional API to the block list.
func (s *Sandbox) BlockAPI(api string) {
	for _, existing := range s.blockedAPIs {
		if existing == api {
			return
		}
	}
	s.blockedAPIs = append(s.blockedAPIs, api)
}

// BlockedAPIs returns the current list of blocked APIs.
func (s *Sandbox) BlockedAPIs() []string {
	out := make([]string, len(s.blockedAPIs))
	copy(out, s.blockedAPIs)
	return out
}

// WrapExpression wraps a JS expression in a sandbox that blocks dangerous APIs.
func (s *Sandbox) WrapExpression(expr string) string {
	return s.wrap(expr, false)
}

// WrapFunction wraps a JS function declaration in a sandbox that blocks dangerous APIs.
func (s *Sandbox) WrapFunction(funcDecl string) string {
	return s.wrap(funcDecl, true)
}

func (s *Sandbox) wrap(code string, isFunction bool) string {
	var b strings.Builder

	b.WriteString("(function() {\n")

	// Save originals
	for _, api := range s.blockedAPIs {
		safeName := safeVarName(api)
		b.WriteString(fmt.Sprintf("  const _save_%s = typeof %s !== 'undefined' ? %s : undefined;\n", safeName, api, api))
	}

	// Block APIs
	for _, api := range s.blockedAPIs {
		if strings.Contains(api, ".") {
			// Property on an object (e.g. navigator.sendBeacon)
			parts := strings.SplitN(api, ".", 2)
			b.WriteString(fmt.Sprintf("  try { %s.%s = function() { throw new Error('%s is blocked by VulpineOS sandbox'); }; } catch(e) {}\n", parts[0], parts[1], api))
		} else {
			b.WriteString(fmt.Sprintf("  %s = undefined;\n", api))
		}
	}

	// Execute code in try block
	b.WriteString("  try {\n")
	if isFunction {
		b.WriteString(fmt.Sprintf("    %s\n", code))
	} else {
		b.WriteString(fmt.Sprintf("    return (%s);\n", code))
	}
	b.WriteString("  } finally {\n")

	// Restore originals
	for _, api := range s.blockedAPIs {
		safeName := safeVarName(api)
		if strings.Contains(api, ".") {
			parts := strings.SplitN(api, ".", 2)
			b.WriteString(fmt.Sprintf("    try { %s.%s = _save_%s; } catch(e) {}\n", parts[0], parts[1], safeName))
		} else {
			b.WriteString(fmt.Sprintf("    %s = _save_%s;\n", api, safeName))
		}
	}

	b.WriteString("  }\n")
	b.WriteString("})()")

	return b.String()
}

// safeVarName converts an API name like "navigator.sendBeacon" to "navigator_sendBeacon".
func safeVarName(api string) string {
	return strings.ReplaceAll(api, ".", "_")
}
