package profiles

import "github.com/VulpineOS/vulpine-networklab/internal/identity"

var Registry = map[string]func() *identity.NetworkIdentity{
	"firefox131_macos":   Firefox131MacOS,
	"firefox131_windows": Firefox131Windows,
	"firefox131_linux":   Firefox131Linux,
	"chrome132_macos":    Chrome132MacOS,
	"chrome132_windows":  Chrome132Windows,
}

func Get(name string) *identity.NetworkIdentity {
	fn, ok := Registry[name]
	if !ok {
		return nil
	}
	return fn()
}
