package main

import (
	"fmt"
	"io"

	"vulpineos/internal/extensions"
)

// PrintExtensionsStatus writes the registered extension provider
// availability to w. Operator-visible diagnostic wired into the
// --list-extensions flag. The public build ships with all four
// providers reporting unavailable; alternate builds that register
// providers via build-tagged init() files flip the corresponding
// rows to "available".
func PrintExtensionsStatus(w io.Writer) {
	fmt.Fprintln(w, "VulpineOS extension providers:")
	fmt.Fprintf(w, "  Credentials: %s\n", extensionAvailabilityString(providerAvailable(extensions.Registry.Credentials())))
	fmt.Fprintf(w, "  Audio:       %s\n", extensionAvailabilityString(audioAvailable(extensions.Registry.Audio())))
	fmt.Fprintf(w, "  Mobile:      %s\n", extensionAvailabilityString(mobileAvailable(extensions.Registry.Mobile())))
	fmt.Fprintf(w, "  Sentinel:    %s\n", extensionAvailabilityString(sentinelAvailable(extensions.Registry.Sentinel())))
	fmt.Fprintf(w, "  Captcha:     %s\n", extensionAvailabilityString(captchaAvailable(extensions.Registry.Captcha())))
}

func providerAvailable(p extensions.CredentialProvider) bool {
	return p != nil && p.Available()
}

func audioAvailable(a extensions.AudioCapturer) bool {
	return a != nil && a.Available()
}

func mobileAvailable(m extensions.MobileBridge) bool {
	return m != nil && m.Available()
}

func sentinelAvailable(s extensions.SentinelProvider) bool {
	return s != nil && s.Available()
}

func captchaAvailable(c extensions.CaptchaProvider) bool {
	return c != nil && c.Available()
}

func extensionAvailabilityString(available bool) string {
	if available {
		return "available"
	}
	return "unavailable"
}

// printExtensionStatus is the internal alias used by main.go's
// --list-extensions flag. Keeps the old name call-compatible with
// the existing flag wiring while PrintExtensionsStatus is the
// documented public entrypoint.
func printExtensionStatus(w io.Writer) { PrintExtensionsStatus(w) }
